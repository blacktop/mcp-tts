package playback

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/blacktop/mcp-tts/internal/speechlock"
)

// helperArg marks an invocation of the test binary as a stand-in process
// (player, supervisor, sampler) rather than a test run. It is an argument, not
// an environment variable, so helpers that start other helpers do not pass
// their own role down.
const helperArg = "playback-test-helper"

const tickInterval = 20 * time.Millisecond

func TestMain(m *testing.M) {
	if len(os.Args) > 2 && os.Args[1] == helperArg {
		os.Exit(runHelper(os.Args[2], os.Args[3:]))
	}
	diagnosticWindow = 300 * time.Millisecond
	killGrace = 300 * time.Millisecond
	os.Exit(m.Run())
}

func helperSpec(mode string, args ...string) Spec {
	return Spec{Label: mode, Path: os.Args[0], Args: append([]string{helperArg, mode}, args...)}
}

func runHelper(mode string, args []string) int {
	switch mode {
	case "sleep":
		time.Sleep(millis(args[0]))
		return 0
	case "fail":
		fmt.Fprintln(os.Stderr, args[1])
		return atoi(args[0])
	case "late-fail":
		time.Sleep(millis(args[0]))
		return atoi(args[1])
	case "hang":
		time.Sleep(time.Hour)
		return 0
	case "ignore-term":
		signal.Ignore(syscall.SIGTERM)
		time.Sleep(time.Hour)
		return 0
	case "parent-of":
		return helpParentOf(args[0])
	case "play":
		playAudibly(args[0], millis(args[1]))
		return 0
	case "write":
		if err := os.WriteFile(args[0], make([]byte, atoi(args[1])), 0o644); err != nil {
			return 1
		}
		return 0
	case "player":
		return helpPlayer(args)
	case "supervise-direct":
		return helpSuperviseDirect(args[0], args[1], millis(args[2]))
	case "supervise-player":
		return helpSupervisePlayer(args[0], args[1:])
	}
	fmt.Fprintf(os.Stderr, "unknown helper mode %q\n", mode)
	return 64
}

func millis(s string) time.Duration { return time.Duration(atoi(s)) * time.Millisecond }

func atoi(s string) int {
	n, err := strconv.Atoi(s)
	if err != nil {
		panic(err)
	}
	return n
}

// logEvent appends "<event> <pid> <unix nanoseconds>" to the shared log.
func logEvent(logPath, event string) {
	f, err := os.OpenFile(logPath, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o600)
	if err != nil {
		panic(err)
	}
	defer f.Close()
	fmt.Fprintf(f, "%s %d %d\n", event, os.Getpid(), time.Now().UnixNano())
}

// playAudibly stands in for audio output: it logs a tick for as long as it
// "plays", so a test can tell exactly when a killed player fell silent.
func playAudibly(logPath string, d time.Duration) {
	logEvent(logPath, "start")
	end := time.Now().Add(d)
	for time.Now().Before(end) {
		logEvent(logPath, "tick")
		time.Sleep(tickInterval)
	}
	logEvent(logPath, "end")
}

// helpParentOf starts a child in its own process group's shadow and hangs, so
// a test can check that stopping a player reaches the player's children.
func helpParentOf(pidPath string) int {
	child := exec.Command(os.Args[0], helperArg, "hang")
	if err := child.Start(); err != nil {
		return 1
	}
	if err := os.WriteFile(pidPath, []byte(strconv.Itoa(child.Process.Pid)), 0o600); err != nil {
		return 1
	}
	time.Sleep(time.Hour)
	return 0
}

type fakeBackend struct {
	prepare string
	play    time.Duration
	logPath string
}

func (b fakeBackend) Prepare() error {
	switch {
	case b.prepare == "fail":
		return errors.New("no audio device")
	case b.prepare == "hang":
		time.Sleep(time.Hour)
	case strings.HasPrefix(b.prepare, "slow:"):
		time.Sleep(millis(strings.TrimPrefix(b.prepare, "slow:")))
	}
	return nil
}

func (b fakeBackend) Play() error {
	playAudibly(b.logPath, b.play)
	return nil
}

// playerArgs builds the arguments of the hidden-player stand-in.
func playerArgs(lockPath string, admission time.Duration, prepare string, play time.Duration, logPath string) []string {
	return []string{
		helperArg, "player", lockPath, strconv.FormatInt(admission.Milliseconds(), 10),
		prepare, strconv.FormatInt(play.Milliseconds(), 10), logPath,
	}
}

func helpPlayer(args []string) int {
	backend := fakeBackend{prepare: args[2], play: millis(args[3]), logPath: args[4]}
	err := RunPlayer(context.Background(), PlayerOptions{
		LockPath: args[0], Label: "fake-player", Admission: millis(args[1]), Out: os.Stdout,
	}, backend)
	var busy *BusyError
	switch {
	case err == nil:
		return 0
	case errors.As(err, &busy):
		return 3
	default:
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
}

// helpSuperviseDirect behaves like a server using the direct-child path: it
// takes the speech lock, starts a player that inherits it, and waits.
func helpSuperviseDirect(lockPath, logPath string, play time.Duration) int {
	lock, err := speechlock.Acquire(context.Background(), lockPath, "supervisor")
	if err != nil {
		return 2
	}
	spec := helperSpec("play", logPath, strconv.FormatInt(play.Milliseconds(), 10))
	spec.Lock = lock
	proc, err := Start(spec)
	if err != nil {
		return 3
	}
	fmt.Println("started")
	_ = proc.Wait(context.Background())
	lock.Release()
	return 0
}

// helpSupervisePlayer behaves like a server using the hidden player, and exits
// at the requested stage of the player's startup so a test can observe what
// the player does once its supervisor is gone.
func helpSupervisePlayer(stage string, playerArgv []string) int {
	proc, stdout, err := start(Spec{Label: "player", Path: os.Args[0], Args: playerArgv}, true)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	fmt.Printf("pid %d\n", proc.PID())
	if stage == "spawn" {
		return 0
	}
	lines := bufio.NewScanner(stdout)
	for lines.Scan() {
		if lines.Text() == stage {
			return 0
		}
	}
	return 4
}

// events reads the shared log as (event, pid, time) records.
type event struct {
	name string
	pid  int
	at   int64
}

func readEvents(t *testing.T, logPath string) []event {
	t.Helper()
	data, err := os.ReadFile(logPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		t.Fatalf("reading event log: %v", err)
	}
	var events []event
	for line := range strings.Lines(string(data)) {
		fields := strings.Fields(line)
		if len(fields) != 3 {
			t.Fatalf("malformed event line %q", line)
		}
		at, err := strconv.ParseInt(fields[2], 10, 64)
		if err != nil {
			t.Fatalf("malformed event time in %q", line)
		}
		events = append(events, event{name: fields[0], pid: atoi(fields[1]), at: at})
	}
	return events
}

// assertNoSoundAfter fails if pid logged any activity at or after cutoff.
func assertNoSoundAfter(t *testing.T, logPath string, pid int, cutoff int64) {
	t.Helper()
	for _, e := range readEvents(t, logPath) {
		if e.pid == pid && e.at >= cutoff {
			t.Fatalf("player %d was still active (%s) %s after the next speaker started",
				pid, e.name, time.Duration(e.at-cutoff))
		}
	}
}

func firstEvent(t *testing.T, logPath, name string, pid int) (event, bool) {
	t.Helper()
	for _, e := range readEvents(t, logPath) {
		if e.name == name && e.pid == pid {
			return e, true
		}
	}
	return event{}, false
}

// waitGone polls until pid is no longer running.
func waitGone(t *testing.T, pid int, within time.Duration) {
	t.Helper()
	deadline := time.Now().Add(within)
	for processAlive(pid) {
		if time.Now().After(deadline) {
			t.Fatalf("process %d is still running after %s", pid, within)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
