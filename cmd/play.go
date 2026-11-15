//go:build !darwin

package cmd

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/gopxl/beep/v2"
	"github.com/gopxl/beep/v2/mp3"
	"github.com/gopxl/beep/v2/speaker"
	"github.com/spf13/cobra"

	"github.com/blacktop/mcp-tts/internal/playback"
)

// playerBusyExit is the hidden player's exit status when the speech lock
// stayed busy for the whole admission wait.
const playerBusyExit = 3

var playOptions struct {
	format    string
	rate      int
	label     string
	lockPath  string
	admission time.Duration
}

// playCmd is the hidden player. The server runs one per utterance so the audio
// library starts from a fresh process every time: a stalled device is recovered
// by ending this process, which the library cannot do from the inside.
var playCmd = &cobra.Command{
	Use:           "play <file>",
	Short:         "Play one audio file (internal)",
	Hidden:        true,
	Args:          cobra.ExactArgs(1),
	SilenceUsage:  true,
	SilenceErrors: true,
	RunE: func(_ *cobra.Command, args []string) error {
		err := playback.RunPlayer(context.Background(), playback.PlayerOptions{
			LockPath:  playOptions.lockPath,
			Label:     playOptions.label,
			Admission: playOptions.admission,
			Out:       os.Stdout,
		}, &beepBackend{path: args[0], format: playOptions.format, sampleRate: playOptions.rate})
		var busy *playback.BusyError
		if errors.As(err, &busy) {
			os.Exit(playerBusyExit)
		}
		return err
	},
}

func init() {
	playCmd.Flags().StringVar(&playOptions.format, "format", clipMP3, "Audio format: mp3 or pcm")
	playCmd.Flags().IntVar(&playOptions.rate, "rate", 0, "Sample rate of pcm audio")
	playCmd.Flags().StringVar(&playOptions.label, "label", "", "Name of the tool that produced the audio")
	playCmd.Flags().StringVar(&playOptions.lockPath, "lock", "", "Speech lock file; empty plays without waiting")
	playCmd.Flags().DurationVar(&playOptions.admission, "admission", admissionTimeout, "Longest wait for the speech lock")
	rootCmd.AddCommand(playCmd)
}

// beepBackend plays one file through the Beep speaker.
type beepBackend struct {
	path       string
	format     string
	sampleRate int
	streamer   beep.Streamer
}

func (b *beepBackend) Prepare() error {
	data, err := os.ReadFile(b.path)
	if err != nil {
		return fmt.Errorf("reading audio file: %w", err)
	}
	var rate beep.SampleRate
	switch b.format {
	case clipMP3:
		streamer, format, err := mp3.Decode(seekableAudio{bytes.NewReader(data)})
		if err != nil {
			return fmt.Errorf("failed to decode audio: %w", err)
		}
		b.streamer, rate = streamer, format.SampleRate
	case clipPCM:
		if b.sampleRate <= 0 {
			return fmt.Errorf("pcm audio needs a positive --rate, got %d", b.sampleRate)
		}
		rate = beep.SampleRate(b.sampleRate)
		b.streamer = &PCMStream{data: data, sampleRate: rate}
	default:
		return fmt.Errorf("unsupported audio format %q; expected %s or %s", b.format, clipMP3, clipPCM)
	}
	if err := speaker.Init(rate, rate.N(time.Second/10)); err != nil {
		return fmt.Errorf("failed to initialize speaker: %w", err)
	}
	return nil
}

func (b *beepBackend) Play() error {
	speaker.PlayAndWait(b.streamer)
	return b.streamer.Err()
}
