package speechlock

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

const helperEnv = "SPEECHLOCK_TEST_HELPER"

// TestMain lets the test binary act as a second process that holds the lock.
func TestMain(m *testing.M) {
	if os.Getenv(helperEnv) == "hold" {
		os.Exit(runHoldHelper(os.Args[len(os.Args)-2], os.Args[len(os.Args)-1]))
	}
	os.Exit(m.Run())
}

// runHoldHelper acquires the lock, announces it by creating readyPath, then
// holds the lock until the process is killed.
func runHoldHelper(lockPath, readyPath string) int {
	lock, err := Acquire(context.Background(), lockPath, "helper")
	if err != nil {
		return 2
	}
	defer lock.Release()
	if err := os.WriteFile(readyPath, nil, 0o600); err != nil {
		return 3
	}
	// Block until killed, or until the test process goes away and closes stdin.
	_, _ = io.Copy(io.Discard, os.Stdin)
	return 0
}

func startHolder(t *testing.T, lockPath string) *exec.Cmd {
	t.Helper()
	readyPath := filepath.Join(t.TempDir(), "ready")
	cmd := exec.Command(os.Args[0], lockPath, readyPath)
	cmd.Env = append(os.Environ(), helperEnv+"=hold")
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatalf("creating holder stdin: %v", err)
	}
	t.Cleanup(func() { stdin.Close() })
	if err := cmd.Start(); err != nil {
		t.Fatalf("starting holder process: %v", err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})
	deadline := time.Now().Add(10 * time.Second)
	for {
		if _, err := os.Stat(readyPath); err == nil {
			return cmd
		}
		if time.Now().After(deadline) {
			t.Fatal("holder process never acquired the lock")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func acquireWithin(t *testing.T, path string, d time.Duration) (*Lock, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), d)
	defer cancel()
	return Acquire(ctx, path, "test")
}

func TestAcquireExcludesAnotherProcess(t *testing.T) {
	path := filepath.Join(t.TempDir(), lockFileName)
	holder := startHolder(t, path)

	if _, err := acquireWithin(t, path, 300*time.Millisecond); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Acquire while another process holds the lock: err = %v, want deadline exceeded", err)
	}
	recorded, ok := ReadHolder(path)
	if !ok || recorded.PID != holder.Process.Pid || recorded.Label != "helper" {
		t.Fatalf("ReadHolder = %+v, %v; want pid %d label helper", recorded, ok, holder.Process.Pid)
	}

	if err := holder.Process.Kill(); err != nil {
		t.Fatalf("killing holder: %v", err)
	}
	_ = holder.Wait()

	// The kernel frees a dead holder's lock; Windows may do so with a delay.
	lock, err := acquireWithin(t, path, 10*time.Second)
	if err != nil {
		t.Fatalf("Acquire after the holder was killed: %v", err)
	}
	lock.Release()
}

func TestAcquireCancelledWhileWaitingLeavesLockFree(t *testing.T) {
	path := filepath.Join(t.TempDir(), lockFileName)
	first, err := acquireWithin(t, path, time.Second)
	if err != nil {
		t.Fatalf("first Acquire: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	waiter := make(chan error, 1)
	go func() {
		lock, err := Acquire(ctx, path, "waiter")
		lock.Release()
		waiter <- err
	}()
	time.Sleep(100 * time.Millisecond)
	cancel()
	if err := <-waiter; !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled Acquire: err = %v, want context.Canceled", err)
	}

	first.Release()
	// A waiter that kept running after cancellation would now own the lock.
	time.Sleep(150 * time.Millisecond)
	next, err := acquireWithin(t, path, time.Second)
	if err != nil {
		t.Fatalf("Acquire after a cancelled waiter: %v", err)
	}
	next.Release()
}

func TestAcquireWithCancelledContextDoesNotKeepLock(t *testing.T) {
	path := filepath.Join(t.TempDir(), lockFileName)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	lock, err := Acquire(ctx, path, "cancelled")
	if !errors.Is(err, context.Canceled) || lock != nil {
		t.Fatalf("Acquire with a cancelled context = %v, %v; want nil, context.Canceled", lock, err)
	}
	next, err := acquireWithin(t, path, time.Second)
	if err != nil {
		t.Fatalf("lock was left held by a cancelled Acquire: %v", err)
	}
	next.Release()
}

func TestAcquireExcludesGoroutinesInOneProcess(t *testing.T) {
	path := filepath.Join(t.TempDir(), lockFileName)
	var inside, overlaps atomic.Int32
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			lock, err := acquireWithin(t, path, 10*time.Second)
			if err != nil {
				t.Errorf("Acquire: %v", err)
				return
			}
			if inside.Add(1) > 1 {
				overlaps.Add(1)
			}
			time.Sleep(5 * time.Millisecond)
			inside.Add(-1)
			lock.Release()
		})
	}
	wg.Wait()
	if n := overlaps.Load(); n != 0 {
		t.Fatalf("%d goroutines held the lock at the same time", n)
	}
}

func TestNextHolderReplacesPreviousPlayerRecord(t *testing.T) {
	path := filepath.Join(t.TempDir(), lockFileName)
	lock, err := acquireWithin(t, path, time.Second)
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	lock.RecordPlayer(4242)
	if recorded, ok := ReadHolder(path); !ok || recorded.PlayerPID != 4242 || recorded.PID != os.Getpid() {
		t.Fatalf("ReadHolder while held = %+v, %v", recorded, ok)
	}
	lock.Release()
	next, err := Acquire(context.Background(), path, "next")
	if err != nil {
		t.Fatal(err)
	}
	defer next.Release()
	if recorded, ok := ReadHolder(path); !ok || recorded.Label != "next" || recorded.PlayerPID != 0 {
		t.Fatalf("ReadHolder for the next speaker = %+v, %v", recorded, ok)
	}
}

func TestNilLockIsANoOp(t *testing.T) {
	var lock *Lock
	if lock.File() != nil {
		t.Fatal("nil Lock returned a file")
	}
	lock.RecordPlayer(1)
	lock.Release()
}

func TestPathInCreatesPrivateDirectoryOnce(t *testing.T) {
	base := t.TempDir()
	first, err := pathIn(base)
	if err != nil {
		t.Fatalf("pathIn: %v", err)
	}
	second, err := pathIn(base)
	if err != nil || first != second {
		t.Fatalf("pathIn = %q then %q, %v", first, second, err)
	}
	if want := filepath.Join(base, "mcp-tts", lockFileName); first != want {
		t.Fatalf("pathIn = %q, want %q", first, want)
	}
	lock, err := acquireWithin(t, first, time.Second)
	if err != nil {
		t.Fatalf("Acquire in the created directory: %v", err)
	}
	lock.Release()
}
