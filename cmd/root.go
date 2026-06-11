/*
Copyright © 2025 blacktop

Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction, including without limitation the rights
to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
copies of the Software, and to permit persons to whom the Software is
furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in
all copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN
THE SOFTWARE.
*/
package cmd

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"time"

	"github.com/caarlos0/ctrlc"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/log"
	"github.com/gopxl/beep/v2"
	"github.com/gopxl/beep/v2/mp3"
	"github.com/gopxl/beep/v2/speaker"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/openai/openai-go"
	"github.com/openai/openai-go/option"
	"github.com/spf13/cobra"
	"google.golang.org/genai"
)

var (
	verbose bool
	logger  *log.Logger
	// Version stores the service's version
	Version string
	// Flag to suppress "Speaking:" output
	suppressSpeakingOutput bool
	// Flag to enable interactive elicitation prompts (default: false).
	// Agents call tools with explicit arguments; elicitation interrupts that
	// flow, so it is opt-in for interactive clients that want it.
	elicitEnabled bool
	// serverCtx is cancelled only when the MCP server shuts down. Audio
	// playback runs under this context so a cancelled tool request (client
	// timeout or end-of-turn teardown) cannot truncate in-flight speech.
	serverCtx context.Context
	// playbackWG tracks in-flight background playback so a clean shutdown
	// (stdin EOF / client disconnect) can wait for it to finish instead of
	// cutting off detached audio — e.g. a one-shot `cat msgs.jsonl | mcp-tts`
	// or a final notification right before the session ends.
	playbackWG sync.WaitGroup
	// Global TTS mutex to prevent concurrent speech
	ttsMutex sync.Mutex
	// Flag to enable/disable sequential TTS (default: true)
	sequentialTTS bool = true
	// Speaker is initialized once; all streams resample to the initial rate
	speakerInitOnce   sync.Once
	speakerInitErr    error
	speakerSampleRate beep.SampleRate
	// Audio saving options
	outputDir string // Directory to save audio files
	noPlay    bool   // Skip playback when saving
)

// acquireTTSLock attempts to acquire the TTS mutex with context support.
// Returns a release function that should be deferred.
func acquireTTSLock(ctx context.Context) (func(), error) {
	if !sequentialTTS {
		return func() {}, nil
	}

	pid := os.Getpid()
	log.Debug("Attempting to acquire local TTS mutex", "pid", pid)
	acquired := make(chan struct{})

	go func() {
		ttsMutex.Lock()
		log.Debug("Local TTS mutex acquired", "pid", pid)
		close(acquired)
	}()

	select {
	case <-acquired:
		log.Debug("Attempting to acquire global TTS lock", "pid", pid)
		globalRelease, err := acquireGlobalTTSLock(ctx)
		if err != nil {
			log.Debug("Failed to acquire global lock, releasing local mutex", "pid", pid, "error", err)
			ttsMutex.Unlock()
			return nil, err
		}

		log.Debug("Both TTS locks acquired successfully", "pid", pid)
		return func() {
			log.Debug("Releasing both TTS locks", "pid", pid)
			globalRelease()
			ttsMutex.Unlock()
			log.Debug("Both TTS locks released", "pid", pid)
		}, nil

	case <-ctx.Done():
		select {
		case <-acquired:
			ttsMutex.Unlock()
		default:
		}
		return nil, ctx.Err()
	}
}

// initSpeaker initializes the speaker once with the given sample rate.
// The first TTS call determines the speaker sample rate for the process lifetime.
// Subsequent calls with different sample rates will have their audio resampled.
// Common rates: OpenAI/Google=24000Hz, ElevenLabs=44100Hz.
func initSpeaker(sampleRate beep.SampleRate) error {
	speakerInitOnce.Do(func() {
		speakerSampleRate = sampleRate
		speakerInitErr = speaker.Init(sampleRate, sampleRate.N(time.Second/10))
	})
	return speakerInitErr
}

// resampleToSpeaker resamples the streamer to match the speaker's sample rate.
// Must be called after initSpeaker. Returns the original streamer if rates match.
func resampleToSpeaker(streamer beep.Streamer, from beep.SampleRate) beep.Streamer {
	if speakerSampleRate == 0 || from == speakerSampleRate {
		return streamer
	}
	return beep.Resample(4, from, speakerSampleRate, streamer)
}

// playInBackground acquires the TTS lock synchronously, then runs the playback
// function detached from the request that triggered it. This surfaces lock
// acquisition failures to the caller while still letting playback outlive a
// cancelled or timed-out tool request.
func playInBackground(label string, play func(ctx context.Context)) error {
	release, err := acquireTTSLock(serverCtx)
	if err != nil {
		return fmt.Errorf("failed to acquire TTS lock: %w", err)
	}

	playbackWG.Go(func() {
		defer release()
		log.Debug("Starting background playback", "label", label)
		play(serverCtx)
	})
	return nil
}

func startSayPlayback(playArgs []string, savedPath, text string) error {
	release, err := acquireTTSLock(serverCtx)
	if err != nil {
		return fmt.Errorf("failed to acquire TTS lock: %w", err)
	}

	cmd := exec.CommandContext(serverCtx, "/usr/bin/say", playArgs...)
	log.Debug("Starting say command", "args", playArgs)
	if err := cmd.Start(); err != nil {
		release()
		return fmt.Errorf("failed to start say command: %w", err)
	}

	playbackWG.Go(func() {
		defer release()
		if err := cmd.Wait(); err != nil {
			if serverCtx.Err() == nil {
				log.Error("Say command failed", "error", err)
			}
			return
		}
		// say -o wrote the file without playing; play it now.
		if savedPath != "" {
			log.Debug("Playing saved AIFF file", "path", savedPath)
			if err := exec.CommandContext(serverCtx, "afplay", savedPath).Run(); err != nil && serverCtx.Err() == nil {
				log.Warn("Failed to play saved audio", "error", err)
			}
		}
		log.Info("Speaking text completed", "text", text)
	})

	return nil
}

// playStreamer plays a beep streamer and blocks until playback finishes or ctx
// is cancelled (server shutdown), clearing the speaker on cancellation.
func playStreamer(ctx context.Context, streamer beep.Streamer) {
	done := make(chan struct{})
	speaker.Play(beep.Seq(streamer, beep.Callback(func() { close(done) })))
	select {
	case <-done:
	case <-ctx.Done():
		speaker.Clear()
	}
}

// playMP3 decodes MP3 bytes and initializes the speaker synchronously — so a
// corrupt/undecodable response or an unavailable audio device surfaces as an
// error to the caller — then detaches the real-time playback so a cancelled or
// timed-out tool call cannot truncate the audio.
func playMP3(label string, audioData []byte) error {
	if handled, err := routeCloudPlayback(label, "mp3", func() ([]byte, error) {
		return audioData, nil
	}); handled {
		return err
	}
	streamer, format, err := mp3.Decode(io.NopCloser(bytes.NewReader(audioData)))
	if err != nil {
		return fmt.Errorf("failed to decode audio: %w", err)
	}
	if err := initSpeaker(format.SampleRate); err != nil {
		streamer.Close()
		return fmt.Errorf("failed to initialize speaker: %w", err)
	}
	if err := playInBackground(label, func(ctx context.Context) {
		defer streamer.Close()
		log.Info("Speaking text", "label", label)
		playStreamer(ctx, resampleToSpeaker(streamer, format.SampleRate))
		log.Debug("Finished speaking", "label", label)
	}); err != nil {
		streamer.Close()
		return err
	}
	return nil
}

// playPCM initializes the speaker synchronously (surfacing an unavailable audio
// device to the caller) then detaches real-time playback of raw 16-bit
// little-endian PCM samples.
func playPCM(label string, audioData []byte, sampleRate int) error {
	if handled, err := routeCloudPlayback(label, "wav", func() ([]byte, error) {
		var buf bytes.Buffer
		if err := writeWAVHeader(&buf, len(audioData), sampleRate, 1, 16); err != nil {
			return nil, fmt.Errorf("building WAV for GUI playback: %w", err)
		}
		buf.Write(audioData)
		return buf.Bytes(), nil
	}); handled {
		return err
	}
	stream := &PCMStream{data: audioData, sampleRate: beep.SampleRate(sampleRate)}
	if err := initSpeaker(stream.sampleRate); err != nil {
		return fmt.Errorf("failed to initialize speaker: %w", err)
	}
	if err := playInBackground(label, func(ctx context.Context) {
		log.Info("Speaking text", "label", label)
		playStreamer(ctx, resampleToSpeaker(stream, stream.sampleRate))
		log.Debug("Finished speaking", "label", label)
	}); err != nil {
		return err
	}
	return nil
}

// deliverAudio runs the save/play/result tail shared by the cloud TTS handlers,
// which each produce a full audio buffer. It saves synchronously when enabled
// (so the returned path is accurate and save errors surface), returns early in
// no-play mode, otherwise prepares playback synchronously via play (decode +
// speaker init errors surface to the caller) which then detaches the audio.
func deliverAudio(text string, audioData []byte, save func([]byte, string) (string, error), play func() error) (*mcp.CallToolResult, any, error) {
	var savedPath string
	if shouldSave() {
		var err error
		if savedPath, err = save(audioData, text); err != nil {
			log.Error("Failed to save audio file", "error", err)
			return errorResult(fmt.Sprintf("Error saving audio: %v", err)), nil, nil
		}
		log.Info("Audio saved", "path", savedPath)
	}
	if !shouldPlay() {
		return textResult(formatSaveResult(text, savedPath, false)), nil, nil
	}
	if err := play(); err != nil {
		log.Error("Failed to start playback", "error", err)
		return errorResult(fmt.Sprintf("Error: %v", err)), nil, nil
	}
	return textResult(formatSaveResult(text, savedPath, true)), nil, nil
}

// Helper functions for building tool results

func errorResult(msg string) *mcp.CallToolResult {
	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: msg}},
		IsError: true,
	}
}

func textResult(msg string) *mcp.CallToolResult {
	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: msg}},
	}
}

func sayCommandArgs(input SayTTSParams) ([]string, *mcp.CallToolResult) {
	rate := DefaultSayRate
	if input.Rate != nil {
		rate = *input.Rate
	}
	args := make([]string, 0, 4)
	args = append(args, "--rate", fmt.Sprintf("%d", rate))

	if input.Voice == nil || *input.Voice == "" {
		return args, nil
	}

	voice := *input.Voice
	for _, r := range voice {
		if !isAllowedSayVoiceRune(r) {
			return nil, errorResult(fmt.Sprintf("Error: Voice contains invalid characters: %s", voice))
		}
	}

	installed, err := IsVoiceInstalled(voice)
	if err != nil {
		log.Warn("Failed to check voice availability", "error", err, "voice", voice)
	} else if !installed {
		return nil, errorResult(VoiceNotInstalledError(voice))
	}

	args = append(args, "--voice", voice)
	return args, nil
}

func isAllowedSayVoiceRune(r rune) bool {
	return (r >= 'a' && r <= 'z') ||
		(r >= 'A' && r <= 'Z') ||
		(r >= '0' && r <= '9') ||
		r == ' ' ||
		r == '(' ||
		r == ')' ||
		r == '-' ||
		r == '_'
}

// Parameter types for tools with MCP schema descriptions for LLMs
type SayTTSParams struct {
	Text  string  `json:"text" mcp:"The text to speak aloud"`
	Rate  *int    `json:"rate,omitempty,omitzero" mcp:"Speech rate in words per minute (50-500, default: 200)"`
	Voice *string `json:"voice,omitempty,omitzero" mcp:"Voice to use for speech synthesis (e.g. 'Alex', 'Samantha', 'Victoria')"`
}

type ElevenLabsTTSParams struct {
	Text string `json:"text" mcp:"The text to convert to speech using ElevenLabs API"`
}

type SixtyDBTTSParams struct {
	Text string `json:"text" mcp:"The text to convert to speech using the 60dB API"`
}

type GoogleTTSParams struct {
	Text  string  `json:"text" mcp:"The text to convert to speech using Google TTS"`
	Voice *string `json:"voice,omitempty,omitzero" mcp:"Voice name to use (e.g. 'Kore', 'Puck', 'Fenrir', etc. - see documentation for full list of 30 voices, default: 'Kore')"`
	Model *string `json:"model,omitempty,omitzero" mcp:"TTS model to use (gemini-3.1-flash-tts-preview, gemini-2.5-flash-preview-tts, gemini-2.5-pro-preview-tts, gemini-2.5-flash-lite-preview-tts; default: 'gemini-3.1-flash-tts-preview')"`
}

type OpenAITTSParams struct {
	Text         string   `json:"text" mcp:"The text to convert to speech using OpenAI TTS"`
	Voice        *string  `json:"voice,omitempty,omitzero" mcp:"Voice to use (alloy, ash, ballad, coral, echo, fable, nova, onyx, sage, shimmer, verse; default: 'alloy')"`
	Model        *string  `json:"model,omitempty,omitzero" mcp:"TTS model to use (gpt-4o-mini-tts-2025-12-15, tts-1, tts-1-hd; default: 'gpt-4o-mini-tts-2025-12-15')"`
	Speed        *float64 `json:"speed,omitempty,omitzero" mcp:"Speech speed (0.25-4.0, default: 1.0)"`
	Instructions *string  `json:"instructions,omitempty,omitzero" mcp:"Instructions for voice modulation and style"`
}

type TTSParams struct {
	Text string `json:"text" mcp:"The text to speak aloud"`
}

func init() {
	// Override the default error level style.
	styles := log.DefaultStyles()
	styles.Levels[log.ErrorLevel] = lipgloss.NewStyle().
		SetString("ERROR").
		Padding(0, 1, 0, 1).
		Background(lipgloss.Color("204")).
		Foreground(lipgloss.Color("0"))
	// Add a custom style for key `err`
	styles.Keys["err"] = lipgloss.NewStyle().Foreground(lipgloss.Color("204"))
	styles.Values["err"] = lipgloss.NewStyle().Bold(true)
	logger = log.New(os.Stderr)
	logger.SetStyles(styles)

	// Define CLI flags
	rootCmd.PersistentFlags().BoolVarP(&verbose, "verbose", "v", false, "Enable verbose debug logging")
	rootCmd.PersistentFlags().BoolVar(&suppressSpeakingOutput, "suppress-speaking-output", false, "Suppress 'Speaking:' text output")
	rootCmd.PersistentFlags().BoolVar(&sequentialTTS, "sequential-tts", true, "Enforce sequential TTS (prevent concurrent speech)")
	rootCmd.PersistentFlags().BoolVar(&elicitEnabled, "elicit", false, "Enable interactive elicitation prompts (env: MCP_TTS_ELICIT)")
	rootCmd.PersistentFlags().StringVar(&outputDir, "output-dir", "", "Save audio files to directory (env: MCP_TTS_OUTPUT_DIR)")
	rootCmd.PersistentFlags().BoolVar(&noPlay, "no-play", false, "Skip playback, only save (requires --output-dir)")

	// Check environment variable for suppressing output
	if os.Getenv("MCP_TTS_SUPPRESS_SPEAKING_OUTPUT") == "true" {
		suppressSpeakingOutput = true
	}

	// Check environment variable for concurrent TTS
	if os.Getenv("MCP_TTS_ALLOW_CONCURRENT") == "true" {
		sequentialTTS = false
	}

	// Check environment variable for interactive elicitation
	if os.Getenv("MCP_TTS_ELICIT") == "true" {
		elicitEnabled = true
	}

	// Check environment variable for output directory
	if dir := os.Getenv("MCP_TTS_OUTPUT_DIR"); dir != "" && outputDir == "" {
		outputDir = dir
	}

	// Check environment variable for no-play mode
	if os.Getenv("MCP_TTS_NO_PLAY") == "true" {
		noPlay = true
	}
}

// rootCmd represents the base command when called without any subcommands
var rootCmd = &cobra.Command{
	Use:   "mcp-tts",
	Short: "TTS (text-to-speech) MCP Server",
	Long: `TTS (text-to-speech) MCP Server.

Provides multiple text-to-speech services via MCP protocol:

• say_tts - Uses macOS built-in 'say' command (macOS only)
• elevenlabs_tts - Uses ElevenLabs API for high-quality speech synthesis
• google_tts - Uses Google's Gemini TTS models for natural speech
• openai_tts - Uses OpenAI's TTS API with various voice options
• sixtydb_tts - Uses the 60dB API for high-quality speech synthesis

Each tool supports different voices, rates, and configuration options.
Requires appropriate API keys for cloud-based services.

Designed to be used with the MCP (Model Context Protocol).`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		if verbose {
			log.SetLevel(log.DebugLevel)
		}

		// serverCtx outlives individual tool requests. Audio playback is
		// detached onto it so a cancelled request cannot truncate speech;
		// it is cancelled only when the server itself shuts down.
		var serverCancel context.CancelFunc
		serverCtx, serverCancel = context.WithCancel(context.Background())
		defer serverCancel()

		// Bury GUI audio jobs left behind by crashed mcp-tts processes before we
		// start routing new ones. No-op outside a routable Background session.
		cleanupStaleGUIJobs()

		// Validate --no-play requires --output-dir
		if noPlay && outputDir == "" {
			return fmt.Errorf("--no-play requires --output-dir to be set")
		}

		// Validate output directory exists and is a directory
		if outputDir != "" {
			info, err := os.Stat(outputDir)
			if err != nil {
				if os.IsNotExist(err) {
					return fmt.Errorf("output directory does not exist: %s", outputDir)
				}
				return fmt.Errorf("failed to access output directory: %w", err)
			}
			if !info.IsDir() {
				return fmt.Errorf("output path is not a directory: %s", outputDir)
			}
			log.Debug("Audio saving enabled", "outputDir", outputDir, "noPlay", noPlay)
		}

		// Log sequential TTS status
		if sequentialTTS {
			log.Debug("Sequential TTS enabled - only one speech operation at a time")
		} else {
			log.Debug("Concurrent TTS enabled - multiple speech operations allowed simultaneously")
		}

		// Create a new MCP server with icon (v1.2.0 feature)
		// Service icons as base64-encoded SVG data URIs
		// Server icon: talking person with sound waves
		serverIcon := mcp.Icon{
			Source:   "data:image/svg+xml;base64,PHN2ZyB4bWxucz0iaHR0cDovL3d3dy53My5vcmcvMjAwMC9zdmciIHdpZHRoPSIyNCIgaGVpZ2h0PSIyNCIgdmlld0JveD0iMCAwIDI0IDI0IiBmaWxsPSJub25lIiBzdHJva2U9ImN1cnJlbnRDb2xvciIgc3Ryb2tlLXdpZHRoPSIyIiBzdHJva2UtbGluZWNhcD0icm91bmQiIHN0cm9rZS1saW5lam9pbj0icm91bmQiPjxjaXJjbGUgY3g9IjkiIGN5PSI3IiByPSI0Ii8+PHBhdGggZD0iTTMgMjF2LTJhNCA0IDAgMCAxIDQtNGg0YTQgNCAwIDAgMSA0IDR2MiIvPjxwYXRoIGQ9Ik0xNiAxMXMxIDEgMiAxIDItMSAyLTEiLz48cGF0aCBkPSJNMTkgOGMxLjUgMS41IDEuNSAzLjUgMCA1Ii8+PHBhdGggZD0iTTIxLjUgNS41YzMgMyAzIDcuNSAwIDEwLjUiLz48L3N2Zz4=",
			MIMEType: "image/svg+xml",
			Sizes:    []string{"24x24"},
		}
		// Apple logo for macOS say
		appleIcon := mcp.Icon{
			Source:   "data:image/svg+xml;base64,PHN2ZyB4bWxucz0iaHR0cDovL3d3dy53My5vcmcvMjAwMC9zdmciIHdpZHRoPSIyNCIgaGVpZ2h0PSIyNCIgdmlld0JveD0iMCAwIDI0IDI0Ij48cGF0aCBmaWxsPSJjdXJyZW50Q29sb3IiIGQ9Ik0xNy4wNSAyMC4yOGMtLjk4Ljk1LTIuMDUuOC0zLjA4LjM1LTEuMDktLjQ2LTIuMDktLjQ4LTMuMjQgMC0xLjQ0LjYyLTIuMi41NS0zLjA2LS4zNS0zLjEtMy4yMy0zLjcxLTEwLjIzIDIuMTgtMTAuMjMgMS40OC0uMDEgMi41Ljc4IDMuMzYuODMgMS40OS0uMDQgMi41My0uODMgMy42LS44MyAxLjEgMCAyLjA4LjgzIDMuNTguODMgMS4xNSAwIDIuNC0uNSAzLjM2LS44MyAxLjA0LS4wNSAyLjEuNDMgMi45NiAxLjI1LTIuNyAxLjYtMi4yNSA1LjYuNDcgNi43LS41NSAxLjUtMS4yNyAyLjk1LTIuMTMgMy40NXpNMTIuMDMgNy4yNWMtLjE1LTIuMjMgMS42Ni00LjA3IDMuNzQtNC4yNS4yOSAyLjU4LTIuMzQgNC41LTMuNzQgNC4yNXoiLz48L3N2Zz4=",
			MIMEType: "image/svg+xml",
			Sizes:    []string{"24x24"},
		}
		// OpenAI logo
		openaiIcon := mcp.Icon{
			Source:   "data:image/svg+xml;base64,PHN2ZyB4bWxucz0iaHR0cDovL3d3dy53My5vcmcvMjAwMC9zdmciIHdpZHRoPSIyNCIgaGVpZ2h0PSIyNCIgdmlld0JveD0iMCAwIDI0IDI0Ij48cGF0aCBmaWxsPSJjdXJyZW50Q29sb3IiIGQ9Ik0yMi40MTggOS44MjJhNS45MDQgNS45MDQgMCAwIDAtLjUyLTQuOTEgNi4xIDYuMSAwIDAgMC0yLjgyMi0yLjQ0IDYuMiA2LjIgMCAwIDAtMy43NjktLjQzNCA2IDYgMCAwIDAtMy40NjYtMS41MyA2LjE1IDYuMTUgMCAwIDAtMy4zMDYuNDcxQTYuMSA2LjEgMCAwIDAgNS45NzQgMy41MSA2IDYgMCAwIDAgMS45OTggNy4zMzdhNS45IDUuOSAwIDAgMCAuNzI0IDQuNTI0IDUuOSA1LjkgMCAwIDAgLjUyIDQuOTExIDYuMSA2LjEgMCAwIDAgMi44MjIgMi40NGE2LjIgNi4yIDAgMCAwIDMuNzY5LjQzNCA2LjA1IDYuMDUgMCAwIDAgMy4zNjUgMS41MyA2LjE1IDYuMTUgMCAwIDAgMy4zMDUtLjQ3IDYuMSA2LjEgMCAwIDAgMi41NjQtMi4wMDIgNiA2IDAgMCAwIDMuOTc2LTMuODI2IDUuOSA1LjkgMCAwIDAtLjcyNS00LjU1NnptLTkuMTQyIDEyLjAzYTQuNTcgNC41NyAwIDAgMS0yLjk3NS0xLjA5MmMuMDM4LS4wMjEuMTA0LS4wNTcuMTQ3LS4wODNsNC45MzktMi44NTRhLjguOCAwIDAgMCAuNDA2LS42OTRWMTAuMjZsMS4wNDUuNjAzYS4wNzUuMDc1IDAgMCAxIC4wNC4wNjd2NS43N2E0LjU5IDQuNTkgMCAwIDEtNC41OTUgNC41ODNsLTEuMDA3LS40M3pNMy44NzcgMTcuNjVhNC41NiA0LjU2IDAgMCAxLS41NDctMy4wNzZjLjAzNy4wMjMuMTAyLjA2LjE0OC4wODVsNC45MzggMi44NTJhLjgxNi44MTYgMCAwIDAgLjgxMiAwbDYuMDMtMy40ODJ2MS4yMDdhLjA3My4wNzMgMCAwIDEtLjAyOS4wNjJsLTQuOTkzIDIuODgzYTQuNiA0LjYgMCAwIDEtNi4zNTktMS41M1pNMi41MDYgNy44NmE0LjU2IDQuNTYgMCAwIDEgMi4zODItMi4wMDd2NS44NzNhLjc3Mi43NzIgMCAwIDAgLjQwNS42NzRsNi4wMyAzLjQ4MWwtMS4wNDcuNjA0YS4wNzUuMDc1IDAgMCAxLS4wNjkuMDA1bC00Ljk5NC0yLjg4NmE0LjU5IDQuNTkgMCAwIDEtMS43MDctNi4yNzR6bTE2LjU2MiAzLjg1NC02LjAzLTMuNDgzIDEuMDQ3LS42MDJhLjA3NS4wNzUgMCAwIDEgLjA2OS0uMDA1bDQuOTk0IDIuODgzYTQuNTcgNC41NyAwIDAgMS0uNzEyIDguMjU3di01Ljg3YS44LjggMCAwIDAtLjQwNS0uNzEzbC4wMzctLjQ2N3ptMS4wNDMtMy4wODVhNS44IDUuOCAwIDAgMC0uMTQ4LS4wODVsLTQuOTM4LTIuODUyYS44MTYuODE2IDAgMCAwLS44MTIgMGwtNi4wMyAzLjQ4MlY4LjA3YS4wNy4wNyAwIDAgMSAuMDI4LS4wNjJsNC45OTQtMi44ODRhNC41OSA0LjU5IDAgMCAxIDYuOTA2IDQuNzM2Wm0tNi41NCAzLjk1OC0xLjA0Ni0uNjAzYS4wNzQuMDc0IDAgMCAxLS4wNC0uMDY2VjYuMTQ4YTQuNjQgNC42NCAwIDAgMSA3LjU3LTMuNTQ2IDUuNiA1LjYgMCAwIDAtLjE0Ni4wODNsLTQuOTQgMi44NTRhLjguOCAwIDAgMC0uNDA1LjY5NHY1Ljg1bC4wMDctLjQ1em0uNTY4LTEuOTQgMi42ODYtMS41NTEgMi42ODYgMS41NXY3LjYzM2wtMi42ODYgMS41NTEtMi42ODYtMS41NTFWMTAuNjM3eiIvPjwvc3ZnPg==",
			MIMEType: "image/svg+xml",
			Sizes:    []string{"24x24"},
		}
		// Google/Gemini icon (sparkle/star shape)
		googleIcon := mcp.Icon{
			Source:   "data:image/svg+xml;base64,PHN2ZyB4bWxucz0iaHR0cDovL3d3dy53My5vcmcvMjAwMC9zdmciIHdpZHRoPSIyNCIgaGVpZ2h0PSIyNCIgdmlld0JveD0iMCAwIDI0IDI0Ij48cGF0aCBmaWxsPSIjNDI4NUY0IiBkPSJNMTIgMkM2LjQ4IDIgMiA2LjQ4IDIgMTJzNC40OCAxMCAxMCAxMCAxMC00LjQ4IDEwLTEwUzE3LjUyIDIgMTIgMnptNS40NiAxMy40NWwtMy4wOC0xLjc4Yy0uMy0uMTctLjY3LS4xNy0uOTcgMEwxMC4zMyAxNS40NWMtLjMuMTctLjY3LjE3LS45NyAwbC0zLjA4LTEuNzhhLjk3Ljk3IDAgMCAxLS40OC0uODRWOC4xN2MwLS4zNS4xOC0uNjcuNDgtLjg0bDMuMDgtMS43OGMuMy0uMTcuNjctLjE3Ljk3IDBsMy4wOCAxLjc4Yy4zLjE3LjQ4LjQ5LjQ4Ljg0djQuNjZjMCAuMzUtLjE4LjY3LS40OC44NHoiLz48L3N2Zz4=",
			MIMEType: "image/svg+xml",
			Sizes:    []string{"24x24"},
		}
		// ElevenLabs icon (stylized "XI" or wave pattern)
		elevenLabsIcon := mcp.Icon{
			Source:   "data:image/svg+xml;base64,PHN2ZyB4bWxucz0iaHR0cDovL3d3dy53My5vcmcvMjAwMC9zdmciIHdpZHRoPSIyNCIgaGVpZ2h0PSIyNCIgdmlld0JveD0iMCAwIDI0IDI0Ij48cGF0aCBmaWxsPSJjdXJyZW50Q29sb3IiIGQ9Ik03IDRoMnYxNkg3em04IDBoMnYxNmgtMnoiLz48L3N2Zz4=",
			MIMEType: "image/svg+xml",
			Sizes:    []string{"24x24"},
		}
		// 60dB icon (audio waveform bars)
		sixtyDBIcon := mcp.Icon{
			Source:   "data:image/svg+xml;base64,PHN2ZyB4bWxucz0iaHR0cDovL3d3dy53My5vcmcvMjAwMC9zdmciIHdpZHRoPSIyNCIgaGVpZ2h0PSIyNCIgdmlld0JveD0iMCAwIDI0IDI0IiBmaWxsPSJub25lIiBzdHJva2U9ImN1cnJlbnRDb2xvciIgc3Ryb2tlLXdpZHRoPSIyIiBzdHJva2UtbGluZWNhcD0icm91bmQiPjxwYXRoIGQ9Ik00IDEwdjRNOCA2djEyTTEyIDl2Nk0xNiA0djE2TTIwIDh2OCIvPjwvc3ZnPg==",
			MIMEType: "image/svg+xml",
			Sizes:    []string{"24x24"},
		}
		impl := &mcp.Implementation{
			Name:       "mcp-tts",
			Title:      "Text-to-Speech",
			Version:    Version,
			WebsiteURL: "https://github.com/blacktop/mcp-tts",
			Icons:      []mcp.Icon{serverIcon},
		}
		s := mcp.NewServer(impl, nil)

		// Prompt functionality removed - focusing on tools with new SDK

		if runtime.GOOS == "darwin" {
			// Add the "say_tts" tool with v1.2.0 features
			sayTool := &mcp.Tool{
				Name:        "say_tts",
				Title:       "macOS Say",
				Description: "Speaks the provided text out loud using the macOS text-to-speech engine",
				InputSchema: buildSayTTSSchema(),
				Icons:       []mcp.Icon{appleIcon},
				Annotations: &mcp.ToolAnnotations{
					Title:          "macOS Text-to-Speech",
					ReadOnlyHint:   false, // Produces audio output
					IdempotentHint: true,  // Same text = same speech
				},
			}

			// Add the say tool handler
			mcp.AddTool(s, sayTool, func(ctx context.Context, req *mcp.CallToolRequest, input SayTTSParams) (*mcp.CallToolResult, any, error) {
				select {
				case <-ctx.Done():
					return textResult("Request cancelled"), nil, nil
				default:
				}

				log.Debug("Say tool called", "params", input)

				text := input.Text
				if text == "" {
					return errorResult("Error: Empty text provided"), nil, nil
				}

				// Gather optional settings before taking the global speech lock so
				// other sessions are not blocked while the user decides.
				if input.Voice == nil && input.Rate == nil {
					content, result, stop := maybeElicitContent(
						ctx,
						req,
						"elicit macOS Say settings",
						"Configure macOS Say settings (or accept defaults):",
						saySettingsSchema(),
					)
					if stop {
						return result, nil, nil
					}
					applySaySettings(&input, content)
				}

				// Build base args (rate + optional voice). Validation happens
				// synchronously so the caller sees argument errors immediately.
				args, validationResult := sayCommandArgs(input)
				if validationResult != nil {
					return validationResult, nil, nil
				}

				// Log potentially dangerous shell metacharacters (exec.Command is safe, but log for awareness)
				dangerousChars := []rune{';', '&', '|', '<', '>', '`', '$', '(', ')', '{', '}', '[', ']', '\\', '\'', '"', '\n', '\r'}
				textBytes := []byte(text)
				for _, char := range dangerousChars {
					if bytes.ContainsRune(textBytes, char) {
						log.Warn("Potentially dangerous character in text input", "char", string(char), "text", text)
					}
				}

				// Detect the launchd audio session once. A Background session has
				// no route to GUI audio, so route through gui/<uid> when possible
				// and fail loudly (never silently) when no GUI session exists.
				mode, managerName := sessionModeFn()
				if mode == sessionHeadless {
					return errorResult(headlessAudioError(managerName)), nil, nil
				}

				// Save-only mode: write the file synchronously so we can report
				// the exact path. say -o writes faster than real time and does
				// not play, so there is nothing to truncate.
				if !shouldPlay() {
					filename := generateFilename(text, "aiff")
					savedPath := filepath.Join(outputDir, filename)
					if mode == sessionRoutable {
						if err := guiSayPlay(ctx, args, text, savedPath, false); err != nil {
							if ctx.Err() != nil {
								return textResult("Say command cancelled"), nil, nil
							}
							return errorResult(fmt.Sprintf("Error: Say command failed: %v", err)), nil, nil
						}
						return textResult(formatSaveResult(text, savedPath, false)), nil, nil
					}
					saveArgs := append(append([]string{}, args...), "-o", savedPath, text)
					log.Debug("Saving say audio to file", "path", savedPath, "args", saveArgs)
					if err := exec.CommandContext(ctx, "/usr/bin/say", saveArgs...).Run(); err != nil {
						if ctx.Err() != nil {
							return textResult("Say command cancelled"), nil, nil
						}
						log.Error("Say command failed", "error", err)
						return errorResult(fmt.Sprintf("Error: Say command failed: %v", err)), nil, nil
					}
					return textResult(formatSaveResult(text, savedPath, false)), nil, nil
				}

				// Play mode: acquire the speech lock and start `say` before
				// returning so start/lock failures surface to the caller. The
				// process is then waited in the background so request
				// cancellation cannot truncate speech.
				var savedPath string
				if shouldSave() {
					filename := generateFilename(text, "aiff")
					savedPath = filepath.Join(outputDir, filename)
				}

				if mode == sessionRoutable {
					if err := startRoutableSayPlayback(args, text, savedPath); err != nil {
						log.Error("Failed to start GUI-routed say playback", "error", err)
						return errorResult(fmt.Sprintf("Error: %v", err)), nil, nil
					}
					return textResult(formatSaveResult(text, savedPath, true)), nil, nil
				}

				playArgs := append([]string{}, args...)
				if savedPath != "" {
					playArgs = append(playArgs, "-o", savedPath)
				}
				playArgs = append(playArgs, text)

				if err := startSayPlayback(playArgs, savedPath, text); err != nil {
					log.Error("Failed to start say playback", "error", err)
					return errorResult(fmt.Sprintf("Error: %v", err)), nil, nil
				}

				return textResult(formatSaveResult(text, savedPath, true)), nil, nil
			})
		}

		elevenLabsTool := &mcp.Tool{
			Name:        "elevenlabs_tts",
			Title:       "ElevenLabs",
			Description: "Uses the ElevenLabs API to generate speech from text",
			InputSchema: buildElevenLabsTTSSchema(),
			Icons:       []mcp.Icon{elevenLabsIcon},
			Annotations: &mcp.ToolAnnotations{
				Title:          "ElevenLabs Text-to-Speech",
				ReadOnlyHint:   false,
				IdempotentHint: true,
			},
		}

		mcp.AddTool(s, elevenLabsTool, func(ctx context.Context, _ *mcp.CallToolRequest, input ElevenLabsTTSParams) (*mcp.CallToolResult, any, error) {
			select {
			case <-ctx.Done():
				return textResult("Request cancelled"), nil, nil
			default:
			}

			log.Debug("ElevenLabs tool called", "params", input)
			text := input.Text
			if text == "" {
				return errorResult("Error: text must be a string"), nil, nil
			}

			voiceID := os.Getenv("ELEVENLABS_VOICE_ID")
			if voiceID == "" {
				voiceID = DefaultElevenLabsVoiceID
				log.Debug("Voice not specified, using default", "voiceID", voiceID)
			}

			modelID := os.Getenv("ELEVENLABS_MODEL_ID")
			if modelID == "" {
				modelID = DefaultElevenLabsModel
				log.Debug("Model not specified, using default", "modelID", modelID)
			}

			apiKey := os.Getenv("ELEVENLABS_API_KEY")
			if apiKey == "" {
				log.Error("ELEVENLABS_API_KEY not set")
				return errorResult("Error: ELEVENLABS_API_KEY is not set"), nil, nil
			}

			// Fetch the audio synchronously so HTTP/API errors surface to the
			// caller, then hand playback off to the background.
			url := fmt.Sprintf("https://api.elevenlabs.io/v1/text-to-speech/%s/stream", voiceID)
			params := ElevenLabsParams{
				Text:    text,
				ModelID: modelID,
				VoiceSettings: SynthesisOptions{
					Stability:       0.5, // Must be 0.0 (Creative), 0.5 (Natural), or 1.0 (Robust)
					SimilarityBoost: 0.75,
					Style:           0.5,
					UseSpeakerBoost: false,
				},
			}

			b, err := json.Marshal(params)
			if err != nil {
				log.Error("Failed to marshal request body", "error", err)
				return errorResult(fmt.Sprintf("Error: failed to marshal request body: %v", err)), nil, nil
			}

			log.Debug("Making ElevenLabs API request", "url", url, "voice", voiceID, "model", modelID, "text", text)
			httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewBuffer(b))
			if err != nil {
				log.Error("Failed to create request", "error", err)
				return errorResult(fmt.Sprintf("Error: failed to create request: %v", err)), nil, nil
			}
			httpReq.Header.Set("xi-api-key", apiKey)
			httpReq.Header.Set("Content-Type", "application/json")
			httpReq.Header.Set("accept", "audio/mpeg")

			safeLog("Sending HTTP request", httpReq)
			res, err := http.DefaultClient.Do(httpReq)
			if err != nil {
				if ctx.Err() != nil {
					return textResult("Request cancelled"), nil, nil
				}
				log.Error("Failed to send request", "error", err)
				return errorResult(fmt.Sprintf("Error: failed to send request: %v", err)), nil, nil
			}
			defer res.Body.Close()

			if res.StatusCode != http.StatusOK {
				body, _ := io.ReadAll(res.Body)
				log.Error("ElevenLabs request failed", "status", res.Status, "body", string(body))
				if len(body) > 0 {
					return errorResult(fmt.Sprintf("Error: ElevenLabs API error (status %d): %s", res.StatusCode, string(body))), nil, nil
				}
				return errorResult(fmt.Sprintf("Error: ElevenLabs API error: status %d %s", res.StatusCode, res.Status)), nil, nil
			}

			audioData, err := io.ReadAll(res.Body)
			if err != nil {
				if ctx.Err() != nil {
					return textResult("Request cancelled"), nil, nil
				}
				log.Error("Failed to read ElevenLabs response", "error", err)
				return errorResult(fmt.Sprintf("Error: failed to read response: %v", err)), nil, nil
			}
			log.Debug("ElevenLabs audio received", "bytes", len(audioData))

			return deliverAudio(text, audioData, saveMP3, func() error {
				return playMP3("elevenlabs_tts", audioData)
			})
		})

		// Add the "sixtydb_tts" tool
		sixtyDBTool := &mcp.Tool{
			Name:        "sixtydb_tts",
			Title:       "60dB",
			Description: "Uses the 60dB API to generate speech from text",
			InputSchema: buildSixtyDBTTSSchema(),
			Icons:       []mcp.Icon{sixtyDBIcon},
			Annotations: &mcp.ToolAnnotations{
				Title:          "60dB Text-to-Speech",
				ReadOnlyHint:   false,
				IdempotentHint: true,
			},
		}

		mcp.AddTool(s, sixtyDBTool, func(ctx context.Context, _ *mcp.CallToolRequest, input SixtyDBTTSParams) (*mcp.CallToolResult, any, error) {
			select {
			case <-ctx.Done():
				return textResult("Request cancelled"), nil, nil
			default:
			}

			log.Debug("60dB tool called", "params", input)
			text := input.Text
			if text == "" {
				return errorResult("Error: text must be a string"), nil, nil
			}

			apiKey := os.Getenv("SIXTYDB_API_KEY")
			if apiKey == "" {
				log.Error("SIXTYDB_API_KEY not set")
				return errorResult("Error: SIXTYDB_API_KEY is not set"), nil, nil
			}

			// voice_id is optional: when unset, 60dB uses the account's default
			// voice. The model is selected via the voice, so there is no model_id.
			voiceID := os.Getenv("SIXTYDB_VOICE_ID")
			if voiceID == "" {
				log.Debug("Voice not specified, using 60dB account default")
			}

			// Fetch the audio synchronously so HTTP/API errors surface to the
			// caller, then hand playback off to the background.
			const url = "https://api.60db.ai/tts-synthesize"
			params := SixtyDBParams{
				Text:         text,
				VoiceID:      voiceID,
				Speed:        DefaultSixtyDBSpeed,      // 0.5-2.0
				Stability:    DefaultSixtyDBStability,  // 0-100; lower = more expressive
				Similarity:   DefaultSixtyDBSimilarity, // 0-100; voice matching level
				Enhance:      true,
				OutputFormat: DefaultSixtyDBOutputFormat, // mp3, routed through playMP3/saveMP3
			}

			b, err := json.Marshal(params)
			if err != nil {
				log.Error("Failed to marshal request body", "error", err)
				return errorResult(fmt.Sprintf("Error: failed to marshal request body: %v", err)), nil, nil
			}

			log.Debug("Making 60dB API request", "url", url, "voice", voiceID, "text", text)
			httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewBuffer(b))
			if err != nil {
				log.Error("Failed to create request", "error", err)
				return errorResult(fmt.Sprintf("Error: failed to create request: %v", err)), nil, nil
			}
			httpReq.Header.Set("Authorization", "Bearer "+apiKey)
			httpReq.Header.Set("Content-Type", "application/json")
			httpReq.Header.Set("accept", "application/json")

			safeLog("Sending HTTP request", httpReq)
			res, err := http.DefaultClient.Do(httpReq)
			if err != nil {
				if ctx.Err() != nil {
					return textResult("Request cancelled"), nil, nil
				}
				log.Error("Failed to send request", "error", err)
				return errorResult(fmt.Sprintf("Error: failed to send request: %v", err)), nil, nil
			}
			defer res.Body.Close()

			body, err := io.ReadAll(res.Body)
			if err != nil {
				if ctx.Err() != nil {
					return textResult("Request cancelled"), nil, nil
				}
				log.Error("Failed to read 60dB response", "error", err)
				return errorResult(fmt.Sprintf("Error: failed to read response: %v", err)), nil, nil
			}

			if res.StatusCode != http.StatusOK {
				log.Error("60dB request failed", "status", res.Status, "body", string(body))
				if len(body) > 0 {
					return errorResult(fmt.Sprintf("Error: 60dB API error (status %d): %s", res.StatusCode, string(body))), nil, nil
				}
				return errorResult(fmt.Sprintf("Error: 60dB API error: status %d %s", res.StatusCode, res.Status)), nil, nil
			}

			var parsed SixtyDBResponse
			if err := json.Unmarshal(body, &parsed); err != nil {
				log.Error("Failed to decode 60dB response", "error", err)
				return errorResult(fmt.Sprintf("Error: failed to decode 60dB response: %v", err)), nil, nil
			}
			if !parsed.Success {
				msg := parsed.Message
				if msg == "" {
					msg = "request was not successful"
				}
				log.Error("60dB synthesis failed", "message", msg)
				return errorResult(fmt.Sprintf("Error: 60dB API error: %s", msg)), nil, nil
			}
			if parsed.AudioBase64 == "" {
				log.Error("60dB response contained no audio")
				return errorResult("Error: 60dB API returned no audio"), nil, nil
			}

			audioData, err := base64.StdEncoding.DecodeString(parsed.AudioBase64)
			if err != nil {
				log.Error("Failed to decode 60dB audio", "error", err)
				return errorResult(fmt.Sprintf("Error: failed to decode 60dB audio: %v", err)), nil, nil
			}
			log.Debug("60dB audio received", "bytes", len(audioData))

			return deliverAudio(text, audioData, saveMP3, func() error {
				return playMP3("sixtydb_tts", audioData)
			})
		})

		// Add Google TTS tool
		googleTTSTool := &mcp.Tool{
			Name:        "google_tts",
			Title:       "Google Gemini",
			Description: "Uses Google's dedicated Text-to-Speech API with Gemini TTS models",
			InputSchema: buildGoogleTTSSchema(),
			Icons:       []mcp.Icon{googleIcon},
			Annotations: &mcp.ToolAnnotations{
				Title:          "Google Gemini Text-to-Speech",
				ReadOnlyHint:   false,
				IdempotentHint: true,
			},
		}

		mcp.AddTool(s, googleTTSTool, func(ctx context.Context, req *mcp.CallToolRequest, input GoogleTTSParams) (*mcp.CallToolResult, any, error) {
			select {
			case <-ctx.Done():
				return textResult("Request cancelled"), nil, nil
			default:
			}

			log.Debug("Google TTS tool called", "params", input)
			text := input.Text
			if text == "" {
				return errorResult("Error: Empty text provided"), nil, nil
			}

			// Gather optional settings before taking the global speech lock so
			// other sessions are not blocked while the user decides.
			if input.Voice == nil && input.Model == nil {
				content, result, stop := maybeElicitContent(
					ctx,
					req,
					"elicit Google TTS settings",
					"Configure Google TTS settings (or accept defaults):",
					googleSettingsSchema(),
				)
				if stop {
					return result, nil, nil
				}
				applyGoogleSettings(&input, content)
			}

			voice := DefaultGoogleVoice
			if input.Voice != nil && *input.Voice != "" {
				voice = *input.Voice
			}

			model := DefaultGoogleModel
			if input.Model != nil && *input.Model != "" {
				model = *input.Model
			}

			apiKey := os.Getenv("GOOGLE_AI_API_KEY")
			if apiKey == "" {
				apiKey = os.Getenv("GEMINI_API_KEY")
			}
			if apiKey == "" {
				log.Error("GOOGLE_AI_API_KEY or GEMINI_API_KEY not set")
				return errorResult("Error: GOOGLE_AI_API_KEY or GEMINI_API_KEY is not set"), nil, nil
			}

			client, err := genai.NewClient(ctx, &genai.ClientConfig{
				APIKey:  apiKey,
				Backend: genai.BackendGeminiAPI,
			})
			if err != nil {
				log.Error("Failed to create Google AI client", "error", err)
				return errorResult(fmt.Sprintf("Error: Failed to create client: %v", err)), nil, nil
			}

			log.Debug("Generating TTS audio",
				"model", model,
				"voice", voice,
				"text", text,
			)

			// Generate TTS audio using the dedicated TTS models
			content := []*genai.Content{
				genai.NewContentFromText(text, genai.RoleUser),
			}

			response, err := client.Models.GenerateContent(ctx, model, content, &genai.GenerateContentConfig{
				ResponseModalities: []string{"AUDIO"},
				SpeechConfig: &genai.SpeechConfig{
					VoiceConfig: &genai.VoiceConfig{
						PrebuiltVoiceConfig: &genai.PrebuiltVoiceConfig{
							VoiceName: voice,
						},
					},
				},
			})
			if err != nil {
				log.Error("Failed to generate TTS audio", "error", err)
				return errorResult(fmt.Sprintf("Error: Failed to generate TTS audio: %v", err)), nil, nil
			}

			if len(response.Candidates) == 0 || response.Candidates[0].Content == nil || len(response.Candidates[0].Content.Parts) == 0 {
				log.Error("No audio data in TTS response")
				return errorResult("Error: No audio data received from Google TTS"), nil, nil
			}

			part := response.Candidates[0].Content.Parts[0]
			if part.InlineData == nil {
				log.Error("No inline data in TTS response")
				return errorResult("Error: No audio data received from Google TTS"), nil, nil
			}

			audioData := part.InlineData.Data
			log.Debug("Google TTS audio received", "bytes", len(audioData))

			const googleTTSSampleRate = 24000

			return deliverAudio(text, audioData,
				func(d []byte, t string) (string, error) { return saveWAV(d, googleTTSSampleRate, t) },
				func() error { return playPCM("google_tts", audioData, googleTTSSampleRate) },
			)
		})

		// Add OpenAI TTS tool
		openaiTTSTool := &mcp.Tool{
			Name:        "openai_tts",
			Title:       "OpenAI",
			Description: "Uses OpenAI's Text-to-Speech API to generate speech from text",
			InputSchema: buildOpenAITTSSchema(),
			Icons:       []mcp.Icon{openaiIcon},
			Annotations: &mcp.ToolAnnotations{
				Title:          "OpenAI Text-to-Speech",
				ReadOnlyHint:   false,
				IdempotentHint: true,
			},
		}

		mcp.AddTool(s, openaiTTSTool, func(ctx context.Context, req *mcp.CallToolRequest, input OpenAITTSParams) (*mcp.CallToolResult, any, error) {
			select {
			case <-ctx.Done():
				return textResult("Request cancelled"), nil, nil
			default:
			}

			log.Debug("OpenAI TTS tool called", "params", input)
			text := input.Text
			if text == "" {
				return errorResult("Error: Empty text provided"), nil, nil
			}

			// Gather optional settings before taking the global speech lock so
			// other sessions are not blocked while the user decides.
			if input.Voice == nil && input.Model == nil && input.Speed == nil {
				content, result, stop := maybeElicitContent(
					ctx,
					req,
					"elicit OpenAI TTS settings",
					"Configure OpenAI TTS settings (or accept defaults):",
					openAISettingsSchema(),
				)
				if stop {
					return result, nil, nil
				}
				applyOpenAISettings(&input, content)
			}

			voice := DefaultOpenAIVoice
			if input.Voice != nil && *input.Voice != "" {
				voice = *input.Voice
			}

			model := DefaultOpenAIModel
			if input.Model != nil && *input.Model != "" {
				model = *input.Model
			}

			speed := DefaultOpenAISpeed
			if input.Speed != nil {
				if *input.Speed >= 0.25 && *input.Speed <= 4.0 {
					speed = *input.Speed
				} else {
					log.Warn("Speed out of range, using default", "provided", *input.Speed, "default", 1.0)
				}
			}

			instructions := ""
			if input.Instructions != nil && *input.Instructions != "" {
				instructions = *input.Instructions
			} else {
				instructions = os.Getenv("OPENAI_TTS_INSTRUCTIONS")
			}

			if len(instructions) > 1000 {
				log.Warn("Instructions are very long, may exceed API limits", "length", len(instructions))
			}

			apiKey := os.Getenv("OPENAI_API_KEY")
			if apiKey == "" {
				log.Error("OPENAI_API_KEY not set")
				return errorResult("Error: OPENAI_API_KEY is not set"), nil, nil
			}

			client := openai.NewClient(option.WithAPIKey(apiKey))

			logFields := []any{"model", model, "voice", voice, "speed", speed, "text", text}
			if instructions != "" {
				logFields = append(logFields, "instructions", instructions)
			}
			log.Debug("Generating OpenAI TTS audio", logFields...)

			reqParams := openai.AudioSpeechNewParams{
				Model: openai.SpeechModel(model),
				Input: text,
				Voice: openai.AudioSpeechNewParamsVoice(voice),
			}
			if speed != 1.0 {
				reqParams.Speed = openai.Float(speed)
			}
			if instructions != "" {
				reqParams.Instructions = openai.String(instructions)
			}

			response, err := client.Audio.Speech.New(ctx, reqParams)
			if err != nil {
				log.Error("Failed to generate OpenAI TTS audio", "error", err)
				return errorResult(fmt.Sprintf("Error: Failed to generate TTS audio: %v", err)), nil, nil
			}
			defer response.Body.Close()

			// Buffer the response body so we can both save and decode it
			audioData, err := io.ReadAll(response.Body)
			if err != nil {
				log.Error("Failed to read OpenAI TTS response", "error", err)
				return errorResult(fmt.Sprintf("Error: Failed to read response: %v", err)), nil, nil
			}
			log.Debug("OpenAI TTS audio data received", "bytes", len(audioData))

			return deliverAudio(text, audioData, saveMP3, func() error {
				return playMP3("openai_tts", audioData)
			})
		})

		// Add interactive TTS tool that uses elicitation to choose provider
		ttsTool := &mcp.Tool{
			Name:  "tts",
			Title: "Interactive TTS",
			Description: "Selects a TTS provider and voice settings interactively, " +
				"then returns a recommendation to call the chosen provider tool.",
			InputSchema: buildTTSSchema(),
			Annotations: &mcp.ToolAnnotations{
				Title:          "Interactive Text-to-Speech",
				ReadOnlyHint:   false,
				IdempotentHint: false,
			},
		}
		mcp.AddTool(s, ttsTool, func(
			ctx context.Context,
			req *mcp.CallToolRequest,
			input TTSParams,
		) (*mcp.CallToolResult, any, error) {
			text := input.Text
			if text == "" {
				return errorResult("Error: Empty text provided"), nil, nil
			}

			providers := availableProviders()
			if len(providers) == 0 {
				return errorResult("Error: No TTS providers configured"), nil, nil
			}

			if !canElicit(req) {
				p := providers[0]
				return textResult(buildProviderRecommendation(
					p.ID, p.Name, providerRecommendationArgs(p.ID, text, nil),
				)), nil, nil
			}

			provider := providers[0]
			if len(providers) > 1 {
				selection := elicitForm(ctx, req.Session,
					"Which TTS provider would you like to use?",
					providerSelectionSchema(providers),
				)
				if result, stop := elicitationStopResult(selection, "elicit TTS provider selection"); stop {
					return result, nil, nil
				}
				var cancelled bool
				var err error
				provider, cancelled, err = chooseProvider(providers, selection)
				if err != nil {
					return errorResult(fmt.Sprintf("Error: %v", err)), nil, nil
				}
				if cancelled {
					return textResult("Request cancelled"), nil, nil
				}
			}

			var settingsContent map[string]any
			if settingsSchema := settingsSchemaForProvider(provider.ID); settingsSchema != nil {
				content, result, stop := maybeElicitContent(
					ctx,
					req,
					"elicit TTS voice settings",
					"Configure voice settings (or accept defaults):",
					settingsSchema,
				)
				if stop {
					return result, nil, nil
				}
				settingsContent = content
			}

			return textResult(buildProviderRecommendation(
				provider.ID,
				provider.Name,
				providerRecommendationArgs(provider.ID, text, settingsContent),
			)), nil, nil
		})

		log.Info("Starting MCP server", "name", "mcp-tts", "version", Version)
		// Start the server using stdin/stdout. serverCtx (created above) is
		// cancelled on shutdown, which also stops any in-flight background
		// playback.
		if err := ctrlc.Default.Run(serverCtx, func() error {
			runErr := s.Run(serverCtx, &mcp.StdioTransport{})
			// Transport closed (one-shot pipe or client disconnect): let any
			// detached playback finish before exiting so audio isn't cut off.
			// Ctrl-C cancels serverCtx, which stops playback and drains this.
			log.Debug("Transport closed; waiting for in-flight playback")
			playbackWG.Wait()
			// A closed stdio transport (EOF) is the normal end of a session,
			// not a failure — only surface genuine serve errors.
			if runErr != nil && !errors.Is(runErr, io.EOF) {
				return fmt.Errorf("failed to serve MCP: %v", runErr)
			}
			return nil
		}); err != nil {
			if errors.As(err, &ctrlc.ErrorCtrlC{}) {
				log.Warn("Exiting...")
				os.Exit(0)
			} else {
				return fmt.Errorf("failed while serving MCP: %v", err)
			}
		}
		return nil
	},
}

func safeLog(message string, req *http.Request) {
	reqCopy := req.Clone(context.Background())
	if _, exists := reqCopy.Header["Xi-Api-Key"]; exists {
		reqCopy.Header["Xi-Api-Key"] = []string{"******"} // Mask password
	}
	if _, exists := reqCopy.Header["Authorization"]; exists {
		reqCopy.Header["Authorization"] = []string{"******"} // Mask bearer token
	}
	log.With(reqCopy).Debug(message)
}

// Execute adds all child commands to the root command and sets flags appropriately.
// This is called by main.main(). It only needs to happen once to the rootCmd.
func Execute() {
	if err := rootCmd.Execute(); err != nil {
		log.Fatal("Failed to execute command", "error", err)
	}
}
