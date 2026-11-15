package cmd

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/charmbracelet/log"
	"github.com/gopxl/beep/v2/mp3"

	"github.com/blacktop/mcp-tts/internal/playback"
	"github.com/blacktop/mcp-tts/internal/speechlock"
)

const (
	// drainSlack is added to the longest possible playback when a shutdown
	// waits for speech that is already under way.
	drainSlack = 30 * time.Second
	// stopAllowance is how long cancelled playbacks get to end their players.
	stopAllowance = 10 * time.Second

	clipMP3 = "mp3"
	clipPCM = "pcm"
)

var (
	// admissionTimeout bounds the wait for the speech lock. It is not a
	// fairness guarantee and does not bound the rest of a tool call. A
	// variable so tests can shorten it.
	admissionTimeout = 45 * time.Second
	// speechLockPath locates the cross-process speech lock. A variable so
	// tests can use a private lock file.
	speechLockPath = speechlock.DefaultPath
	// startPlayback launches a playback process. A variable so tests can run
	// stand-in players.
	startPlayback = playback.Start
)

// clip is fully synthesized audio ready to be played.
type clip struct {
	label      string
	format     string // clipMP3 or clipPCM
	data       []byte
	sampleRate int // clipPCM only
	budget     time.Duration
}

// acquireSpeechLock waits for this process's turn to speak, for at most
// admissionTimeout and no longer than ctx allows. It returns ctx.Err() when the
// caller gave up, and an error naming the lock's recorded holder when the wait
// timed out. With sequential speech disabled it returns a nil lock.
func acquireSpeechLock(ctx context.Context, label string) (*speechlock.Lock, error) {
	if !sequentialTTS {
		return nil, nil
	}
	path, err := speechLockPath()
	if err != nil {
		return nil, fmt.Errorf("speech lock unavailable: %w", err)
	}
	admission, cancel := context.WithTimeout(ctx, admissionTimeout)
	defer cancel()
	lock, err := speechlock.Acquire(admission, path, label)
	switch {
	case err == nil:
		return lock, nil
	case ctx.Err() != nil:
		return nil, ctx.Err()
	case errors.Is(err, context.DeadlineExceeded):
		holder, known := speechlock.ReadHolder(path)
		return nil, speechBusyError(holder, known)
	default:
		return nil, err
	}
}

// speechBusyError explains an admission timeout. The recorded holder says who
// held the lock, not why speech was delayed, and the wording keeps that apart.
func speechBusyError(holder speechlock.Holder, known bool) error {
	record := "no holder is recorded"
	if known {
		record = fmt.Sprintf("recorded holder: %s, pid %d", holder.Label, holder.PID)
		if holder.PlayerPID != 0 {
			record += fmt.Sprintf(", player pid %d", holder.PlayerPID)
		}
		record += fmt.Sprintf(", since %s; the record may be stale and does not explain the delay",
			holder.Since.Format(time.TimeOnly))
	}
	return fmt.Errorf("speech is busy: gave up after waiting %s for other speech to finish, nothing was spoken (%s)",
		admissionTimeout, record)
}

// detachPlayback follows a started player to its end in the background, so a
// cancelled or timed-out tool request cannot cut speech short. The player's own
// budget bounds how long that can take. cleanup runs once the player is gone.
func detachPlayback(label string, proc *playback.Proc, cleanup func()) {
	playbackWG.Go(func() {
		defer cleanup()
		// Marking the terminal can take a moment; it must not hold up the
		// tool result, so it happens here rather than in the caller.
		stopSpeaking := beginRexSpeaking(serverCtx)
		defer stopSpeaking()
		if err := proc.Wait(serverCtx); err != nil && serverCtx.Err() == nil {
			log.Warn("Playback failed after it started", "label", label, "error", err)
			return
		}
		log.Debug("Finished speaking", "label", label)
	})
}

// drainPlayback waits for speech already under way when the server shuts down.
// Every playback is bounded by its own budget; the limit here is the backstop
// that guarantees the server exits. It then cancels whatever is left.
func drainPlayback(cancel context.CancelFunc) {
	if waitGroupWithin(&playbackWG, playback.HardCap+drainSlack) {
		return
	}
	log.Warn("Playback did not finish before the shutdown limit; stopping it")
	cancel()
	waitGroupWithin(&playbackWG, stopAllowance)
}

// stopPlayback cancels in-flight playback and gives the players a moment to be
// ended before the process exits.
func stopPlayback(cancel context.CancelFunc) {
	cancel()
	waitGroupWithin(&playbackWG, stopAllowance)
}

func waitGroupWithin(wg *sync.WaitGroup, limit time.Duration) bool {
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()
	timer := time.NewTimer(limit)
	defer timer.Stop()
	select {
	case <-done:
		return true
	case <-timer.C:
		return false
	}
}

// clipBudget returns how long a clip may play. A zero duration means its
// length could not be measured; that is handled as its own case rather than
// read as a zero-length clip.
func clipBudget(label string, duration time.Duration, textLen int) time.Duration {
	if duration > 0 {
		return playback.BudgetForDuration(duration)
	}
	budget := playback.BudgetForText(textLen)
	log.Warn("Audio length is unknown; budgeting playback from the text length", "label", label, "budget", budget)
	return budget
}

// seekableAudio gives the MP3 decoder a seekable source. Without seeking the
// decoder cannot determine the clip's length and reports it as zero.
type seekableAudio struct {
	*bytes.Reader
}

func (seekableAudio) Close() error { return nil }

// mp3Duration validates an MP3 and measures its length. It returns 0 when the
// length cannot be determined.
func mp3Duration(audioData []byte) (time.Duration, error) {
	streamer, format, err := mp3.Decode(seekableAudio{bytes.NewReader(audioData)})
	if err != nil {
		return 0, err
	}
	defer streamer.Close()
	return format.SampleRate.D(streamer.Len()), nil
}

// pcmDuration returns the length of 16-bit mono PCM audio.
func pcmDuration(audioData []byte, sampleRate int) time.Duration {
	if sampleRate <= 0 {
		return 0
	}
	const bytesPerSample = 2
	return time.Duration(len(audioData)/bytesPerSample) * time.Second / time.Duration(sampleRate)
}

// writeTempFile creates a 0600 temp file from pattern, writes data, and returns
// the path with a cleanup func. On any failure it removes the partial file.
func writeTempFile(pattern string, data []byte) (string, func(), error) {
	f, err := os.CreateTemp("", pattern)
	if err != nil {
		return "", func() {}, fmt.Errorf("creating temp file %q: %w", pattern, err)
	}
	path := f.Name()
	if _, err := f.Write(data); err != nil {
		f.Close()
		os.Remove(path)
		return "", func() {}, fmt.Errorf("writing temp file %q: %w", path, err)
	}
	if err := f.Close(); err != nil {
		os.Remove(path)
		return "", func() {}, fmt.Errorf("closing temp file %q: %w", path, err)
	}
	return path, func() { os.Remove(path) }, nil
}
