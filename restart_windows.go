//go:build windows

package selfupdate

import (
	"os"
	"os/exec"
	"syscall"
)

// Restart starts a new process with the updated binary and exits the current one.
func Restart() error {
	exePath, err := executablePath()
	if err != nil {
		return err
	}
	return restartWith(exePath)
}

func restartWith(exePath string) error {
	cmd := exec.Command(exePath, os.Args[1:]...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP,
	}

	if err := cmd.Start(); err != nil {
		return err
	}

	os.Exit(0)
	return nil
}
