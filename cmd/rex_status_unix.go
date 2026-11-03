//go:build !windows

package cmd

import (
	"io"

	"golang.org/x/sys/unix"
)

type rexTTY int

func openRexTTY() (io.WriteCloser, error) {
	fd, err := unix.Open("/dev/tty", unix.O_WRONLY|unix.O_NOCTTY|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	return rexTTY(fd), nil
}

// A blocked or disconnected terminal must never hold up audio playback.
func (t rexTTY) Write(p []byte) (int, error) { return unix.Write(int(t), p) }
func (t rexTTY) Close() error                { return unix.Close(int(t)) }
