//go:build darwin

package cmd

import (
	"bytes"
	"context"
	"fmt"
	"time"

	"github.com/charmbracelet/log"

	"github.com/blacktop/mcp-tts/internal/playback"
)

// afplayStartupWindow is how long a clip's start waits to catch afplay failing
// at once (unreadable file, no output device). afplay has no readiness signal,
// so this is an early-failure heuristic and not proof that audio is audible.
const afplayStartupWindow = 250 * time.Millisecond

// startClip plays a clip with afplay as a child of this process. The child
// inherits the speech lock, so the lock stays held while it can make sound.
func startClip(ctx context.Context, c clip) error {
	ext, data := clipMP3, c.data
	if c.format == clipPCM {
		var wav bytes.Buffer
		if err := writeWAVHeader(&wav, len(c.data), c.sampleRate, 1, 16); err != nil {
			return fmt.Errorf("building WAV for %s: %w", c.label, err)
		}
		wav.Write(c.data)
		ext, data = "wav", wav.Bytes()
	}
	path, removeFile, err := writeTempFile("mcp-tts-audio-*."+ext, data)
	if err != nil {
		return fmt.Errorf("preparing audio for %s: %w", c.label, err)
	}
	lock, err := acquireSpeechLock(ctx, c.label)
	if err != nil {
		removeFile()
		return err
	}
	proc, err := startPlayback(playback.Spec{
		Label:         c.label,
		Path:          afplayBinPath,
		Args:          afplayArgs(path),
		Budget:        c.budget,
		Lock:          lock,
		StartupWindow: afplayStartupWindow,
		Diagnose:      playbackDiagnostic(),
	})
	if err != nil {
		lock.Release()
		removeFile()
		return fmt.Errorf("failed to start playback: %w", err)
	}
	log.Info("Speaking text", "label", c.label)
	detachPlayback(c.label, proc, func() {
		lock.Release()
		removeFile()
	})
	return nil
}

// playbackDiagnostic returns the diagnostic run when a player overruns its
// budget: a short stack sample of the player, saved for later inspection.
func playbackDiagnostic() func(context.Context, playback.Incident) {
	return func(ctx context.Context, incident playback.Incident) {
		dir, err := playback.DefaultIncidentDir()
		if err != nil {
			log.Warn("Playback incident not recorded", "error", err)
			return
		}
		path, err := playback.CaptureIncident(ctx, dir, incident)
		if err != nil {
			log.Warn("Playback incident report is incomplete", "path", path, "error", err)
			return
		}
		log.Warn("Playback incident recorded", "path", path)
	}
}
