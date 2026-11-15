//go:build !windows

package playback

import (
	"errors"
	"syscall"
)

// processAlive reports whether pid exists and has not been reaped.
func processAlive(pid int) bool {
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}
