//go:build !windows

package playback

import (
	"errors"
	"io"
	"os"
	"os/exec"
	"syscall"
	"time"
)

// pipeDrainDelay bounds how long reaping waits for the player's output pipes
// once the player itself has exited.
const pipeDrainDelay = time.Second

type unixProc struct {
	cmd *exec.Cmd
}

func startOS(spec Spec, output io.Writer, wantStdout bool) (osProc, io.ReadCloser, error) {
	cmd := exec.Command(spec.Path, spec.Args...)
	cmd.Stdin = spec.Stdin
	cmd.Stdout = output
	cmd.Stderr = output
	cmd.WaitDelay = pipeDrainDelay
	// A separate process group lets the stop sequence reach the player's own
	// children, and keeps a terminal's Ctrl-C from bypassing it.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if lockFile := spec.Lock.File(); lockFile != nil {
		cmd.ExtraFiles = []*os.File{lockFile}
	}

	var stdoutRead, stdoutWrite *os.File
	if wantStdout {
		var err error
		if stdoutRead, stdoutWrite, err = os.Pipe(); err != nil {
			return nil, nil, err
		}
		cmd.Stdout = stdoutWrite
	}
	err := cmd.Start()
	if stdoutWrite != nil {
		stdoutWrite.Close()
	}
	if err != nil {
		if stdoutRead != nil {
			stdoutRead.Close()
		}
		return nil, nil, err
	}
	if stdoutRead == nil {
		return &unixProc{cmd: cmd}, nil, nil
	}
	return &unixProc{cmd: cmd}, stdoutRead, nil
}

func (u *unixProc) pid() int { return u.cmd.Process.Pid }

func (u *unixProc) terminate() error { return u.signalGroup(syscall.SIGTERM) }

func (u *unixProc) kill() error { return u.signalGroup(syscall.SIGKILL) }

func (u *unixProc) wait() error { return u.cmd.Wait() }

// signalGroup signals the player's process group. The caller only signals a
// player it has not yet seen exit, which keeps the window for hitting a
// recycled group ID as small as a group-wide signal allows.
func (u *unixProc) signalGroup(sig syscall.Signal) error {
	err := syscall.Kill(-u.cmd.Process.Pid, sig)
	if errors.Is(err, syscall.ESRCH) {
		return nil
	}
	return err
}
