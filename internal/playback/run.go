// Package playback runs each utterance in its own child process so a stalled
// audio backend is recovered by ending that process, never by resetting state
// inside the server.
package playback

import (
	"context"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/blacktop/mcp-tts/internal/speechlock"
)

// ErrBudgetExceeded reports a player that was stopped for running too long.
var ErrBudgetExceeded = errors.New("playback exceeded its time budget")

// Timing of the stop sequence. Variables so tests can shorten them.
var (
	// diagnosticWindow is how long a diagnostic may observe an overrunning
	// player before it is terminated. It is a fixed delay, not a wait: the
	// diagnostic finishing late, hanging or failing does not extend it.
	diagnosticWindow = 3 * time.Second
	// diagnosticTimeout bounds the diagnostic itself. It is longer than the
	// window because a stack sampler needs the player alive only while it
	// samples, then several more seconds to write its report. Nothing waits
	// for it, so it does not delay the stop sequence.
	diagnosticTimeout = 15 * time.Second
	// killGrace is how long a terminated player has to exit before it is killed.
	killGrace = 2 * time.Second
	// reapGrace bounds the wait for a killed player to be reaped. A process
	// stuck in an uninterruptible kernel call can outlast SIGKILL; the caller
	// must still get control back. An inherited Unix lock remains held by the
	// player until it actually exits.
	reapGrace = 5 * time.Second
)

const outputLimit = 4 << 10

// Incident describes a player that overran its budget.
type Incident struct {
	Label   string
	Player  string
	PID     int
	Elapsed time.Duration
	Budget  time.Duration
}

// Spec describes one playback process.
type Spec struct {
	// Label names the playback in logs and errors.
	Label string
	// Path and Args are the player command.
	Path string
	Args []string
	// Stdin, when set, is written to the player's standard input.
	Stdin io.Reader
	// Budget is how long the player may run. Zero means HardCap.
	Budget time.Duration
	// Lock is the speech lock guarding this playback. On Unix the player
	// inherits it, so the lock stays held for as long as the player lives even
	// if this process dies first. Ignored on Windows, where locks are not
	// inherited and the player takes the lock itself.
	Lock *speechlock.Lock
	// StartupWindow, when positive, makes Start wait that long and report a
	// player that fails within it. It catches early failures only; a player
	// still running afterwards is not thereby known to be audible.
	StartupWindow time.Duration
	// Diagnose, when set, is called once if the budget expires, concurrently
	// with the stop sequence, which does not wait for it. The player is
	// terminated diagnosticWindow later whatever the diagnostic is doing; the
	// diagnostic's context ends after diagnosticTimeout.
	Diagnose func(ctx context.Context, incident Incident)
}

// osProc is the platform's handle on a started player.
type osProc interface {
	pid() int
	// terminate asks the player and its descendants to exit.
	terminate() error
	// kill ends the player and its descendants unconditionally.
	kill() error
	// wait blocks until the player exits and reaps it.
	wait() error
}

// Proc is a started playback process.
type Proc struct {
	spec    Spec
	child   osProc
	output  *boundedBuffer
	started time.Time
	done    chan struct{}
	exitErr error
}

// Start launches the player described by spec.
func Start(spec Spec) (*Proc, error) {
	proc, _, err := start(spec, false)
	if err != nil {
		return nil, err
	}
	if spec.StartupWindow <= 0 {
		return proc, nil
	}
	window := time.NewTimer(spec.StartupWindow)
	defer window.Stop()
	select {
	case <-proc.done:
		if err := proc.exitError(); err != nil {
			return nil, err
		}
	case <-window.C:
	}
	return proc, nil
}

func start(spec Spec, wantStdout bool) (*Proc, io.ReadCloser, error) {
	if spec.Budget <= 0 {
		spec.Budget = HardCap
	}
	output := &boundedBuffer{limit: outputLimit}
	child, stdout, err := startOS(spec, output, wantStdout)
	if err != nil {
		return nil, nil, fmt.Errorf("starting %s: %w", filepath.Base(spec.Path), err)
	}
	spec.Lock.RecordPlayer(child.pid())
	proc := &Proc{
		spec:    spec,
		child:   child,
		output:  output,
		started: time.Now(),
		done:    make(chan struct{}),
	}
	go func() {
		proc.exitErr = child.wait()
		close(proc.done)
	}()
	return proc, stdout, nil
}

// PID returns the player's process ID.
func (p *Proc) PID() int { return p.child.pid() }

// Wait blocks until the player exits. It stops the player when ctx is done or
// when the budget, measured from the start of playback, runs out. Wait always
// returns within a bounded time after either event.
func (p *Proc) Wait(ctx context.Context) error {
	budget := time.NewTimer(time.Until(p.started.Add(p.spec.Budget)))
	defer budget.Stop()
	select {
	case <-p.done:
		return p.exitError()
	case <-ctx.Done():
		if !p.stop(0) {
			return fmt.Errorf("%w; %s (pid %d) could not be killed", ctx.Err(), p.name(), p.PID())
		}
		return ctx.Err()
	case <-budget.C:
	}

	incident := Incident{
		Label:   p.spec.Label,
		Player:  p.spec.Path,
		PID:     p.PID(),
		Elapsed: time.Since(p.started),
		Budget:  p.spec.Budget,
	}
	delay := time.Duration(0)
	if p.spec.Diagnose != nil {
		delay = diagnosticWindow
		diagCtx, cancel := context.WithTimeout(context.Background(), diagnosticTimeout)
		go func() {
			defer cancel()
			p.spec.Diagnose(diagCtx, incident)
		}()
	}
	err := fmt.Errorf("%w: %s ran %s against a budget of %s",
		ErrBudgetExceeded, p.name(), incident.Elapsed.Round(time.Millisecond), incident.Budget)
	if !p.stop(delay) {
		return fmt.Errorf("%w; pid %d could not be killed", err, p.PID())
	}
	return err
}

// stop terminates the player after delay, kills it killGrace later, and
// reports whether it was reaped within reapGrace of the kill.
func (p *Proc) stop(delay time.Duration) bool {
	if p.exited(delay) {
		return true
	}
	_ = p.child.terminate()
	if p.exited(killGrace) {
		return true
	}
	return p.abort()
}

// abort kills the player without a grace period.
func (p *Proc) abort() bool {
	_ = p.child.kill()
	return p.exited(reapGrace)
}

func (p *Proc) exited(within time.Duration) bool {
	if within <= 0 {
		select {
		case <-p.done:
			return true
		default:
			return false
		}
	}
	timer := time.NewTimer(within)
	defer timer.Stop()
	select {
	case <-p.done:
		return true
	case <-timer.C:
		return false
	}
}

func (p *Proc) name() string { return filepath.Base(p.spec.Path) }

// exitError returns the player's exit error with whatever it printed.
func (p *Proc) exitError() error {
	if p.exitErr == nil {
		return nil
	}
	if detail := strings.TrimSpace(p.output.String()); detail != "" {
		return fmt.Errorf("%s: %w: %s", p.name(), p.exitErr, detail)
	}
	return fmt.Errorf("%s: %w", p.name(), p.exitErr)
}

// boundedBuffer keeps the first limit bytes written to it and drops the rest,
// so a chatty player cannot grow the server's memory.
type boundedBuffer struct {
	mu    sync.Mutex
	limit int
	data  []byte
}

func (b *boundedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if room := b.limit - len(b.data); room > 0 {
		b.data = append(b.data, p[:min(room, len(p))]...)
	}
	return len(p), nil
}

func (b *boundedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return string(b.data)
}
