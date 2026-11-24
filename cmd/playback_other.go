//go:build !darwin

package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"time"

	"github.com/charmbracelet/log"

	"github.com/blacktop/mcp-tts/internal/playback"
)

const (
	// playerReadyTimeout bounds audio device initialization in the hidden
	// player, which the audio library itself does not bound.
	playerReadyTimeout = 5 * time.Second
	// playerAdmissionSlack lets the player's own "busy" answer arrive before
	// this process stops waiting for it.
	playerAdmissionSlack = 5 * time.Second
)

// startClip plays a clip in a hidden `mcp-tts play` process. That process
// takes the speech lock itself and holds it for as long as it lives, so the
// lock cannot be free while it is still able to make sound.
func startClip(ctx context.Context, c clip) error {
	path, removeFile, err := writeTempFile("mcp-tts-audio-*."+c.format, c.data)
	if err != nil {
		return fmt.Errorf("preparing audio for %s: %w", c.label, err)
	}
	exe, err := os.Executable()
	if err != nil {
		removeFile()
		return fmt.Errorf("locating the mcp-tts executable: %w", err)
	}
	lockPath := ""
	if sequentialTTS {
		if lockPath, err = speechLockPath(); err != nil {
			removeFile()
			return fmt.Errorf("speech lock unavailable: %w", err)
		}
	}
	proc, err := playback.StartPlayer(ctx, playback.PlayerSpec{
		Label: c.label,
		Exe:   exe,
		Args: []string{
			"play",
			"--format", c.format,
			"--rate", strconv.Itoa(c.sampleRate),
			"--label", c.label,
			"--lock", lockPath,
			"--admission", admissionTimeout.String(),
			"--volume", volumeArg(),
			path,
		},
		Admission:    admissionTimeout + playerAdmissionSlack,
		ReadyTimeout: playerReadyTimeout,
		Budget:       c.budget,
	})
	if err != nil {
		removeFile()
		var busy *playback.BusyError
		switch {
		case ctx.Err() != nil:
			return ctx.Err()
		case errors.As(err, &busy):
			return speechBusyError(busy.Holder, busy.Known)
		default:
			return fmt.Errorf("failed to start playback: %w", err)
		}
	}
	log.Info("Speaking text", "label", c.label)
	detachPlayback(c.label, proc, removeFile)
	return nil
}

// playbackDiagnostic returns no diagnostic: stack sampling of a stalled player
// is implemented for macOS only.
func playbackDiagnostic() func(context.Context, playback.Incident) { return nil }
