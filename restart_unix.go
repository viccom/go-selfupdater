//go:build !windows

package selfupdate

import (
	"os"
	"syscall"
)

// Restart replaces the current process with the updated binary.
// On Unix, this uses execve so the process PID stays the same.
func Restart() error {
	exePath, err := executablePath()
	if err != nil {
		return err
	}
	return restartWith(exePath)
}

func restartWith(exePath string) error {
	return syscall.Exec(exePath, os.Args, os.Environ())
}
