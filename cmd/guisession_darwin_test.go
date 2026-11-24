//go:build darwin

package cmd

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestParseGUIExitCode(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		wantCode int
		wantDone bool
	}{
		{
			name: "running job",
			input: `com.blacktop.mcp-tts.123.1 = {
	active count = 1
	state = running
	program = /usr/bin/say
}`,
			wantCode: 0,
			wantDone: false,
		},
		{
			name: "exited success",
			input: `com.blacktop.mcp-tts.123.1 = {
	state = not running
	last exit code = 0
}`,
			wantCode: 0,
			wantDone: true,
		},
		{
			name: "exited failure",
			input: `com.blacktop.mcp-tts.123.1 = {
	state = not running
	last exit code = 7
}`,
			wantCode: 7,
			wantDone: true,
		},
		{
			name:     "not found",
			input:    `Could not find service "com.blacktop.mcp-tts.123.1" in domain for gui`,
			wantCode: 0,
			wantDone: false,
		},
		{
			name: "exit line but still running (race)",
			input: `com.blacktop.mcp-tts.123.1 = {
	state = running
	last exit code = 0
}`,
			wantCode: 0,
			wantDone: false,
		},
		{
			name:     "spaced indentation variant",
			input:    "  state = not running\n  last exit code = 3\n",
			wantCode: 3,
			wantDone: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			code, done := parseGUIExitCode(tt.input)
			if code != tt.wantCode || done != tt.wantDone {
				t.Fatalf("parseGUIExitCode() = (%d, %v), want (%d, %v)",
					code, done, tt.wantCode, tt.wantDone)
			}
		})
	}
}

func TestWritePlist(t *testing.T) {
	var buf bytes.Buffer
	spec := guiJobSpec{
		Label: "com.blacktop.mcp-tts.999.1",
		ProgramArguments: []string{
			"/usr/bin/say", "--rate", "200", "--voice", "Samantha", "-f", "/tmp/x.txt",
		},
		StandardErrorPath: "/tmp/job.err",
	}
	if err := writePlist(&buf, spec); err != nil {
		t.Fatalf("writePlist() error = %v", err)
	}
	out := buf.String()

	wantSubstrings := []string{
		`<?xml version="1.0" encoding="UTF-8"?>`,
		`<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">`,
		`<key>Label</key><string>com.blacktop.mcp-tts.999.1</string>`,
		`<key>RunAtLoad</key><true/>`,
		`<key>AbandonProcessGroup</key><true/>`,
		`<key>ProcessType</key><string>Interactive</string>`,
		`<string>/usr/bin/say</string>`,
		`<string>--rate</string>`,
		`<string>Samantha</string>`,
		`<string>/tmp/x.txt</string>`,
		`<key>StandardErrorPath</key><string>/tmp/job.err</string>`,
	}
	for _, want := range wantSubstrings {
		if !strings.Contains(out, want) {
			t.Errorf("writePlist() output missing %q\n--- output ---\n%s", want, out)
		}
	}
}

func TestWritePlistEscapesValues(t *testing.T) {
	var buf bytes.Buffer
	spec := guiJobSpec{
		Label:             "com.blacktop.mcp-tts.1.1",
		ProgramArguments:  []string{"/usr/bin/say", "--voice", "a&b<c>d"},
		StandardErrorPath: "/tmp/job.err",
	}
	if err := writePlist(&buf, spec); err != nil {
		t.Fatalf("writePlist() error = %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "a&amp;b&lt;c&gt;d") {
		t.Errorf("writePlist() did not escape special chars\n--- output ---\n%s", out)
	}
	if strings.Contains(out, "a&b<c>d") {
		t.Errorf("writePlist() leaked unescaped special chars\n--- output ---\n%s", out)
	}
}

func TestWritePlistPassesPlutilLint(t *testing.T) {
	if _, err := exec.LookPath("plutil"); err != nil {
		t.Skip("plutil not available")
	}
	var buf bytes.Buffer
	spec := guiJobSpec{
		Label:             "com.blacktop.mcp-tts.1.1",
		ProgramArguments:  []string{"/usr/bin/say", "--rate", "200", "-f", "/tmp/x.txt"},
		StandardErrorPath: "/tmp/job.err",
	}
	if err := writePlist(&buf, spec); err != nil {
		t.Fatalf("writePlist() error = %v", err)
	}
	cmd := exec.Command("plutil", "-lint", "-")
	cmd.Stdin = bytes.NewReader(buf.Bytes())
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("plutil -lint rejected plist: %v\n%s\n--- plist ---\n%s",
			err, out, buf.String())
	}
}

func TestNextGUILabelUnique(t *testing.T) {
	const n = 100
	seen := make(map[string]struct{}, n)
	pidSegment := fmt.Sprintf(".%d.", os.Getpid())
	for range n {
		label := nextGUILabel()
		if !strings.HasPrefix(label, guiLabelPrefix+".") {
			t.Fatalf("label %q missing prefix %q", label, guiLabelPrefix)
		}
		if !strings.Contains(label, pidSegment) {
			t.Fatalf("label %q missing pid segment %q", label, pidSegment)
		}
		if _, dup := seen[label]; dup {
			t.Fatalf("duplicate label %q", label)
		}
		seen[label] = struct{}{}
	}
}

func TestParseManagerName(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{"Aqua\n", "Aqua"},
		{"Background\n", "Background"},
		{"  LoginWindow  \n", "LoginWindow"},
		{"", ""},
		{"   \n", ""},
	}
	for _, tt := range tests {
		if got := parseManagerName(tt.in); got != tt.want {
			t.Errorf("parseManagerName(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestGUILabelPID(t *testing.T) {
	tests := []struct {
		label   string
		wantPID int
		wantOK  bool
	}{
		{"com.blacktop.mcp-tts.4321.7", 4321, true},
		{"com.blacktop.mcp-tts.notapid.7", 0, false},
		{"com.blacktop.mcp-tts", 0, false},
		{"some.other.label", 0, false},
	}
	for _, tt := range tests {
		pid, ok := guiLabelPID(tt.label)
		if pid != tt.wantPID || ok != tt.wantOK {
			t.Errorf("guiLabelPID(%q) = (%d, %v), want (%d, %v)",
				tt.label, pid, ok, tt.wantPID, tt.wantOK)
		}
	}
}

func TestStaleGUILabelsSkipsLivePID(t *testing.T) {
	live := fmt.Sprintf("com.blacktop.mcp-tts.%d.1", os.Getpid())
	dead := "com.blacktop.mcp-tts.999999.1"
	input := fmt.Sprintf("\t%s = {\n\t}\n\t%s = {\n\t}\n", live, dead)

	stale := staleGUILabels(input)
	if _, ok := stale[live]; ok {
		t.Errorf("staleGUILabels marked live-pid label %q as stale", live)
	}
	if _, ok := stale[dead]; !ok {
		t.Errorf("staleGUILabels did not mark dead-pid label %q as stale", dead)
	}
}

// fakeSessionMode pins sessionModeFn for the duration of the test.
func fakeSessionMode(t *testing.T, mode sessionMode, name string) {
	t.Helper()
	prev := sessionModeFn
	sessionModeFn = func() (sessionMode, string) { return mode, name }
	t.Cleanup(func() { sessionModeFn = prev })
}

// fakeGUIJobs replaces the launchctl-touching seams so routing can be exercised
// without a real GUI session. bootstrapErr (if non-nil) fails the synchronous
// start; otherwise started records each program that was bootstrapped and the
// detached wait returns waitErr.
func fakeGUIJobs(t *testing.T, bootstrapErr, waitErr error, started *[]string) {
	t.Helper()
	prevStart, prevWait := startGUIJobFn, waitGUIJobFn
	startGUIJobFn = func(_ context.Context, program string, _ []string) (guiJobHandle, error) {
		if bootstrapErr != nil {
			return guiJobHandle{}, bootstrapErr
		}
		if started != nil {
			*started = append(*started, program)
		}
		return guiJobHandle{label: "fake", program: program}, nil
	}
	waitGUIJobFn = func(context.Context, guiJobHandle) error { return waitErr }
	t.Cleanup(func() { startGUIJobFn, waitGUIJobFn = prevStart, prevWait })
}

// withTestServerCtx points serverCtx at a fresh cancellable context and disables
// sequential speech so detached playback in tests does not touch the user's
// speech lock. It drains playbackWG and restores prior state on cleanup.
func withTestServerCtx(t *testing.T) {
	t.Helper()
	prevCtx, prevSeq := serverCtx, sequentialTTS
	ctx, cancel := context.WithCancel(context.Background())
	serverCtx = ctx
	sequentialTTS = false
	t.Cleanup(func() {
		cancel()
		playbackWG.Wait()
		serverCtx, sequentialTTS = prevCtx, prevSeq
	})
}

func TestRouteCloudPlaybackAqua(t *testing.T) {
	fakeSessionMode(t, sessionAqua, "Aqua")
	handled, err := routeCloudPlayback(context.Background(), "openai_tts", "mp3", func() ([]byte, error) { return []byte("data"), nil })
	if handled {
		t.Fatalf("routeCloudPlayback(Aqua) handled = true, want false (direct-child fallback)")
	}
	if err != nil {
		t.Fatalf("routeCloudPlayback(Aqua) err = %v, want nil", err)
	}
}

func TestRouteCloudPlaybackHeadless(t *testing.T) {
	fakeSessionMode(t, sessionHeadless, "Background")
	handled, err := routeCloudPlayback(context.Background(), "openai_tts", "mp3", func() ([]byte, error) { return []byte("data"), nil })
	if !handled {
		t.Fatalf("routeCloudPlayback(headless) handled = false, want true")
	}
	if err == nil {
		t.Fatalf("routeCloudPlayback(headless) err = nil, want headless error")
	}
	if !strings.Contains(err.Error(), "macOS audio is unavailable") {
		t.Fatalf("routeCloudPlayback(headless) err = %q, missing headless template", err)
	}
}

func TestRouteCloudPlaybackRoutableBootstrapErrorSurfaces(t *testing.T) {
	fakeSessionMode(t, sessionRoutable, "Background")
	withTestServerCtx(t)
	bootErr := errors.New("bootstrap boom")
	fakeGUIJobs(t, bootErr, nil, nil)

	handled, err := routeCloudPlayback(context.Background(), "openai_tts", "mp3", func() ([]byte, error) { return []byte("data"), nil })
	if !handled {
		t.Fatalf("routeCloudPlayback(routable) handled = false, want true")
	}
	if err == nil || !strings.Contains(err.Error(), "bootstrap boom") {
		t.Fatalf("routeCloudPlayback(routable) err = %v, want surfaced bootstrap error", err)
	}
}

func TestRouteCloudPlaybackRoutableSuccessDetaches(t *testing.T) {
	fakeSessionMode(t, sessionRoutable, "Background")
	withTestServerCtx(t)
	var started []string
	fakeGUIJobs(t, nil, nil, &started)
	start := startGUIJobFn
	var args []string
	startGUIJobFn = func(ctx context.Context, program string, a []string) (guiJobHandle, error) {
		args = a
		return start(ctx, program, a)
	}

	handled, err := routeCloudPlayback(context.Background(), "openai_tts", "mp3", func() ([]byte, error) { return []byte("data"), nil })
	if !handled || err != nil {
		t.Fatalf("routeCloudPlayback(routable) = (%v, %v), want (true, nil)", handled, err)
	}
	playbackWG.Wait()
	if len(started) != 1 || started[0] != afplayBinPath {
		t.Fatalf("routeCloudPlayback(routable) started = %v, want one afplay job", started)
	}
	if len(args) != 3 || !slices.Equal(args[:2], []string{"-v", "0.5"}) {
		t.Fatalf("afplay args = %q, want the configured volume before the file", args)
	}
}

func TestHeadlessAudioError(t *testing.T) {
	msg := headlessAudioError("Background")
	wantSubstrings := []string{
		"Background",
		guiDomainTarget(),
		"MCP_TTS_OUTPUT_DIR",
		"MCP_TTS_NO_PLAY",
	}
	for _, want := range wantSubstrings {
		if !strings.Contains(msg, want) {
			t.Errorf("headlessAudioError() missing %q\n--- message ---\n%s", want, msg)
		}
	}
}

func TestStartRoutableSayPlaybackBootstrapErrorSurfaces(t *testing.T) {
	withTestServerCtx(t)
	fakeGUIJobs(t, errors.New("bootstrap boom"), nil, nil)

	err := startRoutableSayPlayback(context.Background(), []string{"--rate", "200"}, "hello", "")
	if err == nil || !strings.Contains(err.Error(), "bootstrap boom") {
		t.Fatalf("startRoutableSayPlayback() err = %v, want surfaced bootstrap error", err)
	}
}

func TestStartRoutableSayPlaybackSuccessDetaches(t *testing.T) {
	withTestServerCtx(t)
	var started []string
	fakeGUIJobs(t, nil, nil, &started)

	if err := startRoutableSayPlayback(context.Background(), []string{"--rate", "200"}, "hello", ""); err != nil {
		t.Fatalf("startRoutableSayPlayback() err = %v, want nil", err)
	}
	playbackWG.Wait()
	if len(started) != 1 || started[0] != sayBinPath {
		t.Fatalf("startRoutableSayPlayback() started = %v, want one say job", started)
	}
}

func TestGUISayPlayBootstrapErrorSurfaces(t *testing.T) {
	fakeGUIJobs(t, errors.New("bootstrap boom"), nil, nil)
	err := guiSayPlay(context.Background(), []string{"--rate", "200"}, "hi", "", true)
	if err == nil || !strings.Contains(err.Error(), "bootstrap boom") {
		t.Fatalf("guiSayPlay() err = %v, want surfaced bootstrap error", err)
	}
}

func TestGUISayPlaySaveThenAfplaySequence(t *testing.T) {
	var started []string
	fakeGUIJobs(t, nil, nil, &started)
	err := guiSayPlay(context.Background(), []string{"--rate", "200"}, "hi", "/tmp/out.aiff", true)
	if err != nil {
		t.Fatalf("guiSayPlay() err = %v, want nil", err)
	}
	want := []string{sayBinPath, afplayBinPath}
	if len(started) != len(want) || started[0] != want[0] || started[1] != want[1] {
		t.Fatalf("guiSayPlay() started = %v, want %v (say then afplay)", started, want)
	}
}

func TestGUISaySetsVolumeOnlyWhenSayPlays(t *testing.T) {
	for _, tc := range []struct {
		name       string
		savedPath  string
		play       bool
		wantText   string
		wantAfplay []string
	}{
		{name: "say plays", play: true, wantText: "[[volm 0.5]] hi"},
		{name: "afplay plays the saved file", savedPath: "/tmp/out.aiff", play: true, wantText: "hi",
			wantAfplay: []string{"-v", "0.5", "/tmp/out.aiff"}},
		{name: "save only", savedPath: "/tmp/out.aiff", wantText: "hi"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fakeGUIJobs(t, nil, nil, nil)
			start := startGUIJobFn
			var text string
			var afplay []string
			startGUIJobFn = func(ctx context.Context, program string, args []string) (guiJobHandle, error) {
				switch program {
				case sayBinPath:
					data, err := os.ReadFile(args[len(args)-1])
					if err != nil {
						t.Fatalf("reading the say text file: %v", err)
					}
					text = string(data)
				case afplayBinPath:
					afplay = args
				}
				return start(ctx, program, args)
			}

			if err := guiSayPlay(context.Background(), nil, "hi", tc.savedPath, tc.play); err != nil {
				t.Fatalf("guiSayPlay() err = %v, want nil", err)
			}
			if text != tc.wantText {
				t.Errorf("say text = %q, want %q", text, tc.wantText)
			}
			if !slices.Equal(afplay, tc.wantAfplay) {
				t.Errorf("afplay args = %q, want %q", afplay, tc.wantAfplay)
			}
		})
	}
}

func TestRexGUISayStatus(t *testing.T) {
	for _, tc := range []struct {
		name      string
		savedPath string
		play      bool
		startErr  error
	}{
		{name: "save-only", savedPath: "/tmp/out.aiff"},
		{name: "save-and-play", savedPath: "/tmp/out.aiff", play: true},
		{name: "launch-failure", play: true, startErr: errors.New("bootstrap failed")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var out bytes.Buffer
			ctx := rexTestContext(&out)
			fakeGUIJobs(t, tc.startErr, nil, nil)
			start := startGUIJobFn
			startGUIJobFn = func(ctx context.Context, program string, args []string) (guiJobHandle, error) {
				if tc.savedPath != "" && program == sayBinPath && out.Len() != 0 {
					t.Error("synthesizing a saved file must not show a speaking indicator")
				}
				return start(ctx, program, args)
			}
			err := guiSayPlay(ctx, nil, "hello", tc.savedPath, tc.play)
			if !errors.Is(err, tc.startErr) {
				t.Fatalf("guiSayPlay() error = %v, want %v", err, tc.startErr)
			}
			reports := rexTestReports(t, out.String())
			if !tc.play {
				if len(reports) != 0 {
					t.Fatalf("save-only emitted status: %v", reports)
				}
				return
			}
			if len(reports) != 2 || reports[0]["state"] != "working" || reports[1]["state"] != "clear" || reports[0]["id"] != reports[1]["id"] {
				t.Fatalf("speech status did not clear: %v", reports)
			}
		})
	}
}

func TestGUIRoutedSayProducesRealAudio(t *testing.T) {
	if os.Getenv("GITHUB_ACTIONS") != "" {
		t.Skip("no GUI session in CI")
	}
	if mode, _ := currentSessionMode(); mode != sessionRoutable {
		t.Skip("not in a routable Background session")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	start := time.Now()
	err := guiSayPlay(ctx, []string{"--rate", "200"},
		"one two three four five six seven eight nine ten", "", true)
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("guiSayPlay() error = %v", err)
	}
	if elapsed < 2*time.Second {
		t.Fatalf("guiSayPlay() returned in %v; expected real synthesis+playback >2s "+
			"(a silent Background failure returns in <0.5s)", elapsed)
	}
}

func TestGUIRoutedSaveWritesRealAIFF(t *testing.T) {
	if os.Getenv("GITHUB_ACTIONS") != "" {
		t.Skip("no GUI session in CI")
	}
	if mode, _ := currentSessionMode(); mode != sessionRoutable {
		t.Skip("not in a routable Background session")
	}

	f, err := os.CreateTemp("", "mcp-tts-test-*.aiff")
	if err != nil {
		t.Fatalf("CreateTemp() error = %v", err)
	}
	savedPath := f.Name()
	f.Close()
	defer os.Remove(savedPath)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	err = guiSayPlay(ctx, []string{"--rate", "200"},
		"one two three four five six seven eight nine ten", savedPath, false)
	if err != nil {
		t.Fatalf("guiSayPlay() error = %v", err)
	}
	info, err := os.Stat(savedPath)
	if err != nil {
		t.Fatalf("Stat(%q) error = %v", savedPath, err)
	}
	if info.Size() < 50_000 {
		t.Fatalf("saved AIFF is %d bytes; expected >50000 for a real 10-word clip "+
			"(broken Background stub is ~a few hundred bytes)", info.Size())
	}
}

// fakeLaunchd stands in for launchctl. bootstrap always times out; print
// answers with the queued replies in order, repeating the last one.
type fakeLaunchd struct {
	mu     sync.Mutex
	calls  []string
	prints []func() ([]byte, error)
}

func (f *fakeLaunchd) run(_ context.Context, _ time.Duration, args ...string) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, args[0])
	switch args[0] {
	case "bootstrap":
		return nil, fmt.Errorf("launchctl bootstrap timed out: %w", context.DeadlineExceeded)
	case "print":
		reply := f.prints[0]
		if len(f.prints) > 1 {
			f.prints = f.prints[1:]
		}
		return reply()
	}
	return nil, nil
}

func (f *fakeLaunchd) count(subcommand string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, call := range f.calls {
		if call == subcommand {
			n++
		}
	}
	return n
}

func useFakeLaunchd(t *testing.T, prints ...func() ([]byte, error)) *fakeLaunchd {
	t.Helper()
	fake := &fakeLaunchd{prints: prints}
	original := runLaunchctl
	runLaunchctl = fake.run
	t.Cleanup(func() { runLaunchctl = original })
	return fake
}

func launchdNotFound() ([]byte, error) {
	return []byte("Bad request.\nCould not find service \"x\" in domain for user gui: 501\n"), errors.New("exit status 113")
}

func launchdStillPresent() ([]byte, error) {
	return []byte("com.blacktop.mcp-tts.1.1 = {\n\tstate = running\n}\n"), nil
}

func launchdUnreachable() ([]byte, error) {
	return nil, fmt.Errorf("launchctl print timed out: %w", context.DeadlineExceeded)
}

func TestStartGUIJobBootstrapTimeoutNeverClaimsSilence(t *testing.T) {
	for _, tc := range []struct {
		name      string
		print     func() ([]byte, error)
		wantState guiJobState
		wantText  string
	}{
		{"job removed", launchdNotFound, guiJobAbsent, "Playback outcome unknown; GUI job removed"},
		{"job still present", launchdStillPresent, guiJobPresent, "Playback outcome unknown; GUI job could not be removed, speech may still play"},
		{"probe failed", launchdUnreachable, guiJobUndetermined, "Playback outcome unknown; GUI job state could not be determined"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fake := useFakeLaunchd(t, tc.print)
			_, err := startGUIJob(context.Background(), afplayBinPath, []string{"/tmp/x.mp3"})

			var unknown *guiOutcomeUnknownError
			if !errors.As(err, &unknown) {
				t.Fatalf("startGUIJob() err = %v, want *guiOutcomeUnknownError", err)
			}
			if unknown.state != tc.wantState {
				t.Fatalf("state = %v, want %v", unknown.state, tc.wantState)
			}
			if !strings.Contains(err.Error(), tc.wantText) {
				t.Fatalf("err = %q, want it to contain %q", err, tc.wantText)
			}
			if lower := strings.ToLower(err.Error()); strings.Contains(lower, "not spoken") || strings.Contains(lower, "nothing was spoken") {
				t.Fatalf("err = %q claims nothing was spoken; the job may have run", err)
			}
			if !strings.Contains(err.Error(), "retrying may repeat speech") {
				t.Fatalf("err = %q does not warn that a retry can repeat speech", err)
			}
			if fake.count("kill") != 1 || fake.count("bootout") != 1 {
				t.Fatalf("launchctl calls = %v, want one kill and one bootout", fake.calls)
			}
			_, statErr := os.Stat(unknown.dir)
			if removed := os.IsNotExist(statErr); removed != (tc.wantState == guiJobAbsent) {
				t.Fatalf("scratch dir removed = %v for state %v", removed, tc.wantState)
			}
			os.RemoveAll(unknown.dir)
		})
	}
}

func TestStartGUIJobCancelledBootstrapIsNotAmbiguous(t *testing.T) {
	useFakeLaunchd(t, launchdNotFound)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := startGUIJob(ctx, afplayBinPath, []string{"/tmp/x.mp3"})
	var unknown *guiOutcomeUnknownError
	if err == nil || errors.As(err, &unknown) {
		t.Fatalf("startGUIJob() with a cancelled context = %v, want a plain bootstrap error", err)
	}
}

func TestPlayViaGUIReleasesLockWhenTimedOutJobIsGone(t *testing.T) {
	lockPath := useTestSpeechLock(t)
	useTestServer(t)
	useFakeLaunchd(t, launchdNotFound)

	err := playViaGUI(context.Background(), "openai_tts", "mp3", []byte("data"))
	if err == nil || !strings.Contains(err.Error(), "GUI job removed") {
		t.Fatalf("playViaGUI() err = %v, want the unknown-outcome error", err)
	}
	requireSpeechLockFree(t, lockPath)
}

func TestPlayViaGUIKeepsLockUntilTimedOutJobIsConfirmedGone(t *testing.T) {
	for _, tc := range []struct {
		name  string
		first func() ([]byte, error)
	}{
		{"job still present", launchdStillPresent},
		{"probe failed", launchdUnreachable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			lockPath := useTestSpeechLock(t)
			useTestServer(t)
			originalRetry := guiRemoveRetryInterval
			guiRemoveRetryInterval = 400 * time.Millisecond
			t.Cleanup(func() { guiRemoveRetryInterval = originalRetry })
			// The first probe cannot confirm removal; the retry can.
			fake := useFakeLaunchd(t, tc.first, launchdNotFound)

			err := playViaGUI(context.Background(), "openai_tts", "mp3", []byte("data"))
			if err == nil || !strings.Contains(err.Error(), "Playback outcome unknown") {
				t.Fatalf("playViaGUI() err = %v, want the unknown-outcome error", err)
			}
			if !speechLockHeld(lockPath) {
				t.Fatal("speech lock was released while the GUI job might still be speaking")
			}
			playbackWG.Wait()
			requireSpeechLockFree(t, lockPath)
			if fake.count("bootout") < 2 {
				t.Fatalf("launchctl calls = %v, want the removal retried", fake.calls)
			}
		})
	}
}

func TestSettleGUIStartFailureGivesUpAfterJobTimeout(t *testing.T) {
	lockPath := useTestSpeechLock(t)
	useTestServer(t)
	originalRetry, originalTimeout := guiRemoveRetryInterval, guiJobTimeout
	guiRemoveRetryInterval, guiJobTimeout = 100*time.Millisecond, 500*time.Millisecond
	t.Cleanup(func() { guiRemoveRetryInterval, guiJobTimeout = originalRetry, originalTimeout })
	useFakeLaunchd(t, launchdStillPresent)

	began := time.Now()
	err := playViaGUI(context.Background(), "openai_tts", "mp3", []byte("data"))
	if err == nil {
		t.Fatal("playViaGUI() = nil, want the unknown-outcome error")
	}
	playbackWG.Wait()
	if held := time.Since(began); held < guiJobTimeout || held > guiJobTimeout+5*time.Second {
		t.Fatalf("speech lock was kept for %s, want about %s", held, guiJobTimeout)
	}
	requireSpeechLockFree(t, lockPath)
}

func TestStartRoutableSayPlaybackKeepsLockForUnremovedJob(t *testing.T) {
	lockPath := useTestSpeechLock(t)
	useTestServer(t)
	originalRetry := guiRemoveRetryInterval
	guiRemoveRetryInterval = 400 * time.Millisecond
	t.Cleanup(func() { guiRemoveRetryInterval = originalRetry })
	useFakeLaunchd(t, launchdStillPresent, launchdNotFound)

	err := startRoutableSayPlayback(context.Background(), []string{"--rate", "200"}, "hello", "")
	if err == nil || !strings.Contains(err.Error(), "speech may still play") {
		t.Fatalf("startRoutableSayPlayback() err = %v, want the unknown-outcome error", err)
	}
	if !speechLockHeld(lockPath) {
		t.Fatal("speech lock was released while the GUI say job might still be speaking")
	}
	playbackWG.Wait()
	requireSpeechLockFree(t, lockPath)
}

// A launchctl that never answers must not hold a caller that has no deadline
// of its own, which is how the speech lock used to be kept indefinitely.
func TestStartGUIJobIsBoundedWhenLaunchctlHangs(t *testing.T) {
	originalPath, originalBootstrap, originalCall := launchctlPath, guiBootstrapTimeout, launchctlTimeout
	launchctlPath, guiBootstrapTimeout, launchctlTimeout = os.Args[0], 300*time.Millisecond, 300*time.Millisecond
	t.Cleanup(func() {
		launchctlPath, guiBootstrapTimeout, launchctlTimeout = originalPath, originalBootstrap, originalCall
	})
	t.Setenv(fakeLaunchctlEnv, "hang")

	began := time.Now()
	_, err := startGUIJob(context.Background(), afplayBinPath, []string{"/tmp/x.mp3"})
	took := time.Since(began)

	var unknown *guiOutcomeUnknownError
	if !errors.As(err, &unknown) {
		t.Fatalf("startGUIJob() err = %v, want *guiOutcomeUnknownError", err)
	}
	if unknown.state != guiJobUndetermined {
		t.Fatalf("state = %v, want undetermined: a probe that timed out proves nothing", unknown.state)
	}
	// bootstrap, kill, bootout and print each time out once.
	if limit := 4*300*time.Millisecond + 8*time.Second; took > limit {
		t.Fatalf("startGUIJob() took %s with a hung launchctl, limit %s", took, limit)
	}
	os.RemoveAll(unknown.dir)
}

// Playing a saved file bootstraps a second GUI job after say has finished. If
// that bootstrap times out, the lock must outlive the wait that reported it.
func TestStartRoutableSayPlaybackKeepsLockWhenSecondJobTimedOut(t *testing.T) {
	lockPath := useTestSpeechLock(t)
	useTestServer(t)
	originalRetry := guiRemoveRetryInterval
	guiRemoveRetryInterval = 400 * time.Millisecond
	t.Cleanup(func() { guiRemoveRetryInterval = originalRetry })
	fake := useFakeLaunchd(t, launchdNotFound)
	unknown := &guiOutcomeUnknownError{label: "com.blacktop.mcp-tts.1.2", dir: t.TempDir(), state: guiJobPresent}
	fakeGUIJobs(t, nil, unknown, nil)

	if err := startRoutableSayPlayback(context.Background(), []string{"--rate", "200"}, "hello", ""); err != nil {
		t.Fatalf("startRoutableSayPlayback() err = %v, want nil: the first job started", err)
	}
	time.Sleep(150 * time.Millisecond)
	if !speechLockHeld(lockPath) {
		t.Fatal("speech lock was released while the timed-out GUI job might still be speaking")
	}
	playbackWG.Wait()
	requireSpeechLockFree(t, lockPath)
	if fake.count("bootout") == 0 {
		t.Fatalf("launchctl calls = %v, want the unremoved job retried", fake.calls)
	}
}
