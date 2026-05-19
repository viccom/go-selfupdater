package selfupdate

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"time"
)

// ProgressFunc is called periodically during download with bytes downloaded and total size.
type ProgressFunc func(downloaded, total int64)

// ReplaceResult holds the result of a binary replacement.
type ReplaceResult struct {
	OldPath    string
	BackupPath string
}

// DownloadConfig controls download behavior.
type DownloadConfig struct {
	MaxRetries int           // default: 3
	RetryDelay time.Duration // default: 2s
	Timeout    time.Duration // default: 5min
	Progress   ProgressFunc
}

func (c *DownloadConfig) defaults() {
	if c.MaxRetries <= 0 {
		c.MaxRetries = 3
	}
	if c.RetryDelay <= 0 {
		c.RetryDelay = 2 * time.Second
	}
	if c.Timeout <= 0 {
		c.Timeout = 5 * time.Minute
	}
}

// DownloadAndReplace downloads the asset, validates it, and atomically replaces
// the current binary.
func DownloadAndReplace(asset *Asset, logger func(string, ...any), cfg *DownloadConfig) (*ReplaceResult, error) {
	if logger == nil {
		logger = func(string, ...any) {}
	}
	if cfg == nil {
		cfg = &DownloadConfig{}
	}
	cfg.defaults()

	exePath, err := executablePath()
	if err != nil {
		return nil, fmt.Errorf("get executable path: %w", err)
	}

	tmpFile, err := downloadWithRetry(asset, cfg, logger, exePath)
	if err != nil {
		return nil, err
	}
	defer os.Remove(tmpFile)

	logger("validating checksum ...")
	if err := validateSHA256(tmpFile, asset.SHA256, logger); err != nil {
		return nil, err
	}

	logger("replacing binary %s ...", exePath)
	return replaceBinary(exePath, tmpFile)
}

func downloadWithRetry(asset *Asset, cfg *DownloadConfig, logger func(string, ...any), exePath string) (string, error) {
	var lastErr error
	delay := cfg.RetryDelay
	for attempt := 0; attempt <= cfg.MaxRetries; attempt++ {
		if attempt > 0 {
			logger("retry %d/%d after %v ...", attempt, cfg.MaxRetries, delay)
			time.Sleep(delay)
			delay = delay * 3 / 2
		}

		tmpFile, err := downloadToTemp(asset, cfg, logger, exePath)
		if err == nil {
			return tmpFile, nil
		}
		lastErr = err
		logger("download error: %v", err)
	}
	return "", fmt.Errorf("download failed after %d retries: %w", cfg.MaxRetries, lastErr)
}

func downloadToTemp(asset *Asset, cfg *DownloadConfig, logger func(string, ...any), exePath string) (string, error) {
	client := &http.Client{Timeout: cfg.Timeout}

	resp, err := client.Get(asset.URL)
	if err != nil {
		return "", fmt.Errorf("download: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("download returned HTTP %d", resp.StatusCode)
	}

	// Create temp file in the same directory as the executable to avoid
	// cross-filesystem issues (double disk usage, rename across mounts).
	exeDir := filepath.Dir(exePath)
	f, err := os.CreateTemp(exeDir, "selfupdate-*.tmp")
	if err != nil {
		return "", fmt.Errorf("create temp file: %w", err)
	}

	total := asset.Size
	if total <= 0 {
		total = resp.ContentLength
	}

	var written int64
	buf := make([]byte, 32*1024)
	var lastReportBytes int64
	var lastReportTime time.Time

	for {
		nr, readErr := resp.Body.Read(buf)
		if nr > 0 {
			nw, writeErr := f.Write(buf[:nr])
			if writeErr != nil {
				f.Close()
				os.Remove(f.Name())
				return "", fmt.Errorf("write: %w", writeErr)
			}
			written += int64(nw)

			// Report progress every 1MB or every 500ms, whichever comes first
			now := time.Now()
			if cfg.Progress != nil && (written-lastReportBytes >= 1<<20 || now.Sub(lastReportTime) >= 500*time.Millisecond) {
				cfg.Progress(written, total)
				lastReportBytes = written
				lastReportTime = now
			}
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			f.Close()
			os.Remove(f.Name())
			return "", fmt.Errorf("read: %w", readErr)
		}
	}

	if cfg.Progress != nil {
		cfg.Progress(written, total)
	}

	f.Close()

	// Validate download size if asset.Size is known
	if asset.Size > 0 && written != asset.Size {
		os.Remove(f.Name())
		return "", fmt.Errorf("downloaded %d bytes, expected %d", written, asset.Size)
	}

	if runtime.GOOS != "windows" {
		if err := os.Chmod(f.Name(), 0755); err != nil {
			os.Remove(f.Name())
			return "", fmt.Errorf("chmod: %w", err)
		}
	}

	logger("downloaded %d bytes", written)
	return f.Name(), nil
}

func validateSHA256(path, expected string, logger func(string, ...any)) error {
	if expected == "" {
		logger("WARNING: SHA256 checksum not provided, skipping integrity verification")
		return nil
	}

	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open for checksum: %w", err)
	}
	defer f.Close()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return fmt.Errorf("hash: %w", err)
	}

	actual := hex.EncodeToString(h.Sum(nil))
	if actual != expected {
		return fmt.Errorf("sha256 mismatch: expected %s, got %s", expected, actual)
	}
	return nil
}

func replaceBinary(exePath, newBinary string) (*ReplaceResult, error) {
	backup := exePath + ".old"

	// Preserve original file metadata for restoration on the new binary.
	origInfo, err := os.Stat(exePath)
	if err != nil {
		return nil, fmt.Errorf("stat current binary: %w", err)
	}

	// Remove stale backup from a previous failed update.
	// Only ignore "file not exist"; permission errors are real problems.
	if err := os.Remove(backup); err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("remove stale backup %s: %w", backup, err)
	}

	if err := os.Rename(exePath, backup); err != nil {
		return nil, fmt.Errorf("rename old binary: %w", err)
	}

	if err := copyFile(newBinary, exePath); err != nil {
		// Attempt rollback: restore the original binary from backup.
		// If rollback also fails, we report both errors — the operator
		// must manually recover (the backup file is still at .old).
		if rollbackErr := os.Rename(backup, exePath); rollbackErr != nil {
			return nil, fmt.Errorf("copy new binary: %w (rollback also failed: %w; backup at %s)", err, rollbackErr, backup)
		}
		return nil, fmt.Errorf("copy new binary (rolled back): %w", err)
	}

	// Restore original file permissions and mode bits.
	if err := os.Chmod(exePath, origInfo.Mode()); err != nil {
		// chmod failure: the new binary is in place but with wrong permissions.
		// Attempt rollback to the old binary which has correct permissions.
		if rollbackErr := os.Rename(backup, exePath); rollbackErr != nil {
			return nil, fmt.Errorf("chmod new binary: %v (rollback also failed: %v; backup at %s)", err, rollbackErr, backup)
		}
		return nil, fmt.Errorf("chmod new binary (rolled back): %w", err)
	}

	return &ReplaceResult{OldPath: exePath, BackupPath: backup}, nil
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0755)
	if err != nil {
		return err
	}
	defer out.Close()

	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return out.Sync()
}

// CleanupBackup removes the backup file created during update.
// Deprecated: Use CleanupStaleBackup instead. On Windows the running process
// holds a lock on the old binary, so this call will fail silently.
// CleanupStaleBackup should be called at program startup.
func CleanupBackup(backupPath string) error {
	return CleanupStaleBackup(backupPath)
}

// CleanupStaleBackup removes a leftover .old backup from a previous update.
// It should be called at program startup, not immediately after replacement.
// On Windows, the previous process has already exited by then so the file is
// no longer locked. If the file does not exist, no error is returned.
func CleanupStaleBackup(backupPath string) error {
	if backupPath == "" {
		return nil
	}
	err := os.Remove(backupPath)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}
