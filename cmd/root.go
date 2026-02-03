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
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
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
	"golang.org/x/sync/errgroup"
	"google.golang.org/genai"
)

var (
	verbose bool
	logger  *log.Logger
	// Version stores the service's version
	Version string
	// Flag to suppress "Speaking:" output
	suppressSpeakingOutput bool
	// Global TTS mutex to prevent concurrent speech
	ttsMutex sync.Mutex
	// Flag to enable/disable sequential TTS (default: true)
	sequentialTTS bool = true
)

// acquireTTSLock attempts to acquire the TTS mutex with context support
// Returns a release function that should be deferred
func acquireTTSLock(ctx context.Context) (release func(), err error) {
	if !sequentialTTS {
		// If concurrent TTS is allowed, return a no-op release function
		return func() {}, nil
	}

	// Acquire local process mutex first to prevent deadlocks
	log.Debug("Attempting to acquire local TTS mutex", "pid", os.Getpid())
	acquired := make(chan struct{})

	go func() {
		ttsMutex.Lock()
		log.Debug("Local TTS mutex acquired", "pid", os.Getpid())
		close(acquired)
	}()

	// Wait for local mutex or context cancellation
	select {
	case <-acquired:
		// Got local mutex, now try global system lock
		log.Debug("Attempting to acquire global TTS lock", "pid", os.Getpid())
		globalRelease, err := acquireGlobalTTSLock(ctx)
		if err != nil {
			// Failed to get global lock, release local mutex
			log.Debug("Failed to acquire global lock, releasing local mutex", "pid", os.Getpid(), "error", err)
			ttsMutex.Unlock()
			return nil, err
		}

		log.Debug("Both TTS locks acquired successfully", "pid", os.Getpid())
		// We got both locks - return combined release function (global first, then local)
		return func() {
			log.Debug("Releasing both TTS locks", "pid", os.Getpid())
			globalRelease()
			ttsMutex.Unlock()
			log.Debug("Both TTS locks released", "pid", os.Getpid())
		}, nil
	case <-ctx.Done():
		// Context was cancelled while waiting for local mutex
		// Check if we got the local lock after cancellation
		select {
		case <-acquired:
			ttsMutex.Unlock()
		default:
		}
		return nil, ctx.Err()
	}
}

// progressReporter sends progress notifications to the client during audio playback
type progressReporter struct {
	session       *mcp.ServerSession
	progressToken any
	total         int
	sampleRate    int
	lastPercent   int
	ctx           context.Context
	cancel        context.CancelFunc
	done          chan struct{}
}

// newProgressReporter creates a progress reporter if the client provided a progress token
func newProgressReporter(ctx context.Context, req *mcp.CallToolRequest, total int, sampleRate int) *progressReporter {
	if req == nil || req.Session == nil {
		return nil
	}
	token := req.Params.GetProgressToken()
	if token == nil {
		return nil
	}
	prCtx, cancel := context.WithCancel(ctx)
	return &progressReporter{
		session:       req.Session,
		progressToken: token,
		total:         total,
		sampleRate:    sampleRate,
		lastPercent:   -1,
		ctx:           prCtx,
		cancel:        cancel,
		done:          make(chan struct{}),
	}
}

// start begins polling the position function and sending progress updates
func (pr *progressReporter) start(getPosition func() int) {
	if pr == nil {
		return
	}
	go func() {
		defer close(pr.done)
		ticker := time.NewTicker(250 * time.Millisecond)
		defer ticker.Stop()

		for {
			select {
			case <-pr.ctx.Done():
				return
			case <-ticker.C:
				pos := getPosition()
				percent := 0
				if pr.total > 0 {
					percent = (pos * 100) / pr.total
				}
				// Only send updates when percent changes (reduces noise)
				if percent != pr.lastPercent {
					pr.lastPercent = percent
					durationSec := float64(pos) / float64(pr.sampleRate)
					totalSec := float64(pr.total) / float64(pr.sampleRate)
					msg := fmt.Sprintf("Playing: %.1fs / %.1fs", durationSec, totalSec)
					if err := pr.session.NotifyProgress(pr.ctx, &mcp.ProgressNotificationParams{
						ProgressToken: pr.progressToken,
						Progress:      float64(pos),
						Total:         float64(pr.total),
						Message:       msg,
					}); err != nil {
						log.Debug("Failed to send progress notification", "error", err)
					}
				}
			}
		}
	}()
}

// stop terminates the progress reporter
func (pr *progressReporter) stop() {
	if pr == nil {
		return
	}
	pr.cancel()
	<-pr.done
}

// Parameter types for tools with MCP schema descriptions for LLMs
type SayTTSParams struct {
	Text  string  `json:"text" mcp:"The text to speak aloud"`
	Rate  *int    `json:"rate,omitempty" mcp:"Speech rate in words per minute (50-500, default: 200)"`
	Voice *string `json:"voice,omitempty" mcp:"Voice to use for speech synthesis (e.g. 'Alex', 'Samantha', 'Victoria')"`
}

type ElevenLabsTTSParams struct {
	Text string `json:"text" mcp:"The text to convert to speech using ElevenLabs API"`
}

type GoogleTTSParams struct {
	Text  string  `json:"text" mcp:"The text to convert to speech using Google TTS"`
	Voice *string `json:"voice,omitempty" mcp:"Voice name to use (e.g. 'Kore', 'Puck', 'Fenrir', etc. - see documentation for full list of 30 voices, default: 'Zephyr')"`
	Model *string `json:"model,omitempty" mcp:"TTS model to use (gemini-2.5-flash-preview-tts, gemini-2.5-pro-preview-tts, gemini-2.5-flash-lite-preview-tts; default: 'gemini-2.5-flash-preview-tts')"`
}

type OpenAITTSParams struct {
	Text         string   `json:"text" mcp:"The text to convert to speech using OpenAI TTS"`
	Voice        *string  `json:"voice,omitempty" mcp:"Voice to use (alloy, ash, ballad, coral, echo, fable, nova, onyx, sage, shimmer, verse; default: 'alloy')"`
	Model        *string  `json:"model,omitempty" mcp:"TTS model to use (gpt-4o-mini-tts-2025-12-15, gpt-4o-mini-tts, gpt-4o-audio-preview, tts-1, tts-1-hd; default: 'gpt-4o-mini-tts-2025-12-15')"`
	Speed        *float64 `json:"speed,omitempty" mcp:"Speech speed (0.25-4.0, default: 1.0)"`
	Instructions *string  `json:"instructions,omitempty" mcp:"Instructions for voice modulation and style"`
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

	// Check environment variable for suppressing output
	if os.Getenv("MCP_TTS_SUPPRESS_SPEAKING_OUTPUT") == "true" {
		suppressSpeakingOutput = true
	}

	// Check environment variable for concurrent TTS
	if os.Getenv("MCP_TTS_ALLOW_CONCURRENT") == "true" {
		sequentialTTS = false
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

Each tool supports different voices, rates, and configuration options.
Requires appropriate API keys for cloud-based services.

Designed to be used with the MCP (Model Context Protocol).`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		if verbose {
			log.SetLevel(log.DebugLevel)
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
			mcp.AddTool(s, sayTool, func(ctx context.Context, _ *mcp.CallToolRequest, input SayTTSParams) (*mcp.CallToolResult, any, error) {
				// Check for early cancellation
				select {
				case <-ctx.Done():
					return &mcp.CallToolResult{
						Content: []mcp.Content{&mcp.TextContent{Text: "Request cancelled"}},
					}, nil, nil
				default:
				}

				// Acquire TTS lock
				release, err := acquireTTSLock(ctx)
				if err != nil {
					log.Info("Request cancelled while waiting for TTS lock")
					return &mcp.CallToolResult{
						Content: []mcp.Content{&mcp.TextContent{Text: "Request cancelled while waiting for TTS"}},
					}, nil, nil
				}
				defer release()

				log.Debug("Say tool called", "params", input)

				text := input.Text
				if text == "" {
					return &mcp.CallToolResult{
						Content: []mcp.Content{&mcp.TextContent{Text: "Error: Empty text provided"}},
						IsError: true,
					}, nil, nil
				}

				args := []string{}

				// Add rate if provided
				if input.Rate != nil {
					args = append(args, "--rate", fmt.Sprintf("%d", *input.Rate))
				} else {
					args = append(args, "--rate", "200") // Default rate
				}

				// Add voice if provided and validate it
				if input.Voice != nil && *input.Voice != "" {
					voice := *input.Voice
					// Simple validation to prevent command injection
					// Allow alphanumeric characters, spaces, hyphens, underscores, and parentheses
					for _, r := range voice {
						if !((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') ||
							r == ' ' || r == '(' || r == ')' || r == '-' || r == '_') {
							return &mcp.CallToolResult{
								Content: []mcp.Content{&mcp.TextContent{Text: fmt.Sprintf("Error: Voice contains invalid characters: %s", voice)}},
								IsError: true,
							}, nil, nil
						}
					}

					// Check if the voice is installed on the system
					installed, err := IsVoiceInstalled(voice)
					if err != nil {
						log.Warn("Failed to check voice availability", "error", err, "voice", voice)
						// Continue anyway - the say command will fall back to default voice
					} else if !installed {
						return &mcp.CallToolResult{
							Content: []mcp.Content{&mcp.TextContent{Text: VoiceNotInstalledError(voice)}},
							IsError: true,
						}, nil, nil
					}

					args = append(args, "--voice", voice)
				}

				// Check for potentially dangerous shell metacharacters
				// Note: exec.Command with separate arguments is already safe from command injection,
				// but we're adding this check as an additional safeguard
				dangerousChars := []rune{';', '&', '|', '<', '>', '`', '$', '(', ')', '{', '}', '[', ']', '\\', '\'', '"', '\n', '\r'}
				for _, char := range dangerousChars {
					if bytes.ContainsRune([]byte(text), char) {
						log.Warn("Potentially dangerous character in text input",
							"char", string(char),
							"text", text)
					}
				}

				// Add the text as the last argument
				args = append(args, text)

				log.Debug("Executing say command", "args", args)
				// Execute the say command with context for cancellation
				sayCmd := exec.CommandContext(ctx, "/usr/bin/say", args...)
				if err := sayCmd.Start(); err != nil {
					log.Error("Failed to start say command", "error", err)
					return &mcp.CallToolResult{
						Content: []mcp.Content{&mcp.TextContent{Text: fmt.Sprintf("Error: Failed to start say command: %v", err)}},
						IsError: true,
					}, nil, nil
				}

				// Wait for command completion or cancellation in a goroutine
				done := make(chan error, 1)
				go func() {
					done <- sayCmd.Wait()
				}()

				select {
				case err := <-done:
					if err != nil {
						if ctx.Err() == context.Canceled {
							log.Info("Say command cancelled by user")
							return &mcp.CallToolResult{
								Content: []mcp.Content{&mcp.TextContent{Text: "Say command cancelled"}},
							}, nil, nil
						}
						log.Error("Say command failed", "error", err)
						return &mcp.CallToolResult{
							Content: []mcp.Content{&mcp.TextContent{Text: fmt.Sprintf("Error: Say command failed: %v", err)}},
							IsError: true,
						}, nil, nil
					}
					log.Info("Speaking text completed", "text", text)
					var responseText string
					if suppressSpeakingOutput {
						responseText = "Speech completed"
					} else {
						responseText = fmt.Sprintf("Speaking: %s", text)
					}
					return &mcp.CallToolResult{
						Content: []mcp.Content{&mcp.TextContent{Text: responseText}},
					}, nil, nil
				case <-ctx.Done():
					log.Info("Say command cancelled by user")
					// The CommandContext will handle killing the process
					return &mcp.CallToolResult{
						Content: []mcp.Content{&mcp.TextContent{Text: "Say command cancelled"}},
					}, nil, nil
				}
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
			// Check for early cancellation
			select {
			case <-ctx.Done():
				return &mcp.CallToolResult{
					Content: []mcp.Content{&mcp.TextContent{Text: "Request cancelled"}},
				}, nil, nil
			default:
			}

			// Acquire TTS lock
			release, err := acquireTTSLock(ctx)
			if err != nil {
				log.Info("Request cancelled while waiting for TTS lock")
				return &mcp.CallToolResult{
					Content: []mcp.Content{&mcp.TextContent{Text: "Request cancelled while waiting for TTS"}},
				}, nil, nil
			}
			defer release()

			log.Debug("ElevenLabs tool called", "params", input)
			text := input.Text
			if text == "" {
				return &mcp.CallToolResult{
					Content: []mcp.Content{&mcp.TextContent{Text: "Error: text must be a string"}},
					IsError: true,
				}, nil, nil
			}

			voiceID := os.Getenv("ELEVENLABS_VOICE_ID")
			if voiceID == "" {
				voiceID = "1SM7GgM6IMuvQlz2BwM3"
				log.Debug("Voice not specified, using default", "voiceID", voiceID)
			}

			modelID := os.Getenv("ELEVENLABS_MODEL_ID")
			if modelID == "" {
				modelID = "eleven_v3"
				log.Debug("Model not specified, using default", "modelID", modelID)
			}

			apiKey := os.Getenv("ELEVENLABS_API_KEY")
			if apiKey == "" {
				log.Error("ELEVENLABS_API_KEY not set")
				result := &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "Error: ELEVENLABS_API_KEY is not set"}}}
				result.IsError = true
				return result, nil, nil
			}

			pipeReader, pipeWriter := io.Pipe()

			// Channel to signal when HTTP response status has been validated
			statusValidated := make(chan error, 1)
			// Channel to signal when audio playback is complete
			audioComplete := make(chan error, 1)

			g, ctx := errgroup.WithContext(ctx)

			g.Go(func() error {
				defer pipeWriter.Close()

				url := fmt.Sprintf("https://api.elevenlabs.io/v1/text-to-speech/%s/stream", voiceID)

				params := ElevenLabsParams{
					Text:    text,
					ModelID: modelID,
					VoiceSettings: SynthesisOptions{
						Stability:       0.60,
						SimilarityBoost: 0.75,
						Style:           0.50,
						UseSpeakerBoost: false,
					},
				}

				b, err := json.Marshal(params)
				if err != nil {
					log.Error("Failed to marshal request body", "error", err)
					statusValidated <- fmt.Errorf("failed to marshal request body: %v", err)
					return fmt.Errorf("failed to marshal request body: %v", err)
				}

				log.Debug("Making ElevenLabs API request",
					"url", url,
					"voice", voiceID,
					"model", modelID,
					"text", text,
					"params", params,
				)

				req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewBuffer(b))
				if err != nil {
					log.Error("Failed to create request", "error", err)
					statusValidated <- fmt.Errorf("failed to create request: %v", err)
					return fmt.Errorf("failed to create request: %v", err)
				}

				req.Header.Set("xi-api-key", apiKey)
				req.Header.Set("Content-Type", "application/json")
				req.Header.Set("accept", "audio/mpeg")

				safeLog("Sending HTTP request", req)
				res, err := http.DefaultClient.Do(req)
				if err != nil {
					log.Error("Failed to send request", "error", err)
					statusValidated <- fmt.Errorf("failed to send request: %v", err)
					return fmt.Errorf("failed to send request: %v", err)
				}
				defer res.Body.Close()

				if res.StatusCode != http.StatusOK {
					log.Error("Request failed", "status", res.Status, "statusCode", res.StatusCode)
					// Read the error response body for more details
					body, readErr := io.ReadAll(res.Body)
					errMsg := fmt.Errorf("ElevenLabs API error: status %d %s", res.StatusCode, res.Status)
					if readErr == nil && len(body) > 0 {
						log.Error("Error response body", "body", string(body))
						errMsg = fmt.Errorf("ElevenLabs API error (status %d): %s", res.StatusCode, string(body))
					}
					statusValidated <- errMsg
					return errMsg
				}

				// HTTP status is OK, signal success and proceed with streaming
				statusValidated <- nil

				log.Debug("Copying response body to pipe")
				bytesWritten, err := io.Copy(pipeWriter, res.Body)
				log.Debug("Response body copied", "bytes", bytesWritten)
				return err
			})

			// Wait for HTTP status validation before proceeding to decode
			select {
			case err := <-statusValidated:
				if err != nil {
					log.Error("HTTP request failed", "error", err)
					result := &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: fmt.Sprintf("Error: %v", err)}}}
					result.IsError = true
					return result, nil, nil
				}
				log.Debug("HTTP status validated successfully, proceeding to decode")
			case <-ctx.Done():
				log.Error("Context cancelled while waiting for HTTP status validation")
				result := &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "Error: Request cancelled"}}}
				result.IsError = true
				return result, nil, nil
			}

			// Start audio playback in a separate goroutine with cancellation support
			g.Go(func() error {
				log.Debug("Decoding MP3 stream")
				streamer, format, err := mp3.Decode(pipeReader)
				if err != nil {
					log.Error("Failed to decode response", "error", err)
					audioComplete <- fmt.Errorf("failed to decode response: %v", err)
					return fmt.Errorf("failed to decode response: %v", err)
				}
				defer streamer.Close()

				log.Debug("Initializing speaker", "sampleRate", format.SampleRate)
				speaker.Init(format.SampleRate, format.SampleRate.N(time.Second/10))
				done := make(chan bool, 1)

				// Play audio with callback
				speaker.Play(beep.Seq(streamer, beep.Callback(func() {
					done <- true
				})))

				log.Info("Speaking text via ElevenLabs", "text", text)

				// Wait for either completion or cancellation
				select {
				case <-done:
					log.Debug("Audio playback completed normally")
					audioComplete <- nil
					return nil
				case <-ctx.Done():
					log.Debug("Context cancelled, stopping audio playback")
					// Clear all audio from speaker to stop playback immediately
					speaker.Clear()
					audioComplete <- ctx.Err()
					return ctx.Err()
				}
			})

			// Wait for audio completion or cancellation
			select {
			case err := <-audioComplete:
				if err != nil && err != context.Canceled {
					log.Error("Audio playback failed", "error", err)
					result := &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: fmt.Sprintf("Error: %v", err)}}}
					result.IsError = true
					return result, nil, nil
				}
				if err == context.Canceled {
					log.Info("Audio playback cancelled by user")
					return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "Audio playback cancelled"}}}, nil, nil
				}
			case <-ctx.Done():
				log.Info("Request cancelled, stopping all operations")
				speaker.Clear()
				return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "Request cancelled"}}}, nil, nil
			}

			log.Debug("Finished speaking")

			// Check for any errors that occurred during streaming
			if err := g.Wait(); err != nil && err != context.Canceled {
				log.Error("Error occurred during streaming", "error", err)
				result := &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: fmt.Sprintf("Error: %v", err)}}}
				result.IsError = true
				return result, nil, nil
			}

			if suppressSpeakingOutput {
				return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "Speech completed"}}}, nil, nil
			}
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: fmt.Sprintf("Speaking: %s", text)}}}, nil, nil
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
			// Check for early cancellation
			select {
			case <-ctx.Done():
				return &mcp.CallToolResult{
					Content: []mcp.Content{&mcp.TextContent{Text: "Request cancelled"}},
				}, nil, nil
			default:
			}

			// Acquire TTS lock
			release, err := acquireTTSLock(ctx)
			if err != nil {
				log.Info("Request cancelled while waiting for TTS lock")
				return &mcp.CallToolResult{
					Content: []mcp.Content{&mcp.TextContent{Text: "Request cancelled while waiting for TTS"}},
				}, nil, nil
			}
			defer release()

			log.Debug("Google TTS tool called", "params", input)
			text := input.Text
			if text == "" {
				return &mcp.CallToolResult{
					Content: []mcp.Content{&mcp.TextContent{Text: "Error: Empty text provided"}},
					IsError: true,
				}, nil, nil
			}

			// Get configuration from arguments
			voice := "Kore"
			if input.Voice != nil && *input.Voice != "" {
				voice = *input.Voice
			}

			model := "gemini-2.5-flash-preview-tts"
			if input.Model != nil && *input.Model != "" {
				model = *input.Model
			}

			// Get API key from environment
			apiKey := os.Getenv("GOOGLE_AI_API_KEY")
			if apiKey == "" {
				apiKey = os.Getenv("GEMINI_API_KEY")
			}
			if apiKey == "" {
				log.Error("GOOGLE_AI_API_KEY or GEMINI_API_KEY not set")
				result := &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "Error: GOOGLE_AI_API_KEY or GEMINI_API_KEY is not set"}}}
				result.IsError = true
				return result, nil, nil
			}

			// Create Google AI client
			client, err := genai.NewClient(ctx, &genai.ClientConfig{
				APIKey:  apiKey,
				Backend: genai.BackendGeminiAPI,
			})
			if err != nil {
				log.Error("Failed to create Google AI client", "error", err)
				result := &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: fmt.Sprintf("Error: Failed to create client: %v", err)}}}
				result.IsError = true
				return result, nil, nil
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
				result := &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: fmt.Sprintf("Error: Failed to generate TTS audio: %v", err)}}}
				result.IsError = true
				return result, nil, nil
			}

			// Extract audio data from response
			if len(response.Candidates) == 0 || len(response.Candidates[0].Content.Parts) == 0 {
				log.Error("No audio data in TTS response")
				result := &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "Error: No audio data received from Google TTS"}}}
				result.IsError = true
				return result, nil, nil
			}

			part := response.Candidates[0].Content.Parts[0]
			if part.InlineData == nil {
				log.Error("No inline data in TTS response")
				result := &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "Error: No audio data received from Google TTS"}}}
				result.IsError = true
				return result, nil, nil
			}

			audioData := part.InlineData.Data
			totalSamples := len(audioData) / 2 // 16-bit samples = 2 bytes each
			log.Info("Playing TTS audio via beep speaker", "bytes", len(audioData), "samples", totalSamples)

			// Create PCM stream for beep (Google TTS returns 24kHz PCM)
			pcmStream := &PCMStream{
				data:       audioData,
				sampleRate: beep.SampleRate(24000), // 24kHz sample rate from Google TTS
				position:   0,
			}

			// Initialize speaker with the sample rate
			speaker.Init(pcmStream.sampleRate, pcmStream.sampleRate.N(time.Second/10))

			// Start progress reporting if client requested it
			progress := newProgressReporter(ctx, req, totalSamples, 24000)
			progress.start(func() int { return pcmStream.position })
			defer progress.stop()

			// Play the audio with cancellation support
			done := make(chan bool)
			speaker.Play(beep.Seq(pcmStream, beep.Callback(func() {
				done <- true
			})))

			log.Info("Speaking via Google TTS", "text", text, "voice", voice, "model", model)

			// Wait for either playback completion or cancellation
			select {
			case <-done:
				log.Debug("Google TTS audio playback completed normally")
				if suppressSpeakingOutput {
					return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "Speech completed"}}}, nil, nil
				}
				return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: fmt.Sprintf("Speaking: %s (via Google TTS with voice %s)", text, voice)}}}, nil, nil
			case <-ctx.Done():
				log.Debug("Context cancelled, stopping Google TTS audio playback")
				speaker.Clear()
				log.Info("Google TTS audio playback cancelled by user")
				return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "Google TTS audio playback cancelled"}}}, nil, nil
			}
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
			// Check for early cancellation
			select {
			case <-ctx.Done():
				return &mcp.CallToolResult{
					Content: []mcp.Content{&mcp.TextContent{Text: "Request cancelled"}},
				}, nil, nil
			default:
			}

			// Acquire TTS lock
			release, err := acquireTTSLock(ctx)
			if err != nil {
				log.Info("Request cancelled while waiting for TTS lock")
				return &mcp.CallToolResult{
					Content: []mcp.Content{&mcp.TextContent{Text: "Request cancelled while waiting for TTS"}},
				}, nil, nil
			}
			defer release()

			log.Debug("OpenAI TTS tool called", "params", input)
			text := input.Text
			if text == "" {
				return &mcp.CallToolResult{
					Content: []mcp.Content{&mcp.TextContent{Text: "Error: Empty text provided"}},
					IsError: true,
				}, nil, nil
			}

			// Get configuration from arguments
			voice := "alloy"
			if input.Voice != nil && *input.Voice != "" {
				voice = *input.Voice
			}

			model := "gpt-4o-mini-tts-2025-12-15"
			if input.Model != nil && *input.Model != "" {
				model = *input.Model
			}

			speed := 1.0
			if input.Speed != nil {
				if *input.Speed >= 0.25 && *input.Speed <= 4.0 {
					speed = *input.Speed
				} else {
					log.Warn("Speed out of range, using default", "provided", *input.Speed, "default", 1.0)
				}
			}

			// Get voice instructions from arguments or environment variable
			instructions := ""
			if input.Instructions != nil && *input.Instructions != "" {
				instructions = *input.Instructions
			} else {
				// Fallback to environment variable
				instructions = os.Getenv("OPENAI_TTS_INSTRUCTIONS")
			}

			// Basic validation for instructions length (OpenAI has reasonable limits)
			if len(instructions) > 1000 {
				log.Warn("Instructions are very long, may exceed API limits", "length", len(instructions))
			}

			// Get API key from environment
			apiKey := os.Getenv("OPENAI_API_KEY")
			if apiKey == "" {
				log.Error("OPENAI_API_KEY not set")
				result := &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "Error: OPENAI_API_KEY is not set"}}}
				result.IsError = true
				return result, nil, nil
			}

			// Create OpenAI client
			client := openai.NewClient(option.WithAPIKey(apiKey))

			logFields := []any{
				"model", model,
				"voice", voice,
				"speed", speed,
				"text", text,
			}
			if instructions != "" {
				logFields = append(logFields, "instructions", instructions)
			}
			log.Debug("Generating OpenAI TTS audio", logFields...)

			// Generate TTS audio
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
				result := &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: fmt.Sprintf("Error: Failed to generate TTS audio: %v", err)}}}
				result.IsError = true
				return result, nil, nil
			}
			defer response.Body.Close()

			log.Debug("Decoding MP3 stream from OpenAI")
			// OpenAI returns MP3 format by default
			streamer, format, err := mp3.Decode(response.Body)
			if err != nil {
				log.Error("Failed to decode OpenAI TTS response", "error", err)
				result := &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: fmt.Sprintf("Error: Failed to decode response: %v", err)}}}
				result.IsError = true
				return result, nil, nil
			}
			defer streamer.Close()

			// Get total length for progress reporting
			totalSamples := streamer.Len()
			log.Debug("Initializing speaker for OpenAI TTS", "sampleRate", format.SampleRate, "totalSamples", totalSamples)
			speaker.Init(format.SampleRate, format.SampleRate.N(time.Second/10))

			// Start progress reporting if client requested it
			progress := newProgressReporter(ctx, req, totalSamples, int(format.SampleRate))
			progress.start(func() int { return streamer.Position() })
			defer progress.stop()

			done := make(chan bool)
			speaker.Play(beep.Seq(streamer, beep.Callback(func() {
				done <- true
			})))

			logFields = []any{"text", text, "voice", voice, "model", model, "speed", speed}
			if instructions != "" {
				logFields = append(logFields, "instructions", instructions)
			}
			log.Info("Speaking text via OpenAI TTS", logFields...)

			// Wait for either playback completion or cancellation
			select {
			case <-done:
				log.Debug("OpenAI TTS audio playback completed normally")
				if suppressSpeakingOutput {
					return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "Speech completed"}}}, nil, nil
				}
				return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: fmt.Sprintf("Speaking: %s (via OpenAI TTS with voice %s)", text, voice)}}}, nil, nil
			case <-ctx.Done():
				log.Debug("Context cancelled, stopping OpenAI TTS audio playback")
				speaker.Clear()
				log.Info("OpenAI TTS audio playback cancelled by user")
				return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "OpenAI TTS audio playback cancelled"}}}, nil, nil
			}
		})

		log.Info("Starting MCP server", "name", "mcp-tts", "version", Version)
		// Start the server using stdin/stdout
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		if err := ctrlc.Default.Run(ctx, func() error {
			if err := s.Run(ctx, &mcp.StdioTransport{}); err != nil {
				return fmt.Errorf("failed to serve MCP: %v", err)
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
	log.With(reqCopy).Debug(message)
}

// Execute adds all child commands to the root command and sets flags appropriately.
// This is called by main.main(). It only needs to happen once to the rootCmd.
func Execute() {
	if err := rootCmd.Execute(); err != nil {
		log.Fatal("Failed to execute command", "error", err)
	}
}
