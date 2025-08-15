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
	Voice *string `json:"voice,omitempty" mcp:"Voice name to use (e.g. 'Kore', 'Aoede', 'Fenrir', default: 'Kore')"`
	Model *string `json:"model,omitempty" mcp:"TTS model to use (default: 'gemini-2.5-flash-preview-tts')"`
}

type OpenAITTSParams struct {
	Text         string   `json:"text" mcp:"The text to convert to speech using OpenAI TTS"`
	Voice        *string  `json:"voice,omitempty" mcp:"Voice to use (alloy, ash, ballad, coral, echo, fable, nova, onyx, sage, shimmer, verse; default: 'alloy')"`
	Model        *string  `json:"model,omitempty" mcp:"TTS model to use (gpt-4o-mini-tts, gpt-4o-audio-preview; default: 'gpt-4o-mini-tts')"`
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

		// Create a new MCP server
		impl := &mcp.Implementation{
			Name:    "Say TTS Service",
			Version: Version,
		}
		s := mcp.NewServer(impl, nil)

		// Prompt functionality removed - focusing on tools with new SDK

		if runtime.GOOS == "darwin" {
			// Add the "say_tts" tool
			sayTool := &mcp.Tool{
				Name:        "say_tts",
				Description: "Speaks the provided text out loud using the macOS text-to-speech engine",
				InputSchema: buildSayTTSSchema(),
			}

			// Add the say tool handler
			mcp.AddTool(s, sayTool, func(ctx context.Context, cc *mcp.ServerSession, params *mcp.CallToolParamsFor[SayTTSParams]) (*mcp.CallToolResultFor[any], error) {
				// Check for early cancellation
				select {
				case <-ctx.Done():
					return &mcp.CallToolResultFor[any]{
						Content: []mcp.Content{&mcp.TextContent{Text: "Request cancelled"}},
					}, nil
				default:
				}

				// Acquire TTS lock
				release, err := acquireTTSLock(ctx)
				if err != nil {
					log.Info("Request cancelled while waiting for TTS lock")
					return &mcp.CallToolResultFor[any]{
						Content: []mcp.Content{&mcp.TextContent{Text: "Request cancelled while waiting for TTS"}},
					}, nil
				}
				defer release()

				log.Debug("Say tool called", "params", params.Arguments)

				text := params.Arguments.Text
				if text == "" {
					return &mcp.CallToolResultFor[any]{
						Content: []mcp.Content{&mcp.TextContent{Text: "Error: Empty text provided"}},
						IsError: true,
					}, nil
				}

				args := []string{}

				// Add rate if provided
				if params.Arguments.Rate != nil {
					args = append(args, "--rate", fmt.Sprintf("%d", *params.Arguments.Rate))
				} else {
					args = append(args, "--rate", "200") // Default rate
				}

				// Add voice if provided and validate it
				if params.Arguments.Voice != nil && *params.Arguments.Voice != "" {
					voice := *params.Arguments.Voice
					// Simple validation to prevent command injection
					// Allow alphanumeric characters, spaces, hyphens, underscores, and parentheses
					for _, r := range voice {
						if !((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') ||
							r == ' ' || r == '(' || r == ')' || r == '-' || r == '_') {
							return &mcp.CallToolResultFor[any]{
								Content: []mcp.Content{&mcp.TextContent{Text: fmt.Sprintf("Error: Voice contains invalid characters: %s", voice)}},
								IsError: true,
							}, nil
						}
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
					return &mcp.CallToolResultFor[any]{
						Content: []mcp.Content{&mcp.TextContent{Text: fmt.Sprintf("Error: Failed to start say command: %v", err)}},
						IsError: true,
					}, nil
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
							return &mcp.CallToolResultFor[any]{
								Content: []mcp.Content{&mcp.TextContent{Text: "Say command cancelled"}},
							}, nil
						}
						log.Error("Say command failed", "error", err)
						return &mcp.CallToolResultFor[any]{
							Content: []mcp.Content{&mcp.TextContent{Text: fmt.Sprintf("Error: Say command failed: %v", err)}},
							IsError: true,
						}, nil
					}
					log.Info("Speaking text completed", "text", text)
					var responseText string
					if suppressSpeakingOutput {
						responseText = "Speech completed"
					} else {
						responseText = fmt.Sprintf("Speaking: %s", text)
					}
					return &mcp.CallToolResultFor[any]{
						Content: []mcp.Content{&mcp.TextContent{Text: responseText}},
					}, nil
				case <-ctx.Done():
					log.Info("Say command cancelled by user")
					// The CommandContext will handle killing the process
					return &mcp.CallToolResultFor[any]{
						Content: []mcp.Content{&mcp.TextContent{Text: "Say command cancelled"}},
					}, nil
				}
			})
		}

		elevenLabsTool := &mcp.Tool{
			Name:        "elevenlabs_tts",
			Description: "Uses the ElevenLabs API to generate speech from text",
			InputSchema: buildElevenLabsTTSSchema(),
		}

		mcp.AddTool(s, elevenLabsTool, func(ctx context.Context, cc *mcp.ServerSession, params *mcp.CallToolParamsFor[ElevenLabsTTSParams]) (*mcp.CallToolResultFor[any], error) {
			// Check for early cancellation
			select {
			case <-ctx.Done():
				return &mcp.CallToolResultFor[any]{
					Content: []mcp.Content{&mcp.TextContent{Text: "Request cancelled"}},
				}, nil
			default:
			}

			// Acquire TTS lock
			release, err := acquireTTSLock(ctx)
			if err != nil {
				log.Info("Request cancelled while waiting for TTS lock")
				return &mcp.CallToolResultFor[any]{
					Content: []mcp.Content{&mcp.TextContent{Text: "Request cancelled while waiting for TTS"}},
				}, nil
			}
			defer release()

			log.Debug("ElevenLabs tool called", "params", params.Arguments)
			text := params.Arguments.Text
			if text == "" {
				return &mcp.CallToolResultFor[any]{
					Content: []mcp.Content{&mcp.TextContent{Text: "Error: text must be a string"}},
					IsError: true,
				}, nil
			}

			voiceID := os.Getenv("ELEVENLABS_VOICE_ID")
			if voiceID == "" {
				voiceID = "1SM7GgM6IMuvQlz2BwM3"
				log.Debug("Voice not specified, using default", "voiceID", voiceID)
			}

			modelID := os.Getenv("ELEVENLABS_MODEL_ID")
			if modelID == "" {
				modelID = "eleven_multilingual_v2" // eleven_turbo_v2_5 is also available
				log.Debug("Model not specified, using default", "modelID", modelID)
			}

			apiKey := os.Getenv("ELEVENLABS_API_KEY")
			if apiKey == "" {
				log.Error("ELEVENLABS_API_KEY not set")
				result := &mcp.CallToolResultFor[any]{Content: []mcp.Content{&mcp.TextContent{Text: "Error: ELEVENLABS_API_KEY is not set"}}}
				result.IsError = true
				return result, nil
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
					result := &mcp.CallToolResultFor[any]{Content: []mcp.Content{&mcp.TextContent{Text: fmt.Sprintf("Error: %v", err)}}}
					result.IsError = true
					return result, nil
				}
				log.Debug("HTTP status validated successfully, proceeding to decode")
			case <-ctx.Done():
				log.Error("Context cancelled while waiting for HTTP status validation")
				result := &mcp.CallToolResultFor[any]{Content: []mcp.Content{&mcp.TextContent{Text: "Error: Request cancelled"}}}
				result.IsError = true
				return result, nil
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
					result := &mcp.CallToolResultFor[any]{Content: []mcp.Content{&mcp.TextContent{Text: fmt.Sprintf("Error: %v", err)}}}
					result.IsError = true
					return result, nil
				}
				if err == context.Canceled {
					log.Info("Audio playback cancelled by user")
					return &mcp.CallToolResultFor[any]{Content: []mcp.Content{&mcp.TextContent{Text: "Audio playback cancelled"}}}, nil
				}
			case <-ctx.Done():
				log.Info("Request cancelled, stopping all operations")
				speaker.Clear()
				return &mcp.CallToolResultFor[any]{Content: []mcp.Content{&mcp.TextContent{Text: "Request cancelled"}}}, nil
			}

			log.Debug("Finished speaking")

			// Check for any errors that occurred during streaming
			if err := g.Wait(); err != nil && err != context.Canceled {
				log.Error("Error occurred during streaming", "error", err)
				result := &mcp.CallToolResultFor[any]{Content: []mcp.Content{&mcp.TextContent{Text: fmt.Sprintf("Error: %v", err)}}}
				result.IsError = true
				return result, nil
			}

			if suppressSpeakingOutput {
				return &mcp.CallToolResultFor[any]{Content: []mcp.Content{&mcp.TextContent{Text: "Speech completed"}}}, nil
			}
			return &mcp.CallToolResultFor[any]{Content: []mcp.Content{&mcp.TextContent{Text: fmt.Sprintf("Speaking: %s", text)}}}, nil
		})

		// Add Google TTS tool
		googleTTSTool := &mcp.Tool{
			Name:        "google_tts",
			Description: "Uses Google's dedicated Text-to-Speech API with Gemini TTS models",
			InputSchema: buildGoogleTTSSchema(),
		}

		mcp.AddTool(s, googleTTSTool, func(ctx context.Context, cc *mcp.ServerSession, params *mcp.CallToolParamsFor[GoogleTTSParams]) (*mcp.CallToolResultFor[any], error) {
			// Check for early cancellation
			select {
			case <-ctx.Done():
				return &mcp.CallToolResultFor[any]{
					Content: []mcp.Content{&mcp.TextContent{Text: "Request cancelled"}},
				}, nil
			default:
			}

			// Acquire TTS lock
			release, err := acquireTTSLock(ctx)
			if err != nil {
				log.Info("Request cancelled while waiting for TTS lock")
				return &mcp.CallToolResultFor[any]{
					Content: []mcp.Content{&mcp.TextContent{Text: "Request cancelled while waiting for TTS"}},
				}, nil
			}
			defer release()

			log.Debug("Google TTS tool called", "params", params.Arguments)
			text := params.Arguments.Text
			if text == "" {
				return &mcp.CallToolResultFor[any]{
					Content: []mcp.Content{&mcp.TextContent{Text: "Error: Empty text provided"}},
					IsError: true,
				}, nil
			}

			// Get configuration from arguments
			voice := "Kore"
			if params.Arguments.Voice != nil && *params.Arguments.Voice != "" {
				voice = *params.Arguments.Voice
			}

			model := "gemini-2.5-flash-preview-tts"
			if params.Arguments.Model != nil && *params.Arguments.Model != "" {
				model = *params.Arguments.Model
			}

			// Get API key from environment
			apiKey := os.Getenv("GOOGLE_AI_API_KEY")
			if apiKey == "" {
				apiKey = os.Getenv("GEMINI_API_KEY")
			}
			if apiKey == "" {
				log.Error("GOOGLE_AI_API_KEY or GEMINI_API_KEY not set")
				result := &mcp.CallToolResultFor[any]{Content: []mcp.Content{&mcp.TextContent{Text: "Error: GOOGLE_AI_API_KEY or GEMINI_API_KEY is not set"}}}
				result.IsError = true
				return result, nil
			}

			// Create Google AI client
			client, err := genai.NewClient(ctx, &genai.ClientConfig{
				APIKey:  apiKey,
				Backend: genai.BackendGeminiAPI,
			})
			if err != nil {
				log.Error("Failed to create Google AI client", "error", err)
				result := &mcp.CallToolResultFor[any]{Content: []mcp.Content{&mcp.TextContent{Text: fmt.Sprintf("Error: Failed to create client: %v", err)}}}
				result.IsError = true
				return result, nil
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
				result := &mcp.CallToolResultFor[any]{Content: []mcp.Content{&mcp.TextContent{Text: fmt.Sprintf("Error: Failed to generate TTS audio: %v", err)}}}
				result.IsError = true
				return result, nil
			}

			// Extract audio data from response
			if len(response.Candidates) == 0 || len(response.Candidates[0].Content.Parts) == 0 {
				log.Error("No audio data in TTS response")
				result := &mcp.CallToolResultFor[any]{Content: []mcp.Content{&mcp.TextContent{Text: "Error: No audio data received from Google TTS"}}}
				result.IsError = true
				return result, nil
			}

			part := response.Candidates[0].Content.Parts[0]
			if part.InlineData == nil {
				log.Error("No inline data in TTS response")
				result := &mcp.CallToolResultFor[any]{Content: []mcp.Content{&mcp.TextContent{Text: "Error: No audio data received from Google TTS"}}}
				result.IsError = true
				return result, nil
			}

			audioData := part.InlineData.Data
			log.Info("Playing TTS audio via beep speaker", "bytes", len(audioData))

			// Create PCM stream for beep (Google TTS returns 24kHz PCM)
			pcmStream := &PCMStream{
				data:       audioData,
				sampleRate: beep.SampleRate(24000), // 24kHz sample rate from Google TTS
				position:   0,
			}

			// Initialize speaker with the sample rate
			speaker.Init(pcmStream.sampleRate, pcmStream.sampleRate.N(time.Second/10))

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
					return &mcp.CallToolResultFor[any]{Content: []mcp.Content{&mcp.TextContent{Text: "Speech completed"}}}, nil
				}
				return &mcp.CallToolResultFor[any]{Content: []mcp.Content{&mcp.TextContent{Text: fmt.Sprintf("Speaking: %s (via Google TTS with voice %s)", text, voice)}}}, nil
			case <-ctx.Done():
				log.Debug("Context cancelled, stopping Google TTS audio playback")
				speaker.Clear()
				log.Info("Google TTS audio playback cancelled by user")
				return &mcp.CallToolResultFor[any]{Content: []mcp.Content{&mcp.TextContent{Text: "Google TTS audio playback cancelled"}}}, nil
			}
		})

		// Add OpenAI TTS tool
		openaiTTSTool := &mcp.Tool{
			Name:        "openai_tts",
			Description: "Uses OpenAI's Text-to-Speech API to generate speech from text",
			InputSchema: buildOpenAITTSSchema(),
		}

		mcp.AddTool(s, openaiTTSTool, func(ctx context.Context, cc *mcp.ServerSession, params *mcp.CallToolParamsFor[OpenAITTSParams]) (*mcp.CallToolResultFor[any], error) {
			// Check for early cancellation
			select {
			case <-ctx.Done():
				return &mcp.CallToolResultFor[any]{
					Content: []mcp.Content{&mcp.TextContent{Text: "Request cancelled"}},
				}, nil
			default:
			}

			// Acquire TTS lock
			release, err := acquireTTSLock(ctx)
			if err != nil {
				log.Info("Request cancelled while waiting for TTS lock")
				return &mcp.CallToolResultFor[any]{
					Content: []mcp.Content{&mcp.TextContent{Text: "Request cancelled while waiting for TTS"}},
				}, nil
			}
			defer release()

			log.Debug("OpenAI TTS tool called", "params", params.Arguments)
			text := params.Arguments.Text
			if text == "" {
				return &mcp.CallToolResultFor[any]{
					Content: []mcp.Content{&mcp.TextContent{Text: "Error: Empty text provided"}},
					IsError: true,
				}, nil
			}

			// Get configuration from arguments
			voice := "alloy"
			if params.Arguments.Voice != nil && *params.Arguments.Voice != "" {
				voice = *params.Arguments.Voice
			}

			model := "gpt-4o-mini-tts"
			if params.Arguments.Model != nil && *params.Arguments.Model != "" {
				model = *params.Arguments.Model
			}

			speed := 1.0
			if params.Arguments.Speed != nil {
				if *params.Arguments.Speed >= 0.25 && *params.Arguments.Speed <= 4.0 {
					speed = *params.Arguments.Speed
				} else {
					log.Warn("Speed out of range, using default", "provided", *params.Arguments.Speed, "default", 1.0)
				}
			}

			// Get voice instructions from arguments or environment variable
			instructions := ""
			if params.Arguments.Instructions != nil && *params.Arguments.Instructions != "" {
				instructions = *params.Arguments.Instructions
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
				result := &mcp.CallToolResultFor[any]{Content: []mcp.Content{&mcp.TextContent{Text: "Error: OPENAI_API_KEY is not set"}}}
				result.IsError = true
				return result, nil
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
				result := &mcp.CallToolResultFor[any]{Content: []mcp.Content{&mcp.TextContent{Text: fmt.Sprintf("Error: Failed to generate TTS audio: %v", err)}}}
				result.IsError = true
				return result, nil
			}
			defer response.Body.Close()

			log.Debug("Decoding MP3 stream from OpenAI")
			// OpenAI returns MP3 format by default
			streamer, format, err := mp3.Decode(response.Body)
			if err != nil {
				log.Error("Failed to decode OpenAI TTS response", "error", err)
				result := &mcp.CallToolResultFor[any]{Content: []mcp.Content{&mcp.TextContent{Text: fmt.Sprintf("Error: Failed to decode response: %v", err)}}}
				result.IsError = true
				return result, nil
			}
			defer streamer.Close()

			log.Debug("Initializing speaker for OpenAI TTS", "sampleRate", format.SampleRate)
			speaker.Init(format.SampleRate, format.SampleRate.N(time.Second/10))
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
					return &mcp.CallToolResultFor[any]{Content: []mcp.Content{&mcp.TextContent{Text: "Speech completed"}}}, nil
				}
				return &mcp.CallToolResultFor[any]{Content: []mcp.Content{&mcp.TextContent{Text: fmt.Sprintf("Speaking: %s (via OpenAI TTS with voice %s)", text, voice)}}}, nil
			case <-ctx.Done():
				log.Debug("Context cancelled, stopping OpenAI TTS audio playback")
				speaker.Clear()
				log.Info("OpenAI TTS audio playback cancelled by user")
				return &mcp.CallToolResultFor[any]{Content: []mcp.Content{&mcp.TextContent{Text: "OpenAI TTS audio playback cancelled"}}}, nil
			}
		})

		log.Info("Starting MCP server", "name", "Say TTS Service", "version", Version)
		// Start the server using stdin/stdout
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		if err := ctrlc.Default.Run(ctx, func() error {
			if err := s.Run(ctx, mcp.NewStdioTransport()); err != nil {
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
