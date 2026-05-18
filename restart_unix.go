//go:build !windows

package selfupdate

import (
	"fmt"
	"os"
	"syscall"
)

// Restart replaces the current process with the updated binary.
// Uses the cached exe path from Update() if available, because after
// binary replacement /proc/self/exe may point to the deleted .old file.
func Restart() error {
	return globalRestart("")
}

func restartWith(exePath string) error {
	return globalRestart(exePath)
}

func globalRestart(cachedExePath string) error {
	exePath := cachedExePath
	if exePath == "" {
		path, err := executablePath()
		if err != nil {
			return fmt.Errorf("get exe path for restart: %w", err)
		}
		exePath = path
	}
	return syscall.Exec(exePath, os.Args, os.Environ())
}
