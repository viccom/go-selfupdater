package selfupdate

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
)

// DefaultLogger returns a simple logger that prints to stderr.
func DefaultLogger() func(string, ...interface{}) {
	return log.Printf
}

// executablePath returns the resolved path of the current executable.
func executablePath() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("get executable: %w", err)
	}
	exe, err = filepath.EvalSymlinks(exe)
	if err != nil {
		return "", fmt.Errorf("resolve symlinks: %w", err)
	}
	return exe, nil
}
