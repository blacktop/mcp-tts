//go:build !windows

package playback

import (
	"bufio"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/blacktop/mcp-tts/internal/speechlock"
)

func TestBudgetKillsPlayerThatIgnoresTerminate(t *testing.T) {
	spec := helperSpec("ignore-term")
	spec.Budget = 100 * time.Millisecond
	proc := mustStart(t, spec)

	began := time.Now()
	err := proc.Wait(context.Background())
	took := time.Since(began)
	if !errors.Is(err, ErrBudgetExceeded) {
		t.Fatalf("Wait = %v, want ErrBudgetExceeded", err)
	}
	if took < spec.Budget+killGrace {
		t.Fatalf("player gone after %s: it must have honored the terminate it was meant to ignore", took)
	}
	if processAlive(proc.PID()) {
		t.Fatalf("player %d ignored terminate and was not killed", proc.PID())
	}
}

func TestStopReachesThePlayersChildren(t *testing.T) {
	pidPath := filepath.Join(t.TempDir(), "child.pid")
	spec := helperSpec("parent-of", pidPath)
	spec.Budget = 400 * time.Millisecond
	proc := mustStart(t, spec)

	var childPID int
	deadline := time.Now().Add(5 * time.Second)
	for childPID == 0 {
		if data, err := os.ReadFile(pidPath); err == nil && len(data) > 0 {
			childPID, _ = strconv.Atoi(string(data))
		}
		if time.Now().After(deadline) {
			t.Fatal("player never started its child")
		}
		time.Sleep(10 * time.Millisecond)
	}

	if err := proc.Wait(context.Background()); !errors.Is(err, ErrBudgetExceeded) {
		t.Fatalf("Wait = %v, want ErrBudgetExceeded", err)
	}
	waitGone(t, childPID, 5*time.Second)
}

func TestSpeechLockIsFreeAfterBudgetStop(t *testing.T) {
	lockPath := filepath.Join(t.TempDir(), "speech.lock")
	lock, err := speechlock.Acquire(context.Background(), lockPath, "test")
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	spec := helperSpec("hang")
	spec.Budget = 100 * time.Millisecond
	spec.Lock = lock
	proc := mustStart(t, spec)

	if holder, ok := speechlock.ReadHolder(lockPath); !ok || holder.PlayerPID != proc.PID() {
		t.Fatalf("recorded holder = %+v, %v; want player pid %d", holder, ok, proc.PID())
	}
	if err := proc.Wait(context.Background()); !errors.Is(err, ErrBudgetExceeded) {
		t.Fatalf("Wait = %v, want ErrBudgetExceeded", err)
	}
	lock.Release()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	next, err := speechlock.Acquire(ctx, lockPath, "next")
	if err != nil {
		t.Fatalf("speech lock still held after a budget stop: %v", err)
	}
	next.Release()
}

type unsignallableProc struct{ osProc }

func (unsignallableProc) terminate() error { return errors.New("termination failed") }
func (unsignallableProc) kill() error      { return errors.New("kill failed") }

func TestUnreapedPlayerKeepsSpeechLockAfterWaitReturns(t *testing.T) {
	path := filepath.Join(t.TempDir(), "speech.lock")
	lock, err := speechlock.Acquire(context.Background(), path, "unreaped")
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Release()
	spec := helperSpec("hang")
	spec.Lock = lock
	proc := mustStart(t, spec)
	child := proc.child
	proc.child = unsignallableProc{child}
	previousGrace := reapGrace
	reapGrace = 50 * time.Millisecond
	t.Cleanup(func() {
		reapGrace = previousGrace
		proc.child = child
		proc.abort()
	})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := proc.Wait(ctx); !errors.Is(err, context.Canceled) || !strings.Contains(err.Error(), "could not be killed") {
		t.Fatalf("Wait = %v, want cancelled with failed termination", err)
	}
	lock.Release()
	ctx, cancel = context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	next, err := speechlock.Acquire(ctx, path, "next")
	if next != nil {
		next.Release()
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("unreaped player lost the speech lock: %v", err)
	}
	if holder, known := speechlock.ReadHolder(path); !known || holder.PlayerPID != proc.PID() {
		t.Fatalf("surviving player record = %+v, %v", holder, known)
	}
	proc.child = child
	reapGrace = previousGrace
	if !proc.abort() {
		t.Fatal("player did not exit after restoring termination")
	}
	ctx, cancel = context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	next, err = speechlock.Acquire(ctx, path, "next")
	if err != nil {
		t.Fatal(err)
	}
	next.Release()
}

// A server killed mid-speech leaves its player running. The player inherited
// the speech lock, so the next speaker must wait for it to fall silent.
func TestOrphanedPlayerKeepsSpeechLock(t *testing.T) {
	dir := t.TempDir()
	lockPath := filepath.Join(dir, "speech.lock")
	logPath := filepath.Join(dir, "events.log")

	supervisor := exec.Command(os.Args[0], helperArg, "supervise-direct", lockPath, logPath, "700")
	stdout, err := supervisor.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := supervisor.Start(); err != nil {
		t.Fatalf("starting supervisor: %v", err)
	}
	t.Cleanup(func() {
		_ = supervisor.Process.Kill()
		_ = supervisor.Wait()
	})
	if line, err := bufio.NewReader(stdout).ReadString('\n'); err != nil || line != "started\n" {
		t.Fatalf("supervisor did not start its player: %q, %v", line, err)
	}
	holder, ok := speechlock.ReadHolder(lockPath)
	if !ok || holder.PlayerPID == 0 {
		t.Fatalf("recorded holder = %+v, %v; want the player's pid", holder, ok)
	}

	if err := supervisor.Process.Kill(); err != nil {
		t.Fatalf("killing supervisor: %v", err)
	}
	_ = supervisor.Wait()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	next, err := speechlock.Acquire(ctx, lockPath, "next")
	if err != nil {
		t.Fatalf("Acquire after the supervisor died: %v", err)
	}
	acquired := time.Now().UnixNano()
	next.Release()

	if _, ended := firstEvent(t, logPath, "end", holder.PlayerPID); !ended {
		t.Fatal("next speaker got the lock before the orphaned player finished")
	}
	assertNoSoundAfter(t, logPath, holder.PlayerPID, acquired)
}
