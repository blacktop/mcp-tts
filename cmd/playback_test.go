package cmd

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blacktop/mcp-tts/internal/playback"
	"github.com/blacktop/mcp-tts/internal/speechlock"
)

// standInArg marks an invocation of the test binary as a stand-in player.
const standInArg = "cmd-test-stand-in"

// fakeLaunchctlEnv makes the test binary behave as launchctl when it is run in
// launchctl's place.
const fakeLaunchctlEnv = "MCP_TTS_TEST_FAKE_LAUNCHCTL"

func TestMain(m *testing.M) {
	if os.Getenv(fakeLaunchctlEnv) == "hang" {
		time.Sleep(time.Hour)
		os.Exit(0)
	}
	if len(os.Args) > 2 && os.Args[1] == standInArg {
		os.Exit(runStandIn(os.Args[2], os.Args[3:]))
	}
	os.Exit(m.Run())
}

// runStandIn is a player that makes no sound.
func runStandIn(mode string, args []string) int {
	switch mode {
	case "ok":
		return 0
	case "sleep":
		d, err := time.ParseDuration(args[0])
		if err != nil {
			return 64
		}
		time.Sleep(d)
		return 0
	case "hang":
		time.Sleep(time.Hour)
		return 0
	case "fail":
		fmt.Fprintln(os.Stderr, args[0])
		return 1
	case "echo-stdin":
		// Fails with whatever it was given, so a test can see the text arrive.
		text, _ := io.ReadAll(os.Stdin)
		fmt.Fprintf(os.Stderr, "heard: %s\n", text)
		return 1
	}
	return 64
}

// standInPlayers replaces the real players with a stand-in and records what
// the server asked to run.
type standInPlayers struct {
	mu    sync.Mutex
	specs []playback.Spec
	// modes are the stand-in behaviours for successive starts; the last one
	// repeats.
	modes [][]string
}

func useStandInPlayers(t *testing.T, modes ...[]string) *standInPlayers {
	t.Helper()
	players := &standInPlayers{modes: modes}
	original := startPlayback
	startPlayback = func(spec playback.Spec) (*playback.Proc, error) {
		players.mu.Lock()
		mode := players.modes[min(len(players.specs), len(players.modes)-1)]
		players.specs = append(players.specs, spec)
		players.mu.Unlock()
		spec.Path = os.Args[0]
		spec.Args = append([]string{standInArg}, mode...)
		spec.Diagnose = nil
		return playback.Start(spec)
	}
	t.Cleanup(func() { startPlayback = original })
	return players
}

func (p *standInPlayers) started() []playback.Spec {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]playback.Spec(nil), p.specs...)
}

// useTestSpeechLock gives the test its own speech lock file and turns
// sequential speech on.
func useTestSpeechLock(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "speech.lock")
	originalPath, originalSequential := speechLockPath, sequentialTTS
	speechLockPath = func() (string, error) { return path, nil }
	sequentialTTS = true
	t.Cleanup(func() { speechLockPath, sequentialTTS = originalPath, originalSequential })
	return path
}

func useAdmissionTimeout(t *testing.T, d time.Duration) {
	t.Helper()
	original := admissionTimeout
	admissionTimeout = d
	t.Cleanup(func() { admissionTimeout = original })
}

// useTestServer points serverCtx at a fresh context and waits for detached
// playback when the test ends.
func useTestServer(t *testing.T) context.CancelFunc {
	t.Helper()
	original := serverCtx
	ctx, cancel := context.WithCancel(context.Background())
	serverCtx = ctx
	t.Cleanup(func() {
		cancel()
		playbackWG.Wait()
		serverCtx = original
	})
	return cancel
}

func holdSpeechLock(t *testing.T, path, label string) *speechlock.Lock {
	t.Helper()
	lock, err := speechlock.Acquire(context.Background(), path, label)
	require.NoError(t, err)
	t.Cleanup(lock.Release)
	return lock
}

func speechLockHeld(path string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	lock, err := speechlock.Acquire(ctx, path, "probe")
	if err != nil {
		return true
	}
	lock.Release()
	return false
}

func requireSpeechLockFree(t *testing.T, path string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	lock, err := speechlock.Acquire(ctx, path, "probe")
	require.NoError(t, err, "speech lock is still held")
	lock.Release()
}

func TestAcquireSpeechLockTimesOutWithRecordedHolder(t *testing.T) {
	path := useTestSpeechLock(t)
	useAdmissionTimeout(t, 200*time.Millisecond)
	holdSpeechLock(t, path, "openai_tts")

	lock, err := acquireSpeechLock(context.Background(), "say_tts")
	require.Nil(t, lock)
	require.Error(t, err)
	message := err.Error()
	assert.Contains(t, message, "speech is busy")
	assert.Contains(t, message, "nothing was spoken")
	assert.Contains(t, message, fmt.Sprintf("recorded holder: openai_tts, pid %d", os.Getpid()))
	assert.Contains(t, message, "does not explain the delay", "the holder must not be presented as the cause")
}

func TestAcquireSpeechLockReturnsCancellationNotBusy(t *testing.T) {
	path := useTestSpeechLock(t)
	holdSpeechLock(t, path, "openai_tts")

	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(100*time.Millisecond, cancel)
	lock, err := acquireSpeechLock(ctx, "say_tts")
	require.Nil(t, lock)
	require.ErrorIs(t, err, context.Canceled)
}

func TestAcquireSpeechLockSkippedForConcurrentSpeech(t *testing.T) {
	path := useTestSpeechLock(t)
	holdSpeechLock(t, path, "openai_tts")
	sequentialTTS = false

	lock, err := acquireSpeechLock(context.Background(), "say_tts")
	require.NoError(t, err)
	require.Nil(t, lock, "concurrent speech must not wait on the speech lock")
}

func TestSpeechBusyErrorWithoutRecord(t *testing.T) {
	message := speechBusyError(speechlock.Holder{}, false).Error()
	assert.Contains(t, message, "no holder is recorded")
	assert.NotContains(t, message, "pid 0")
}

func TestMP3DurationMeasuresRealFile(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("testdata", "tone-3s.mp3"))
	require.NoError(t, err)

	duration, err := mp3Duration(data)
	require.NoError(t, err)
	assert.InDelta(t, 3.0, duration.Seconds(), 0.25, "a 3s MP3 must not be measured as empty")

	budget := clipBudget("openai_tts", duration, 40)
	assert.Equal(t, playback.BudgetForDuration(duration), budget)
	assert.Greater(t, budget, duration, "budget must cover the whole clip")
}

func TestMP3DurationRejectsCorruptAudio(t *testing.T) {
	_, err := mp3Duration([]byte("this is not an mp3 file at all"))
	require.Error(t, err)
}

func TestClipBudgetTreatsUnknownLengthAsUnknown(t *testing.T) {
	// A zero duration is "could not be measured". Reading it as a zero-length
	// clip would give real speech only the fixed slack and cut it off.
	budget := clipBudget("openai_tts", 0, 900)
	assert.Equal(t, playback.BudgetForText(900), budget)
	assert.GreaterOrEqual(t, budget, 2*time.Minute)
}

func TestPCMDuration(t *testing.T) {
	assert.Equal(t, 2*time.Second, pcmDuration(make([]byte, 2*24000*2), 24000))
	assert.Equal(t, time.Duration(0), pcmDuration(make([]byte, 100), 0))
}

func TestDetachPlaybackReleasesWhenPlayerExits(t *testing.T) {
	useTestServer(t)
	players := useStandInPlayers(t, []string{"sleep", "100ms"})
	proc, err := startPlayback(playback.Spec{Label: "test", Budget: 10 * time.Second})
	require.NoError(t, err)

	cleaned := make(chan struct{})
	detachPlayback("test", proc, func() { close(cleaned) })
	select {
	case <-cleaned:
	case <-time.After(10 * time.Second):
		t.Fatal("cleanup never ran after the player exited")
	}
	require.Len(t, players.started(), 1)
}

func TestDetachPlaybackStopsPlayerOnServerShutdown(t *testing.T) {
	cancel := useTestServer(t)
	useStandInPlayers(t, []string{"hang"})
	proc, err := startPlayback(playback.Spec{Label: "test", Budget: time.Minute})
	require.NoError(t, err)

	cleaned := make(chan struct{})
	detachPlayback("test", proc, func() { close(cleaned) })
	cancel()
	select {
	case <-cleaned:
	case <-time.After(15 * time.Second):
		t.Fatal("a hung player outlived server shutdown")
	}
}

func TestWaitGroupWithin(t *testing.T) {
	var wg sync.WaitGroup
	release := make(chan struct{})
	wg.Go(func() { <-release })
	assert.False(t, waitGroupWithin(&wg, 50*time.Millisecond), "must report a wait that timed out")
	close(release)
	assert.True(t, waitGroupWithin(&wg, 5*time.Second))
}

func TestDeliverAudioReportsCancellationDuringAdmission(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	result, _, err := deliverAudio(ctx, "hello", []byte("audio"), saveMP3, func(context.Context) error {
		cancel()
		return ctx.Err()
	})
	require.NoError(t, err)
	assert.False(t, result.IsError)
	assert.Equal(t, "Request cancelled", extractTextFromMCPResult(result))
}

func TestDeliverAudioSurfacesPlaybackStartError(t *testing.T) {
	result, _, err := deliverAudio(context.Background(), "hello", []byte("audio"), saveMP3, func(context.Context) error {
		return errors.New("failed to start playback: afplay: exit status 1: cannot open file")
	})
	require.NoError(t, err)
	assert.True(t, result.IsError)
	assert.Contains(t, extractTextFromMCPResult(result), "cannot open file")
}

// useSaveMode sets the audio-saving options for one test.
func useSaveMode(t *testing.T, dir string, play bool) {
	t.Helper()
	prevDir, prevNoPlay := outputDir, noPlay
	outputDir, noPlay = dir, !play
	t.Cleanup(func() { outputDir, noPlay = prevDir, prevNoPlay })
}

func TestRenderSayRendersToTempFileWithoutTheSpeechLock(t *testing.T) {
	useSaveMode(t, "", true)
	players := useStandInPlayers(t, []string{"ok"})

	audio, savedPath, err := renderSay(context.Background(), sessionAqua, []string{"--rate", "200"}, "hello")
	require.NoError(t, err)
	assert.Empty(t, savedPath)
	assert.NotNil(t, audio, "the rendered audio must be returned for playback")

	started := players.started()
	require.Len(t, started, 1, "say only renders; nothing plays yet")
	require.Len(t, started[0].Args, 5)
	out := started[0].Args[3]
	assert.Equal(t, "/usr/bin/say", started[0].Path)
	assert.Equal(t, []string{"--rate", "200", "-o", out, "hello"}, started[0].Args)
	assert.Equal(t, ".aiff", filepath.Ext(out))
	assert.Equal(t, playback.BudgetForText(len("hello")), started[0].Budget)
	assert.Nil(t, started[0].Lock, "rendering makes no sound, so it must not hold the speech lock")
	_, err = os.Stat(out)
	assert.True(t, os.IsNotExist(err), "the temp audio file must be removed")
}

func TestRenderSaySaveOnlyWritesTheSavedFile(t *testing.T) {
	dir := t.TempDir()
	useSaveMode(t, dir, false)
	players := useStandInPlayers(t, []string{"ok"})

	audio, savedPath, err := renderSay(context.Background(), sessionAqua, nil, "hello")
	require.NoError(t, err)
	assert.Nil(t, audio, "save-only mode plays nothing, so the audio is not read")
	assert.Equal(t, dir, filepath.Dir(savedPath))
	assert.Equal(t, ".aiff", filepath.Ext(savedPath))
	require.Len(t, players.started(), 1)
	assert.Equal(t, []string{"-o", savedPath, "hello"}, players.started()[0].Args)
}

func TestRenderSaySurfacesFailures(t *testing.T) {
	useSaveMode(t, "", true)
	t.Run("start", func(t *testing.T) {
		original := startPlayback
		startPlayback = func(playback.Spec) (*playback.Proc, error) { return nil, errors.New("exec failed") }
		t.Cleanup(func() { startPlayback = original })

		_, _, err := renderSay(context.Background(), sessionAqua, nil, "hello")
		require.ErrorContains(t, err, "failed to start say command")
	})
	t.Run("exit", func(t *testing.T) {
		useStandInPlayers(t, []string{"fail", "voice not installed"})

		_, _, err := renderSay(context.Background(), sessionAqua, nil, "hello")
		require.ErrorContains(t, err, "voice not installed")
	})
}

// skipWithoutVoiceSay skips tests that run voice-say itself, which feeds text
// on standard input and is not supported on Windows.
func skipWithoutVoiceSay(t *testing.T) {
	t.Helper()
	if !voiceTTSSupported {
		t.Skip("voice_tts is not available on this platform")
	}
}

func TestRunVoiceSayCommandReturnsFailureWithOutput(t *testing.T) {
	skipWithoutVoiceSay(t)
	useStandInPlayers(t, []string{"echo-stdin"})
	err := runVoiceSayCommand(context.Background(), "/test/bin/voice-say", []string{"--quiet"}, "spoken words", nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "heard: spoken words", "text must reach voice-say on stdin and its output must be returned")
}

func TestRunVoiceSayCommandStopsOnRequestCancel(t *testing.T) {
	skipWithoutVoiceSay(t)
	useStandInPlayers(t, []string{"hang"})
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(150*time.Millisecond, cancel)

	began := time.Now()
	err := runVoiceSayCommand(ctx, "/test/bin/voice-say", nil, "hello", nil)
	require.ErrorIs(t, err, context.Canceled)
	assert.Less(t, time.Since(began), 10*time.Second, "a cancelled voice_tts call must stop voice-say")
}

func TestWriteTempFileIsPrivateAndRemovable(t *testing.T) {
	path, remove, err := writeTempFile("mcp-tts-test-*.bin", []byte("payload"))
	require.NoError(t, err)
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.True(t, bytes.Equal([]byte("payload"), data))
	remove()
	_, err = os.Stat(path)
	assert.True(t, os.IsNotExist(err))
	assert.True(t, strings.HasPrefix(filepath.Base(path), "mcp-tts-test-"))
}
