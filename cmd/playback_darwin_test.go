//go:build darwin

package cmd

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blacktop/mcp-tts/internal/playback"
)

func testClip(format string) clip {
	return clip{label: "openai_tts", format: format, data: []byte("audio bytes"), sampleRate: 24000, budget: 10 * time.Second}
}

func TestStartClipPlaysUnderLockAndCleansUp(t *testing.T) {
	path := useTestSpeechLock(t)
	useTestServer(t)
	players := useStandInPlayers(t, []string{"sleep", "600ms"})

	require.NoError(t, startClip(context.Background(), testClip(clipMP3)))
	started := players.started()
	require.Len(t, started, 1)
	spec := started[0]
	assert.Equal(t, afplayBinPath, spec.Path)
	assert.Equal(t, 10*time.Second, spec.Budget)
	assert.Equal(t, afplayStartupWindow, spec.StartupWindow)
	assert.NotNil(t, spec.Lock, "afplay must inherit the speech lock")
	assert.NotNil(t, spec.Diagnose, "an overrunning player must be sampled")

	require.Len(t, spec.Args, 3)
	assert.Equal(t, []string{"-v", "0.5"}, spec.Args[:2], "afplay must play at the configured volume")
	audioPath := spec.Args[2]
	assert.Equal(t, ".mp3", filepath.Ext(audioPath))
	data, err := os.ReadFile(audioPath)
	require.NoError(t, err)
	assert.Equal(t, "audio bytes", string(data))
	assert.True(t, speechLockHeld(path), "lock must stay held while afplay is still playing")

	playbackWG.Wait()
	requireSpeechLockFree(t, path)
	_, err = os.Stat(audioPath)
	assert.True(t, os.IsNotExist(err), "temp audio file must be removed after playback")
}

func TestStartClipWrapsPCMInWAV(t *testing.T) {
	useTestSpeechLock(t)
	useTestServer(t)
	players := useStandInPlayers(t, []string{"sleep", "300ms"})

	require.NoError(t, startClip(context.Background(), testClip(clipPCM)))
	args := players.started()[0].Args
	audioPath := args[len(args)-1]
	assert.Equal(t, ".wav", filepath.Ext(audioPath))
	data, err := os.ReadFile(audioPath)
	require.NoError(t, err)
	assert.Equal(t, "RIFF", string(data[:4]), "afplay cannot play headerless PCM")
	assert.Equal(t, "audio bytes", string(data[len(data)-len("audio bytes"):]))
}

func TestStartClipSurfacesImmediatePlayerFailure(t *testing.T) {
	path := useTestSpeechLock(t)
	useTestServer(t)
	players := useStandInPlayers(t, []string{"fail", "AudioFileOpen failed"})

	err := startClip(context.Background(), testClip(clipMP3))
	require.ErrorContains(t, err, "failed to start playback")
	require.ErrorContains(t, err, "AudioFileOpen failed")
	requireSpeechLockFree(t, path)
	args := players.started()[0].Args
	_, statErr := os.Stat(args[len(args)-1])
	assert.True(t, os.IsNotExist(statErr), "temp audio file must be removed after a failed start")
}

func TestStartClipLeavesLateFailureToTheLog(t *testing.T) {
	path := useTestSpeechLock(t)
	useTestServer(t)
	// Longer than the startup window: the tool call has already succeeded.
	useStandInPlayers(t, []string{"sleep", (afplayStartupWindow + 300*time.Millisecond).String()})

	began := time.Now()
	require.NoError(t, startClip(context.Background(), testClip(clipMP3)))
	assert.GreaterOrEqual(t, time.Since(began), afplayStartupWindow, "start must wait out the startup window")
	playbackWG.Wait()
	requireSpeechLockFree(t, path)
}

func TestStartClipReportsBusyWithoutPlaying(t *testing.T) {
	path := useTestSpeechLock(t)
	useTestServer(t)
	useAdmissionTimeout(t, 150*time.Millisecond)
	holdSpeechLock(t, path, "say_tts")
	players := useStandInPlayers(t, []string{"ok"})

	err := startClip(context.Background(), testClip(clipMP3))
	require.ErrorContains(t, err, "speech is busy")
	require.ErrorContains(t, err, "recorded holder: say_tts")
	assert.Empty(t, players.started())
	leftovers, _ := filepath.Glob(filepath.Join(os.TempDir(), "mcp-tts-audio-*"))
	for _, leftover := range leftovers {
		info, statErr := os.Stat(leftover)
		if statErr == nil && time.Since(info.ModTime()) < 5*time.Second {
			t.Fatalf("temp audio file %s left behind after a busy admission", leftover)
		}
	}
}

func TestStartClipCancelledDuringAdmission(t *testing.T) {
	path := useTestSpeechLock(t)
	useTestServer(t)
	holdSpeechLock(t, path, "say_tts")
	players := useStandInPlayers(t, []string{"ok"})

	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(100*time.Millisecond, cancel)
	err := startClip(ctx, testClip(clipMP3))
	require.ErrorIs(t, err, context.Canceled)
	assert.Empty(t, players.started())
}

func TestPlayMP3RejectsCorruptAudioBeforePlaying(t *testing.T) {
	useTestSpeechLock(t)
	useTestServer(t)
	fakeSessionMode(t, sessionAqua, "Aqua")
	players := useStandInPlayers(t, []string{"ok"})

	err := playMP3(context.Background(), "openai_tts", "hello", []byte("not an mp3"))
	require.ErrorContains(t, err, "failed to decode audio")
	assert.Empty(t, players.started(), "a corrupt response must not reach the player")
}

func TestPlayMP3BudgetsFromMeasuredDuration(t *testing.T) {
	useTestSpeechLock(t)
	useTestServer(t)
	fakeSessionMode(t, sessionAqua, "Aqua")
	players := useStandInPlayers(t, []string{"ok"})
	data, err := os.ReadFile(filepath.Join("testdata", "tone-3s.mp3"))
	require.NoError(t, err)

	require.NoError(t, playMP3(context.Background(), "openai_tts", "hello", data))
	playbackWG.Wait()
	budget := players.started()[0].Budget
	assert.InDelta(t, playback.BudgetForDuration(3*time.Second).Seconds(), budget.Seconds(), 1.0,
		"budget must come from the clip's real length")
}

func TestPlayPCMBudgetsFromSampleCount(t *testing.T) {
	useTestSpeechLock(t)
	useTestServer(t)
	fakeSessionMode(t, sessionAqua, "Aqua")
	players := useStandInPlayers(t, []string{"ok"})

	fourSeconds := make([]byte, 4*24000*2)
	require.NoError(t, playPCM(context.Background(), "google_tts", "hello", fourSeconds, 24000))
	playbackWG.Wait()
	assert.Equal(t, playback.BudgetForDuration(4*time.Second), players.started()[0].Budget)
}
