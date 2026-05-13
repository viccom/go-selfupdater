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

// ReplaceResult holds the result of a binary replacement.
type ReplaceResult struct {
	OldPath    string
	BackupPath string
}

// DownloadAndReplace downloads the asset, validates it, and atomically replaces
// the current binary. On success it returns the backup path (caller should clean up).
func DownloadAndReplace(asset *Asset, logger func(string, ...interface{})) (*ReplaceResult, error) {
	if logger == nil {
		logger = func(string, ...interface{}) {}
	}

	exePath, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("get executable path: %w", err)
	}
	exePath, err = filepath.EvalSymlinks(exePath)
	if err != nil {
		return nil, fmt.Errorf("resolve symlinks: %w", err)
	}

	logger("downloading %s ...", asset.URL)
	tmpFile, err := downloadToTemp(asset)
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

func downloadToTemp(asset *Asset) (string, error) {
	client := &http.Client{Timeout: 5 * time.Minute}

	resp, err := client.Get(asset.URL)
	if err != nil {
		return "", fmt.Errorf("download: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("download returned %d", resp.StatusCode)
	}

	f, err := os.CreateTemp("", "selfupdate-*.tmp")
	if err != nil {
		return "", fmt.Errorf("create temp file: %w", err)
	}

	if _, err := io.Copy(f, io.LimitReader(resp.Body, 500<<20)); err != nil {
		f.Close()
		os.Remove(f.Name())
		return "", fmt.Errorf("write download: %w", err)
	}
	f.Close()

	if runtime.GOOS != "windows" {
		if err := os.Chmod(f.Name(), 0755); err != nil {
			os.Remove(f.Name())
			return "", fmt.Errorf("chmod: %w", err)
		}
	}

	return f.Name(), nil
}

func validateSHA256(path, expected string) error {
	if expected == "" {
		return nil
	}

	f, err := os.Open(path)
	if err != nil {
		return err
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
	if runtime.GOOS == "windows" {
		return replaceWindows(exePath, newBinary)
	}
	return replaceUnix(exePath, newBinary)
}

func replaceUnix(exePath, newBinary string) (*ReplaceResult, error) {
	backup := exePath + ".old"
	os.Remove(backup)

	if err := os.Rename(exePath, backup); err != nil {
		return nil, fmt.Errorf("rename old binary: %w", err)
	}

	if err := copyFile(newBinary, exePath); err != nil {
		// rollback
		os.Rename(backup, exePath)
		return nil, fmt.Errorf("copy new binary: %w", err)
	}

	if err := os.Chmod(exePath, 0755); err != nil {
		return nil, fmt.Errorf("chmod new binary: %w", err)
	}

	return &ReplaceResult{OldPath: exePath, BackupPath: backup}, nil
}

func replaceWindows(exePath, newBinary string) (*ReplaceResult, error) {
	backup := exePath + ".old"
	os.Remove(backup)

	if err := os.Rename(exePath, backup); err != nil {
		return nil, fmt.Errorf("rename old binary: %w", err)
	}

	if err := copyFile(newBinary, exePath); err != nil {
		os.Rename(backup, exePath)
		return nil, fmt.Errorf("copy new binary: %w", err)
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
