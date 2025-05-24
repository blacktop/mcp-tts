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
)

var (
	verbose bool
	logger  *log.Logger
	// Version stores the service's version
	Version string
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
}

// rootCmd represents the base command when called without any subcommands
var rootCmd = &cobra.Command{
	Use:   "mcp-say",
	Short: "TTS (text-to-speech) MCP Server",
	Long: `TTS (text-to-speech) MCP Server.

Provides a text-to-speech service using the MacOS 'say' command.

Designed to be used with the MCP protocol.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		if verbose {
			log.SetLevel(log.DebugLevel)
		}

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

			return mcp.NewGetPromptResult(
				"Speaking text",
				[]mcp.PromptMessage{
					mcp.NewPromptMessage(
						mcp.RoleUser,
						mcp.NewTextContent(fmt.Sprintf("Speaking: %s", text)),
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
			s.AddTool(sayTool, func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
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
				// Execute the say command
				sayCmd := exec.Command("/usr/bin/say", args...)
				if err := sayCmd.Start(); err != nil {
					log.Error("Failed to start say command", "error", err)
					result := mcp.NewToolResultText(fmt.Sprintf("Error: Failed to start say command: %v", err))
					result.IsError = true
					return result, nil
				}

				log.Info("Speaking text", "text", text)
				return mcp.NewToolResultText(fmt.Sprintf("Speaking: %s", text)), nil
			})
		}

		elevenLabsTool := mcp.NewTool("elevenlabs_tts",
			mcp.WithDescription("Uses the ElevenLabs API to generate speech from text"),
			mcp.WithString("text",
				mcp.Required(),
				mcp.Description("The text to be spoken"),
			),
		)

		s.AddTool(elevenLabsTool, func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
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

			log.Debug("Decoding MP3 stream")
			streamer, format, err := mp3.Decode(pipeReader)
			if err != nil {
				log.Error("Failed to decode response", "error", err)
				result := mcp.NewToolResultText(fmt.Sprintf("Error: Failed to decode response: %v", err))
				result.IsError = true
				return result, nil
			}
			defer streamer.Close()

			log.Debug("Initializing speaker", "sampleRate", format.SampleRate)
			speaker.Init(format.SampleRate, format.SampleRate.N(time.Second/10))
			done := make(chan bool)
			speaker.Play(beep.Seq(streamer, beep.Callback(func() {
				done <- true
			})))

			log.Info("Speaking text via ElevenLabs", "text", text)
			<-done
			log.Debug("Finished speaking")

			// Check for any errors that occurred during streaming
			if err := g.Wait(); err != nil {
				log.Error("Error occurred during streaming", "error", err)
				result := mcp.NewToolResultText(fmt.Sprintf("Error: %v", err))
				result.IsError = true
				return result, nil
			}

			return mcp.NewToolResultText(fmt.Sprintf("Speaking: %s", text)), nil
		})

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

		s.AddTool(googleTTSTool, func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
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

			// Play the audio
			done := make(chan bool)
			speaker.Play(beep.Seq(pcmStream, beep.Callback(func() {
				done <- true
			})))

			// Wait for playback to complete
			<-done

			log.Info("Speaking via Google TTS", "text", text, "voice", voice, "model", model)
			return mcp.NewToolResultText(fmt.Sprintf("Speaking: %s (via Google TTS with voice %s)", text, voice)), nil
		})

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

		s.AddTool(openaiTTSTool, func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
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
			// Wait for playback to complete
			<-done
			log.Debug("Finished speaking via OpenAI TTS")

			return mcp.NewToolResultText(fmt.Sprintf("Speaking: %s (via OpenAI TTS with voice %s)", text, voice)), nil
		})

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
