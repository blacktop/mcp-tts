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
	"strconv"
	"strings"
	"time"

	"github.com/caarlos0/ctrlc"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/log"
	"github.com/gopxl/beep/v2"
	"github.com/gopxl/beep/v2/mp3"
	"github.com/gopxl/beep/v2/speaker"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/openai/openai-go"
	"github.com/openai/openai-go/option"
	"github.com/spf13/cobra"
	"golang.org/x/sync/errgroup"
	"google.golang.org/genai"
	"github.com/blacktop/mcp-tts/internal/windows"
)

// isWSL detects if the code is running in Windows Subsystem for Linux
func isWSL() bool {
	data, err := os.ReadFile("/proc/version")
	if err != nil {
		return false
	}
	version := strings.ToLower(string(data))
	return strings.Contains(version, "microsoft") || strings.Contains(version, "wsl")
}

// canRunPowerShell checks if PowerShell is available for TTS
func canRunPowerShell() bool {
	// PowerShell is available on Windows and WSL
	if runtime.GOOS == "windows" {
		return true
	}
	if runtime.GOOS == "linux" && isWSL() {
		return true
	}
	// Could also be available on other platforms with PowerShell Core,
	// but we'll be conservative for now
	return false
}

var (
	verbose bool
	logger  *log.Logger
	// Version stores the service's version
	Version string
	// Global cancellation manager
	cancellationManager *CancellationManager
	// Flag to suppress "Speaking:" output
	suppressSpeakingOutput bool
)

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
	
	// Check environment variable for suppressing output
	if os.Getenv("MCP_TTS_SUPPRESS_SPEAKING_OUTPUT") == "true" {
		suppressSpeakingOutput = true
	}
}

// rootCmd represents the base command when called without any subcommands
var rootCmd = &cobra.Command{
	Use:   "mcp-tts",
	Short: "TTS (text-to-speech) MCP Server",
	Long: `TTS (text-to-speech) MCP Server.

Provides multiple text-to-speech services via MCP protocol:

• say_tts - Uses macOS built-in 'say' command (macOS only)
• windows_speech_tts - Uses Windows Speech API via PowerShell (Windows/WSL) - Supports both SAPI and WinRT
• windows_speech_voices - Lists available Windows Speech API voices from both SAPI and WinRT (Windows/WSL)
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

		// Initialize cancellation manager
		cancellationManager = NewCancellationManager()

		// Ensure proper shutdown of cancellation manager
		defer func() {
			if cancellationManager != nil {
				cancellationManager.Shutdown()
			}
		}()

		// Create a new MCP server
		s := server.NewMCPServer(
			"Say TTS Service",
			Version,
			server.WithPromptCapabilities(true),
			server.WithToolCapabilities(true),
			server.WithLogging(),
		)

		s.AddPrompt(mcp.NewPrompt("say",
			mcp.WithPromptDescription("Speaks the provided text out loud using the macOS text-to-speech engine"),
			mcp.WithArgument("text",
				mcp.RequiredArgument(),
				mcp.ArgumentDescription("The text to be spoken"),
			),
			mcp.WithArgument("rate",
				mcp.ArgumentDescription("The rate at which the text is spoken (words per minute)"),
			),
			mcp.WithArgument("voice",
				mcp.ArgumentDescription("The voice to use for speech"),
			),
		), func(ctx context.Context, request mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
			text := request.Params.Arguments["text"]
			if text == "" {
				return nil, fmt.Errorf("text is required")
			}

			args := []string{}

			// Add rate if provided
			if rate := request.Params.Arguments["rate"]; rate != "" {
				rateInt, _ := strconv.Atoi(rate)
				args = append(args, "--rate", fmt.Sprintf("%d", rateInt))
			} else {
				args = append(args, "--rate", "200") // Default rate
			}

			// Add voice if provided
			if voice := request.Params.Arguments["voice"]; voice != "" {
				args = append(args, "--voice", voice)
			}

			args = append(args, text)

			// Execute the say command
			sayCmd := exec.Command("/usr/bin/say", args...)
			if err := sayCmd.Start(); err != nil {
				return nil, fmt.Errorf("failed to start say command: %v", err)
			}

			var content string
			if suppressSpeakingOutput {
				content = "Speech completed"
			} else {
				content = fmt.Sprintf("Speaking: %s", text)
			}
			
			return mcp.NewGetPromptResult(
				"Speaking text",
				[]mcp.PromptMessage{
					mcp.NewPromptMessage(
						mcp.RoleUser,
						mcp.NewTextContent(content),
					),
				},
			), nil
		})

		if runtime.GOOS == "darwin" {
			// Add the "say_tts" tool
			sayTool := mcp.NewTool("say_tts",
				mcp.WithDescription("Speaks the provided text out loud using the macOS text-to-speech engine"),
				mcp.WithString("text",
					mcp.Required(),
					mcp.Description("The text to be spoken"),
				),
				mcp.WithNumber("rate",
					mcp.Description("The rate at which the text is spoken (words per minute)"),
				),
				mcp.WithString("voice",
					mcp.Description("The voice to use for speech"),
				),
			)

			// Add the say tool handler
			s.AddTool(sayTool, WithCancellation(func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				log.Debug("Say tool called", "request", request)
				arguments := request.GetArguments()
				text, ok := arguments["text"].(string)
				if !ok {
					result := mcp.NewToolResultText("Error: text must be a string")
					result.IsError = true
					return result, nil
				}

				args := []string{}

				// Add rate if provided
				if rate, ok := arguments["rate"].(float64); ok {
					args = append(args, "--rate", fmt.Sprintf("%d", int(rate)))
				} else {
					args = append(args, "--rate", "200") // Default rate
				}

				// Add voice if provided and validate it
				if voice, ok := arguments["voice"].(string); ok && voice != "" {
					// Simple validation to prevent command injection
					// Only allow alphanumeric characters, spaces, and some common punctuation
					for _, r := range voice {
						if !((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || r == ' ' || r == '(' || r == ')') {
							result := mcp.NewToolResultText(fmt.Sprintf("Error: Voice contains invalid characters: %s", voice))
							result.IsError = true
							return result, nil
						}
					}
					args = append(args, "--voice", voice)
				}

				// Text is passed as a separate argument, not through shell, which provides some safety
				// but we'll still do basic validation
				if text == "" {
					result := mcp.NewToolResultText("Error: Empty text provided")
					result.IsError = true
					return result, nil
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
					result := mcp.NewToolResultText(fmt.Sprintf("Error: Failed to start say command: %v", err))
					result.IsError = true
					return result, nil
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
							return mcp.NewToolResultText("Say command cancelled"), nil
						}
						log.Error("Say command failed", "error", err)
						result := mcp.NewToolResultText(fmt.Sprintf("Error: Say command failed: %v", err))
						result.IsError = true
						return result, nil
					}
					log.Info("Speaking text completed", "text", text)
					if suppressSpeakingOutput {
						return mcp.NewToolResultText("Speech completed"), nil
					}
					return mcp.NewToolResultText(fmt.Sprintf("Speaking: %s", text)), nil
				case <-ctx.Done():
					log.Info("Say command cancelled by user")
					// The CommandContext will handle killing the process
					return mcp.NewToolResultText("Say command cancelled"), nil
				}
			}))
		}

		// Add Windows Speech TTS tool if PowerShell is available
		if canRunPowerShell() {
			ttsEngine := windows.NewTTSEngine()
			
			windowsSpeechTool := mcp.NewTool("windows_speech_tts",
				mcp.WithDescription("Uses Windows Speech API via PowerShell (Windows/WSL) - Supports both SAPI and WinRT"),
				mcp.WithString("text",
					mcp.Required(),
					mcp.Description("The text to be spoken"),
				),
				mcp.WithNumber("rate",
					mcp.Description("The rate at which the text is spoken (-10 to 10, default 0)"),
				),
				mcp.WithString("voice",
					mcp.Description("The voice to use for speech (e.g., 'Microsoft Zira Desktop' for SAPI, 'Microsoft Linda' for WinRT)"),
				),
				mcp.WithString("api",
					mcp.Description("TTS API to use: 'sapi', 'winrt', or 'auto' (default: auto)"),
				),
			)

			s.AddTool(windowsSpeechTool, WithCancellation(func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				log.Debug("Windows Speech TTS tool called", "request", request)
				arguments := request.GetArguments()
				text, ok := arguments["text"].(string)
				if !ok {
					result := mcp.NewToolResultText("Error: text must be a string")
					result.IsError = true
					return result, nil
				}
				
				// Debug: Log the exact text received
				log.Info("DEBUG: Received text", "text", text, "length", len(text))

				if text == "" {
					result := mcp.NewToolResultText("Error: Empty text provided")
					result.IsError = true
					return result, nil
				}

				// Build TTS configuration
				config := windows.TTSConfig{
					Text: text,
					API:  "auto", // default
				}

				// Set API if provided
				if api, ok := arguments["api"].(string); ok && api != "" {
					config.API = api
				}

				// Set voice if provided
				if voice, ok := arguments["voice"].(string); ok && voice != "" {
					config.Voice = voice
				}

				// Set rate if provided
				if rate, ok := arguments["rate"].(float64); ok {
					rateInt := int(rate)
					if rateInt < -10 {
						rateInt = -10
					} else if rateInt > 10 {
						rateInt = 10
					}
					config.Rate = &rateInt
				}

				log.Debug("Executing Windows TTS", "config", config)

				// Execute TTS
				if err := ttsEngine.Speak(ctx, config); err != nil {
					log.Error("Windows TTS failed", "error", err)
					result := mcp.NewToolResultText(fmt.Sprintf("Error: %v", err))
					result.IsError = true
					return result, nil
				}

				log.Info("Speaking text completed via Windows TTS", "text", text)
				if suppressSpeakingOutput {
					return mcp.NewToolResultText("Speech completed"), nil
				}
				return mcp.NewToolResultText(fmt.Sprintf("Speaking: %s", text)), nil
			}))

			// Add companion tool to list available voices from both APIs
			listVoicesTool := mcp.NewTool("windows_speech_voices",
				mcp.WithDescription("Lists available Windows Speech API voices from both SAPI and WinRT (Windows/WSL)"),
			)

			s.AddTool(listVoicesTool, WithCancellation(func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				log.Debug("Windows Speech voices tool called", "request", request)

				voices, err := ttsEngine.GetVoices(ctx)
				if err != nil {
					log.Error("Failed to list Windows Speech voices", "error", err)
					result := mcp.NewToolResultText(fmt.Sprintf("Error: Failed to list voices: %v", err))
					result.IsError = true
					return result, nil
				}

				result := windows.FormatVoiceList(voices)
				return mcp.NewToolResultText(result), nil
			}))
		}

		elevenLabsTool := mcp.NewTool("elevenlabs_tts",
			mcp.WithDescription("Uses the ElevenLabs API to generate speech from text"),
			mcp.WithString("text",
				mcp.Required(),
				mcp.Description("The text to be spoken"),
			),
		)

		s.AddTool(elevenLabsTool, WithCancellation(func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			log.Debug("ElevenLabs tool called", "request", request)
			arguments := request.GetArguments()
			text, ok := arguments["text"].(string)
			if !ok {
				result := mcp.NewToolResultText("Error: text must be a string")
				result.IsError = true
				return result, nil
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
				result := mcp.NewToolResultText("Error: ELEVENLABS_API_KEY is not set")
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
					result := mcp.NewToolResultText(fmt.Sprintf("Error: %v", err))
					result.IsError = true
					return result, nil
				}
				log.Debug("HTTP status validated successfully, proceeding to decode")
			case <-ctx.Done():
				log.Error("Context cancelled while waiting for HTTP status validation")
				result := mcp.NewToolResultText("Error: Request cancelled")
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
					result := mcp.NewToolResultText(fmt.Sprintf("Error: %v", err))
					result.IsError = true
					return result, nil
				}
				if err == context.Canceled {
					log.Info("Audio playback cancelled by user")
					return mcp.NewToolResultText("Audio playback cancelled"), nil
				}
			case <-ctx.Done():
				log.Info("Request cancelled, stopping all operations")
				speaker.Clear()
				return mcp.NewToolResultText("Request cancelled"), nil
			}

			log.Debug("Finished speaking")

			// Check for any errors that occurred during streaming
			if err := g.Wait(); err != nil && err != context.Canceled {
				log.Error("Error occurred during streaming", "error", err)
				result := mcp.NewToolResultText(fmt.Sprintf("Error: %v", err))
				result.IsError = true
				return result, nil
			}

			if suppressSpeakingOutput {
				return mcp.NewToolResultText("Speech completed"), nil
			}
			return mcp.NewToolResultText(fmt.Sprintf("Speaking: %s", text)), nil
		}))

		// Add Google TTS tool
		googleTTSTool := mcp.NewTool("google_tts",
			mcp.WithDescription("Uses Google's dedicated Text-to-Speech API with Gemini TTS models"),
			mcp.WithString("text",
				mcp.Required(),
				mcp.Description("The text message to convert to speech"),
			),
			mcp.WithString("voice",
				mcp.Description("Voice name: Zephyr, Puck, Charon, Kore, Fenrir, Aoede, Leda, Orus, etc. (default: Kore)"),
			),
			mcp.WithString("model",
				mcp.Description("TTS model: gemini-2.5-flash-preview-tts, gemini-2.5-pro-preview-tts (default: gemini-2.5-flash-preview-tts)"),
			),
		)

		s.AddTool(googleTTSTool, WithCancellation(func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			log.Debug("Google TTS tool called", "request", request)
			arguments := request.GetArguments()
			text, ok := arguments["text"].(string)
			if !ok {
				result := mcp.NewToolResultText("Error: text must be a string")
				result.IsError = true
				return result, nil
			}

			if text == "" {
				result := mcp.NewToolResultText("Error: Empty text provided")
				result.IsError = true
				return result, nil
			}

			// Get configuration from arguments
			voice := "Kore"
			if v, ok := arguments["voice"].(string); ok && v != "" {
				voice = v
			}

			model := "gemini-2.5-flash-preview-tts"
			if m, ok := arguments["model"].(string); ok && m != "" {
				model = m
			}

			// Get API key from environment
			apiKey := os.Getenv("GOOGLE_AI_API_KEY")
			if apiKey == "" {
				apiKey = os.Getenv("GEMINI_API_KEY")
			}
			if apiKey == "" {
				log.Error("GOOGLE_AI_API_KEY or GEMINI_API_KEY not set")
				result := mcp.NewToolResultText("Error: GOOGLE_AI_API_KEY or GEMINI_API_KEY is not set")
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
				result := mcp.NewToolResultText(fmt.Sprintf("Error: Failed to create client: %v", err))
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
				result := mcp.NewToolResultText(fmt.Sprintf("Error: Failed to generate TTS audio: %v", err))
				result.IsError = true
				return result, nil
			}

			// Extract audio data from response
			if len(response.Candidates) == 0 || len(response.Candidates[0].Content.Parts) == 0 {
				log.Error("No audio data in TTS response")
				result := mcp.NewToolResultText("Error: No audio data received from Google TTS")
				result.IsError = true
				return result, nil
			}

			part := response.Candidates[0].Content.Parts[0]
			if part.InlineData == nil {
				log.Error("No inline data in TTS response")
				result := mcp.NewToolResultText("Error: No audio data received from Google TTS")
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
					return mcp.NewToolResultText("Speech completed"), nil
				}
				return mcp.NewToolResultText(fmt.Sprintf("Speaking: %s (via Google TTS with voice %s)", text, voice)), nil
			case <-ctx.Done():
				log.Debug("Context cancelled, stopping Google TTS audio playback")
				speaker.Clear()
				log.Info("Google TTS audio playback cancelled by user")
				return mcp.NewToolResultText("Google TTS audio playback cancelled"), nil
			}
		}))

		// Add OpenAI TTS tool
		openaiTTSTool := mcp.NewTool("openai_tts",
			mcp.WithDescription("Uses OpenAI's Text-to-Speech API to generate speech from text"),
			mcp.WithString("text",
				mcp.Required(),
				mcp.Description("The text to be spoken"),
			),
			mcp.WithString("voice",
				mcp.Description("Voice to use: coral, alloy, echo, fable, onyx, nova, shimmer (default: coral)"),
			),
			mcp.WithString("model",
				mcp.Description("TTS model: gpt-4o-mini-tts, tts-1, tts-1-hd (default: gpt-4o-mini-tts)"),
			),
			mcp.WithNumber("speed",
				mcp.Description("Speed of speech from 0.25 to 4.0 (default: 1.0)"),
			),
			mcp.WithString("instructions",
				mcp.Description("Custom voice instructions (e.g., 'Speak in a cheerful and positive tone'). Can be set via OPENAI_TTS_INSTRUCTIONS env var"),
			),
		)

		s.AddTool(openaiTTSTool, WithCancellation(func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			log.Debug("OpenAI TTS tool called", "request", request)
			arguments := request.GetArguments()
			text, ok := arguments["text"].(string)
			if !ok {
				result := mcp.NewToolResultText("Error: text must be a string")
				result.IsError = true
				return result, nil
			}

			if text == "" {
				result := mcp.NewToolResultText("Error: Empty text provided")
				result.IsError = true
				return result, nil
			}

			// Get configuration from arguments
			voice := "coral"
			if v, ok := arguments["voice"].(string); ok && v != "" {
				voice = v
			}

			model := "gpt-4o-mini-tts"
			if m, ok := arguments["model"].(string); ok && m != "" {
				model = m
			}

			speed := 1.0
			if s, ok := arguments["speed"].(float64); ok {
				if s >= 0.25 && s <= 4.0 {
					speed = s
				} else {
					log.Warn("Speed out of range, using default", "provided", s, "default", 1.0)
				}
			}

			// Get voice instructions from arguments or environment variable
			instructions := ""
			if inst, ok := arguments["instructions"].(string); ok && inst != "" {
				instructions = inst
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
				result := mcp.NewToolResultText("Error: OPENAI_API_KEY is not set")
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
			params := openai.AudioSpeechNewParams{
				Model: openai.SpeechModel(model),
				Input: text,
				Voice: openai.AudioSpeechNewParamsVoice(voice),
			}
			if speed != 1.0 {
				params.Speed = openai.Float(speed)
			}
			if instructions != "" {
				params.Instructions = openai.String(instructions)
			}

			response, err := client.Audio.Speech.New(ctx, params)
			if err != nil {
				log.Error("Failed to generate OpenAI TTS audio", "error", err)
				result := mcp.NewToolResultText(fmt.Sprintf("Error: Failed to generate TTS audio: %v", err))
				result.IsError = true
				return result, nil
			}
			defer response.Body.Close()

			log.Debug("Decoding MP3 stream from OpenAI")
			// OpenAI returns MP3 format by default
			streamer, format, err := mp3.Decode(response.Body)
			if err != nil {
				log.Error("Failed to decode OpenAI TTS response", "error", err)
				result := mcp.NewToolResultText(fmt.Sprintf("Error: Failed to decode response: %v", err))
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
					return mcp.NewToolResultText("Speech completed"), nil
				}
				return mcp.NewToolResultText(fmt.Sprintf("Speaking: %s (via OpenAI TTS with voice %s)", text, voice)), nil
			case <-ctx.Done():
				log.Debug("Context cancelled, stopping OpenAI TTS audio playback")
				speaker.Clear()
				log.Info("OpenAI TTS audio playback cancelled by user")
				return mcp.NewToolResultText("OpenAI TTS audio playback cancelled"), nil
			}
		}))

		log.Info("Starting MCP server", "name", "Say TTS Service", "version", Version)
		// Start the server using stdin/stdout
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		if err := ctrlc.Default.Run(ctx, func() error {
			if err := server.ServeStdio(s); err != nil {
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
