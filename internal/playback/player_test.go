package playback

import (
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

type playerFixture struct {
	lockPath string
	logPath  string
}

func newPlayerFixture(t *testing.T) playerFixture {
	t.Helper()
	dir := t.TempDir()
	return playerFixture{lockPath: filepath.Join(dir, "speech.lock"), logPath: filepath.Join(dir, "events.log")}
}

func (f playerFixture) spec(admission time.Duration, prepare string, play time.Duration) PlayerSpec {
	return PlayerSpec{
		Label:        "fake-player",
		Exe:          os.Args[0],
		Args:         playerArgs(f.lockPath, admission, prepare, play, f.logPath),
		Admission:    admission + 5*time.Second,
		ReadyTimeout: 5 * time.Second,
		Budget:       10 * time.Second,
	}
}

func TestStartPlayerCompletesHandshakeAndPlays(t *testing.T) {
	fixture := newPlayerFixture(t)
	proc, err := StartPlayer(context.Background(), fixture.spec(5*time.Second, "ok", 100*time.Millisecond))
	if err != nil {
		t.Fatalf("StartPlayer: %v", err)
	}
	if holder, ok := speechlock.ReadHolder(fixture.lockPath); !ok || holder.PID != proc.PID() {
		t.Fatalf("recorded holder = %+v, %v; the player itself (pid %d) must hold the lock", holder, ok, proc.PID())
	}
	if err := proc.Wait(context.Background()); err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if _, ended := firstEvent(t, fixture.logPath, "end", proc.PID()); !ended {
		t.Fatal("player did not play to the end")
	}
}

func TestStartPlayerReportsBusyWithRecordedHolder(t *testing.T) {
	fixture := newPlayerFixture(t)
	held, err := speechlock.Acquire(context.Background(), fixture.lockPath, "other-speaker")
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	defer held.Release()

	_, err = StartPlayer(context.Background(), fixture.spec(200*time.Millisecond, "ok", 100*time.Millisecond))
	var busy *BusyError
	if !errors.As(err, &busy) {
		t.Fatalf("StartPlayer = %v, want *BusyError", err)
	}
	if !busy.Known || busy.Holder.PID != os.Getpid() || busy.Holder.Label != "other-speaker" {
		t.Fatalf("busy holder = %+v (known %v), want this process as other-speaker", busy.Holder, busy.Known)
	}
	if events := readEvents(t, fixture.logPath); len(events) != 0 {
		t.Fatalf("player made sound while the lock was busy: %+v", events)
	}
}

func TestStartPlayerKillsPlayerThatNeverBecomesReady(t *testing.T) {
	fixture := newPlayerFixture(t)
	spec := fixture.spec(5*time.Second, "hang", 100*time.Millisecond)
	spec.ReadyTimeout = 200 * time.Millisecond

	if _, err := StartPlayer(context.Background(), spec); !errors.Is(err, ErrPlayerNotReady) {
		t.Fatalf("StartPlayer = %v, want ErrPlayerNotReady", err)
	}
	// The stalled player held the lock; killing it must free the lock.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	lock, err := speechlock.Acquire(ctx, fixture.lockPath, "next")
	if err != nil {
		t.Fatalf("speech lock still held by a player that never became ready: %v", err)
	}
	lock.Release()
}

func TestStartPlayerReportsPrepareFailure(t *testing.T) {
	fixture := newPlayerFixture(t)
	_, err := StartPlayer(context.Background(), fixture.spec(5*time.Second, "fail", 100*time.Millisecond))
	if err == nil || !strings.Contains(err.Error(), "no audio device") {
		t.Fatalf("StartPlayer = %v, want the player's preparation error", err)
	}
	var busy *BusyError
	if errors.As(err, &busy) || errors.Is(err, ErrPlayerNotReady) {
		t.Fatalf("StartPlayer = %v, a preparation failure must not read as busy or slow", err)
	}
}

func TestStartPlayerCancelledDuringAdmission(t *testing.T) {
	fixture := newPlayerFixture(t)
	held, err := speechlock.Acquire(context.Background(), fixture.lockPath, "other-speaker")
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(150*time.Millisecond, cancel)
	if _, err := StartPlayer(ctx, fixture.spec(30*time.Second, "ok", 100*time.Millisecond)); !errors.Is(err, context.Canceled) {
		t.Fatalf("StartPlayer = %v, want context.Canceled", err)
	}

	// A player left waiting would take the lock as soon as it is free and speak.
	held.Release()
	time.Sleep(300 * time.Millisecond)
	if events := readEvents(t, fixture.logPath); len(events) != 0 {
		t.Fatalf("cancelled player still spoke: %+v", events)
	}
}

func TestRunPlayerWithoutLockPathDoesNotWait(t *testing.T) {
	fixture := newPlayerFixture(t)
	held, err := speechlock.Acquire(context.Background(), fixture.lockPath, "other-speaker")
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	defer held.Release()

	spec := fixture.spec(5*time.Second, "ok", 50*time.Millisecond)
	spec.Args = playerArgs("", 5*time.Second, "ok", 50*time.Millisecond, fixture.logPath)
	proc, err := StartPlayer(context.Background(), spec)
	if err != nil {
		t.Fatalf("StartPlayer without a lock path: %v", err)
	}
	if err := proc.Wait(context.Background()); err != nil {
		t.Fatalf("Wait: %v", err)
	}
}

// The hidden player owns the speech lock, so whatever happens to the server
// that started it, the next speaker cannot start while it still makes sound.
func TestPlayerHoldsLockAcrossSupervisorDeath(t *testing.T) {
	for _, stage := range []string{"spawn", lineAdmitted, lineReady} {
		t.Run("supervisor exits at "+stage, func(t *testing.T) {
			fixture := newPlayerFixture(t)
			first := playerArgs(fixture.lockPath, 5*time.Second, "slow:300", 700*time.Millisecond, fixture.logPath)
			supervisor := exec.Command(os.Args[0], append([]string{helperArg, "supervise-player", stage}, first...)...)
			out, err := supervisor.Output()
			if err != nil {
				t.Fatalf("supervisor: %v", err)
			}
			firstPID, err := strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(strings.SplitN(string(out), "\n", 2)[0], "pid ")))
			if err != nil {
				t.Fatalf("supervisor output %q has no player pid", out)
			}

			second, err := StartPlayer(context.Background(), fixture.spec(10*time.Second, "ok", 100*time.Millisecond))
			if err != nil {
				t.Fatalf("second player: %v", err)
			}
			if err := second.Wait(context.Background()); err != nil {
				t.Fatalf("second player Wait: %v", err)
			}
			started, ok := firstEvent(t, fixture.logPath, "start", second.PID())
			if !ok {
				t.Fatal("second player never played")
			}
			assertNoSoundAfter(t, fixture.logPath, firstPID, started.at)
			waitGone(t, firstPID, 10*time.Second)
		})
	}
}
