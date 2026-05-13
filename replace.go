package selfupdate

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
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
func DownloadAndReplace(asset *Asset, logger func(string, ...interface{}), cfg *DownloadConfig) (*ReplaceResult, error) {
	if logger == nil {
		logger = func(string, ...interface{}) {}
	}
	if cfg == nil {
		cfg = &DownloadConfig{}
	}
	cfg.defaults()

	exePath, err := executablePath()
	if err != nil {
		return nil, fmt.Errorf("get executable path: %w", err)
	}

	tmpFile, err := downloadWithRetry(asset, cfg, logger)
	if err != nil {
		return nil, err
	}
	defer os.Remove(tmpFile)

	logger("validating checksum ...")
	if err := validateSHA256(tmpFile, asset.SHA256); err != nil {
		return nil, err
	}

	logger("replacing binary %s ...", exePath)
	return replaceBinary(exePath, tmpFile)
}

func downloadWithRetry(asset *Asset, cfg *DownloadConfig, logger func(string, ...interface{})) (string, error) {
	var lastErr error
	delay := cfg.RetryDelay
	for attempt := 0; attempt <= cfg.MaxRetries; attempt++ {
		if attempt > 0 {
			logger("retry %d/%d after %v ...", attempt, cfg.MaxRetries, delay)
			time.Sleep(delay)
			delay = delay * 3 / 2
		}

		tmpFile, err := downloadToTemp(asset, cfg, logger)
		if err == nil {
			return tmpFile, nil
		}
		lastErr = err
		logger("download error: %v", err)
	}
	return "", fmt.Errorf("download failed after %d retries: %w", cfg.MaxRetries, lastErr)
}

func downloadToTemp(asset *Asset, cfg *DownloadConfig, logger func(string, ...interface{})) (string, error) {
	client := &http.Client{Timeout: cfg.Timeout}

	resp, err := client.Get(asset.URL)
	if err != nil {
		return "", fmt.Errorf("download: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("download returned HTTP %d", resp.StatusCode)
	}

	f, err := os.CreateTemp("", "selfupdate-*.tmp")
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

			if cfg.Progress != nil && written-lastReportBytes >= 1<<20 {
				cfg.Progress(written, total)
				lastReportBytes = written
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

	if runtime.GOOS != "windows" {
		if err := os.Chmod(f.Name(), 0755); err != nil {
			os.Remove(f.Name())
			return "", fmt.Errorf("chmod: %w", err)
		}
	}

	logger("downloaded %d bytes", written)
	return f.Name(), nil
}

func validateSHA256(path, expected string) error {
	if expected == "" {
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
	os.Remove(backup)

	if err := os.Rename(exePath, backup); err != nil {
		return nil, fmt.Errorf("rename old binary: %w", err)
	}

	if err := copyFile(newBinary, exePath); err != nil {
		os.Rename(backup, exePath)
		return nil, fmt.Errorf("copy new binary: %w", err)
	}

	if runtime.GOOS != "windows" {
		if err := os.Chmod(exePath, 0755); err != nil {
			return nil, fmt.Errorf("chmod new binary: %w", err)
		}
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
func CleanupBackup(backupPath string) error {
	if backupPath == "" {
		return nil
	}
	return os.Remove(backupPath)
}
