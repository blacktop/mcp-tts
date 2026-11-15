//go:build !darwin

package main

import (
	"bufio"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blacktop/mcp-tts/internal/speechlock"
)

// buildServer compiles the real binary so the hidden player can be run with
// its real exit status, which `go run` does not pass through.
func buildServer(t *testing.T) string {
	t.Helper()
	name := "mcp-tts"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	binary := filepath.Join(t.TempDir(), name)
	out, err := exec.Command("go", "build", "-o", binary, ".").CombinedOutput()
	require.NoError(t, err, "building mcp-tts: %s", out)
	return binary
}

func writeSilence(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "silence.pcm")
	require.NoError(t, os.WriteFile(path, make([]byte, 24000), 0o600)) // half a second
	return path
}

func TestHiddenPlayerReportsBusySpeechLock(t *testing.T) {
	binary := buildServer(t)
	lockPath := filepath.Join(t.TempDir(), "speech.lock")
	held, err := speechlock.Acquire(context.Background(), lockPath, "other-speaker")
	require.NoError(t, err)
	defer held.Release()

	cmd := exec.Command(binary, "play", "--format", "pcm", "--rate", "24000",
		"--lock", lockPath, "--admission", "300ms", writeSilence(t))
	out, err := cmd.Output()

	var exit *exec.ExitError
	require.True(t, errors.As(err, &exit), "player must fail while the lock is held, got %v", err)
	assert.Equal(t, 3, exit.ExitCode(), "busy has its own exit status")
	line := strings.TrimSpace(string(out))
	assert.True(t, strings.HasPrefix(line, "busy "), "stdout = %q, want a busy line", line)
	assert.Contains(t, line, `"label":"other-speaker"`)
}

func TestHiddenPlayerTakesSpeechLockBeforeTouchingAudio(t *testing.T) {
	binary := buildServer(t)
	lockPath := filepath.Join(t.TempDir(), "speech.lock")

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, binary, "play", "--format", "pcm", "--rate", "24000",
		"--lock", lockPath, "--admission", "5s", writeSilence(t))
	stdout, err := cmd.StdoutPipe()
	require.NoError(t, err)
	require.NoError(t, cmd.Start())

	lines := bufio.NewScanner(stdout)
	require.True(t, lines.Scan(), "player printed nothing")
	assert.Equal(t, "admitted", lines.Text())
	for lines.Scan() {
		assert.Equal(t, "ready", lines.Text(), "the only line after admitted is ready")
	}
	// With no audio device (CI) the player fails after admission; with one it
	// plays the silence. Either way it must exit and leave the lock free.
	_ = cmd.Wait()

	free, cancelFree := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancelFree()
	lock, err := speechlock.Acquire(free, lockPath, "next")
	require.NoError(t, err, "speech lock still held after the player exited")
	lock.Release()
}

func TestHiddenPlayerRejectsUnknownFormat(t *testing.T) {
	binary := buildServer(t)
	out, err := exec.Command(binary, "play", "--format", "ogg", writeSilence(t)).CombinedOutput()
	require.Error(t, err)
	assert.Contains(t, string(out), "unsupported audio format")
}
