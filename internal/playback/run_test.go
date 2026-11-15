package playback

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func mustStart(t *testing.T, spec Spec) *Proc {
	t.Helper()
	proc, err := Start(spec)
	if err != nil {
		t.Fatalf("Start(%s): %v", spec.Label, err)
	}
	t.Cleanup(func() { proc.abort() })
	return proc
}

func TestWaitReturnsNilWhenPlayerExitsCleanly(t *testing.T) {
	spec := helperSpec("sleep", "50")
	spec.Budget = 10 * time.Second
	if err := mustStart(t, spec).Wait(context.Background()); err != nil {
		t.Fatalf("Wait = %v, want nil", err)
	}
}

func TestWaitReportsFailureWithPlayerOutput(t *testing.T) {
	spec := helperSpec("fail", "7", "device unavailable")
	spec.Budget = 10 * time.Second
	err := mustStart(t, spec).Wait(context.Background())
	if err == nil || !strings.Contains(err.Error(), "device unavailable") || !strings.Contains(err.Error(), "7") {
		t.Fatalf("Wait = %v, want the exit status and the player's message", err)
	}
	if errors.Is(err, ErrBudgetExceeded) {
		t.Fatalf("Wait = %v, a plain failure must not look like a budget overrun", err)
	}
}

func TestBudgetStopsHungPlayer(t *testing.T) {
	spec := helperSpec("hang")
	spec.Budget = 150 * time.Millisecond
	proc := mustStart(t, spec)

	began := time.Now()
	err := proc.Wait(context.Background())
	if !errors.Is(err, ErrBudgetExceeded) {
		t.Fatalf("Wait = %v, want ErrBudgetExceeded", err)
	}
	if took := time.Since(began); took > spec.Budget+killGrace+2*time.Second {
		t.Fatalf("Wait took %s to stop a hung player", took)
	}
	if processAlive(proc.PID()) {
		t.Fatalf("player %d is still running after Wait returned", proc.PID())
	}
}

func TestStuckDiagnosticDoesNotDelayStop(t *testing.T) {
	var called atomic.Bool
	never := make(chan struct{})
	t.Cleanup(func() { close(never) })

	spec := helperSpec("hang")
	spec.Budget = 100 * time.Millisecond
	spec.Diagnose = func(context.Context, Incident) {
		called.Store(true)
		<-never // ignores its context, like a diagnostic tool that hangs
	}
	proc := mustStart(t, spec)

	began := time.Now()
	err := proc.Wait(context.Background())
	took := time.Since(began)
	if !errors.Is(err, ErrBudgetExceeded) {
		t.Fatalf("Wait = %v, want ErrBudgetExceeded", err)
	}
	if !called.Load() {
		t.Fatal("diagnostic was not invoked on budget expiry")
	}
	if earliest := spec.Budget + diagnosticWindow; took < earliest {
		t.Fatalf("player stopped after %s, before the diagnostic window ended (%s)", took, earliest)
	}
	if latest := spec.Budget + diagnosticWindow + killGrace + 2*time.Second; took > latest {
		t.Fatalf("stuck diagnostic delayed the stop: took %s, limit %s", took, latest)
	}
	if processAlive(proc.PID()) {
		t.Fatalf("player %d survived a stuck diagnostic", proc.PID())
	}
}

func TestDiagnosticReceivesIncidentDetails(t *testing.T) {
	got := make(chan Incident, 1)
	spec := helperSpec("hang")
	spec.Budget = 100 * time.Millisecond
	spec.Diagnose = func(_ context.Context, incident Incident) { got <- incident }
	proc := mustStart(t, spec)
	_ = proc.Wait(context.Background())

	incident := <-got
	if incident.PID != proc.PID() || incident.Label != "hang" || incident.Budget != spec.Budget ||
		incident.Elapsed < spec.Budget || incident.Player != spec.Path {
		t.Fatalf("incident = %+v", incident)
	}
}

func TestCancelStopsPlayerWithoutDiagnostic(t *testing.T) {
	var called atomic.Bool
	spec := helperSpec("hang")
	spec.Budget = time.Minute
	spec.Diagnose = func(context.Context, Incident) { called.Store(true) }
	proc := mustStart(t, spec)

	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(100*time.Millisecond, cancel)
	if err := proc.Wait(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("Wait = %v, want context.Canceled", err)
	}
	if processAlive(proc.PID()) {
		t.Fatalf("player %d is still running after cancellation", proc.PID())
	}
	if called.Load() {
		t.Fatal("diagnostic ran for a cancellation; it is for budget overruns only")
	}
}

func TestStartSurfacesFailureInsideStartupWindow(t *testing.T) {
	spec := helperSpec("fail", "2", "cannot open file")
	spec.StartupWindow = 5 * time.Second
	proc, err := Start(spec)
	if err == nil {
		proc.abort()
		t.Fatal("Start = nil error, want the early failure")
	}
	if !strings.Contains(err.Error(), "cannot open file") {
		t.Fatalf("Start = %v, want the player's message", err)
	}
}

func TestStartAcceptsPlayerThatFinishesInsideStartupWindow(t *testing.T) {
	spec := helperSpec("sleep", "10")
	spec.StartupWindow = 5 * time.Second
	if err := mustStart(t, spec).Wait(context.Background()); err != nil {
		t.Fatalf("Wait = %v, want nil for a short clip that already finished", err)
	}
}

func TestStartLeavesLaterFailureToWait(t *testing.T) {
	spec := helperSpec("late-fail", "400", "5")
	spec.StartupWindow = 50 * time.Millisecond
	spec.Budget = 10 * time.Second
	proc, err := Start(spec)
	if err != nil {
		t.Fatalf("Start = %v, want nil: the failure comes after the startup window", err)
	}
	t.Cleanup(func() { proc.abort() })
	if err := proc.Wait(context.Background()); err == nil {
		t.Fatal("Wait = nil, want the later failure")
	}
}

func TestStartReportsMissingPlayer(t *testing.T) {
	if proc, err := Start(Spec{Label: "missing", Path: "/nonexistent/mcp-tts-player"}); err == nil {
		proc.abort()
		t.Fatal("Start of a missing player = nil error")
	}
}

func TestBoundedBufferKeepsOnlyItsLimit(t *testing.T) {
	buf := &boundedBuffer{limit: 5}
	for _, chunk := range []string{"abc", "defg", "hij"} {
		if n, err := buf.Write([]byte(chunk)); n != len(chunk) || err != nil {
			t.Fatalf("Write(%q) = %d, %v; writers must never see a short write", chunk, n, err)
		}
	}
	if got := buf.String(); got != "abcde" {
		t.Fatalf("buffer = %q, want %q", got, "abcde")
	}
}
