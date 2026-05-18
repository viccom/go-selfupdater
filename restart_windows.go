//go:build windows

package selfupdate

import (
	"fmt"
	"os"
	"os/exec"
	"syscall"
	"time"
)

// Restart starts a new process with the updated binary and exits the current one.
// Uses the cached exe path from Update() if available.
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

	cmd := exec.Command(exePath, os.Args[1:]...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP,
	}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start new process: %w", err)
	}

	// Wait briefly for the new process to confirm it's running.
	// If it exits immediately (e.g. DLL missing, config error), we can
	// report the error instead of silently disappearing.
	done := make(chan error, 1)
	go func() {
		done <- cmd.Wait()
	}()

	select {
	case err := <-done:
		// New process already exited
		if err != nil {
			return fmt.Errorf("new process exited immediately: %v", err)
		}
		// Process exited with code 0 — likely a --version or --help flag.
		// Still proceed with os.Exit since the user requested a restart.
	case <-time.After(2 * time.Second):
		// New process is still running after 2s — assume it's healthy
	}

	os.Exit(0)
	return nil
}
