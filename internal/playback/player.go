package playback

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/blacktop/mcp-tts/internal/speechlock"
)

// The hidden player reports its progress on standard output, one line each.
const (
	lineAdmitted = "admitted"
	lineReady    = "ready"
	lineBusy     = "busy"
)

// ErrPlayerNotReady reports a player that took the speech lock but did not
// finish preparing its audio device in time.
var ErrPlayerNotReady = errors.New("audio player did not become ready in time")

// BusyError reports that the speech lock stayed held for the whole admission
// wait. Holder is the lock's recorded owner when Known is true; the record is
// advisory and may be stale.
type BusyError struct {
	Holder speechlock.Holder
	Known  bool
}

func (e *BusyError) Error() string { return "speech lock is busy" }

// Backend is the audio output a hidden player drives.
type Backend interface {
	// Prepare decodes the audio and initializes the output device.
	Prepare() error
	// Play blocks until the audio has finished.
	Play() error
}

// PlayerOptions configure one run of the hidden player.
type PlayerOptions struct {
	// LockPath is the speech lock file. Empty disables serialization.
	LockPath string
	Label    string
	// Admission is how long to wait for the speech lock.
	Admission time.Duration
	// Out receives the progress lines.
	Out io.Writer
}

// RunPlayer is the body of the hidden player process.
//
// The player takes the speech lock itself and holds it until it exits, so the
// lock is released by the kernel exactly when the process that makes sound is
// gone. It prints "admitted" once it holds the lock and "ready" once the audio
// device is prepared; on an admission timeout it prints "busy" with the
// recorded holder and returns a *BusyError.
func RunPlayer(ctx context.Context, opts PlayerOptions, backend Backend) error {
	if opts.LockPath != "" {
		admission, cancel := context.WithTimeout(ctx, opts.Admission)
		lock, err := speechlock.Acquire(admission, opts.LockPath, opts.Label)
		cancel()
		if err != nil {
			if ctx.Err() == nil && errors.Is(err, context.DeadlineExceeded) {
				busy := &BusyError{}
				busy.Holder, busy.Known = speechlock.ReadHolder(opts.LockPath)
				return errors.Join(busy, writeBusyLine(opts.Out, busy))
			}
			return err
		}
		defer lock.Release()
	}
	if _, err := fmt.Fprintln(opts.Out, lineAdmitted); err != nil {
		return fmt.Errorf("reporting admission: %w", err)
	}
	if err := backend.Prepare(); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(opts.Out, lineReady); err != nil {
		return fmt.Errorf("reporting readiness: %w", err)
	}
	return backend.Play()
}

func writeBusyLine(out io.Writer, busy *BusyError) error {
	if !busy.Known {
		_, err := fmt.Fprintln(out, lineBusy)
		return err
	}
	data, err := json.Marshal(busy.Holder)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(out, "%s %s\n", lineBusy, data)
	return err
}

func parseBusyLine(line string) *BusyError {
	busy := &BusyError{}
	if record := strings.TrimSpace(strings.TrimPrefix(line, lineBusy)); record != "" {
		busy.Known = json.Unmarshal([]byte(record), &busy.Holder) == nil
	}
	return busy
}

// PlayerSpec describes a hidden player to launch.
type PlayerSpec struct {
	Label string
	// Exe and Args run the player.
	Exe  string
	Args []string
	// Admission bounds the wait for the player to report "admitted". It should
	// exceed the player's own admission deadline so the player's "busy" answer
	// arrives first.
	Admission time.Duration
	// ReadyTimeout bounds the wait between "admitted" and "ready".
	ReadyTimeout time.Duration
	// Budget is how long playback may run once the player is ready.
	Budget time.Duration
}

// StartPlayer launches a hidden player and waits for it to take the speech
// lock and prepare its audio device. It returns a *BusyError when the lock
// stayed busy, ErrPlayerNotReady when preparation stalled, and ctx.Err() when
// ctx ended first. In every failure case the player has been killed.
func StartPlayer(ctx context.Context, spec PlayerSpec) (*Proc, error) {
	proc, stdout, err := start(Spec{Label: spec.Label, Path: spec.Exe, Args: spec.Args}, true)
	if err != nil {
		return nil, err
	}
	lines := readLines(stdout)

	fail := func(err error) (*Proc, error) {
		proc.abort()
		return nil, err
	}
	if err := awaitLine(ctx, proc, lines, lineAdmitted, spec.Admission, &BusyError{}); err != nil {
		return fail(err)
	}
	if err := awaitLine(ctx, proc, lines, lineReady, spec.ReadyTimeout, ErrPlayerNotReady); err != nil {
		return fail(err)
	}

	// Playback, not admission, is what the budget measures.
	proc.started = time.Now()
	proc.spec.Budget = spec.Budget
	if proc.spec.Budget <= 0 {
		proc.spec.Budget = HardCap
	}
	return proc, nil
}

// readLines forwards the player's output lines and closes the channel at end
// of output. It keeps reading after the handshake so the player can never
// block on a full pipe.
func readLines(stdout io.ReadCloser) <-chan string {
	lines := make(chan string, 4)
	go func() {
		defer close(lines)
		defer stdout.Close()
		scanner := bufio.NewScanner(stdout)
		for scanner.Scan() {
			select {
			case lines <- scanner.Text():
			default:
			}
		}
	}()
	return lines
}

// awaitLine waits for the player to print want. The player's output ends when
// it exits, so a closed channel means it died first.
func awaitLine(ctx context.Context, proc *Proc, lines <-chan string, want string, within time.Duration, onTimeout error) error {
	timer := time.NewTimer(within)
	defer timer.Stop()
	for {
		select {
		case line, open := <-lines:
			if !open {
				return exitBeforeLine(proc, want)
			}
			if line == want {
				return nil
			}
			if line == lineBusy || strings.HasPrefix(line, lineBusy+" ") {
				return parseBusyLine(line)
			}
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
			return onTimeout
		}
	}
}

func exitBeforeLine(proc *Proc, want string) error {
	if !proc.exited(killGrace) {
		return fmt.Errorf("%s closed its output before reporting %q", proc.name(), want)
	}
	if err := proc.exitError(); err != nil {
		return err
	}
	return fmt.Errorf("%s exited before reporting %q", proc.name(), want)
}
