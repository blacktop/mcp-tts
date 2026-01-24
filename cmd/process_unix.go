//go:build !windows

package cmd

import (
	"os"
	"syscall"
)

// isProcessAlive checks if a process with the given PID is still running.
// On Unix systems, we use signal 0 which is a no-op that tests process existence.
func isProcessAlive(pid int) bool {
	process, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	// Signal 0 doesn't actually send a signal, but returns an error if the
	// process doesn't exist or we don't have permission to signal it.
	// ESRCH = no such process, EPERM = process exists but no permission
	err = process.Signal(syscall.Signal(0))
	if err == nil {
		return true
	}
	// If we get EPERM, the process exists but we can't signal it
	if err == syscall.EPERM {
		return true
	}
	return false
}
