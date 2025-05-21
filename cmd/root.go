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
	"github.com/spf13/cobra"
	"golang.org/x/sync/errgroup"
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
			// Add the "say" tool
			sayTool := mcp.NewTool("say",
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

		elevenLabsTool := mcp.NewTool("elevenlabs",
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
					return fmt.Errorf("failed to create request: %v", err)
				}

				req.Header.Set("xi-api-key", apiKey)
				req.Header.Set("Content-Type", "application/json")
				req.Header.Set("accept", "audio/mpeg")

				safeLog("Sending HTTP request", req)
				res, err := http.DefaultClient.Do(req)
				if err != nil {
					log.Error("Failed to send request", "error", err)
					return fmt.Errorf("failed to send request: %v", err)
				}
				defer res.Body.Close()

				if res.StatusCode != http.StatusOK {
					log.Error("Request failed", "status", res.Status)
					return fmt.Errorf("failed to send request: %v", res.Status)
				}

				log.Debug("Copying response body to pipe")
				bytesWritten, err := io.Copy(pipeWriter, res.Body)
				log.Debug("Response body copied", "bytes", bytesWritten)
				return err
			})

			// Start a goroutine to check for any errors from the errgroup
			errCh := make(chan error, 1)
			go func() {
				errCh <- g.Wait()
			}()

			// Check for any immediate errors before proceeding
			select {
			case err := <-errCh:
				if err != nil {
					log.Error("Error from goroutine", "error", err)
					result := mcp.NewToolResultText(fmt.Sprintf("Error: %v", err))
					result.IsError = true
					return result, nil
				}
			case <-time.After(100 * time.Millisecond):
				log.Debug("No immediate error from goroutine, continuing")
				// Continue if no immediate error
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

			return mcp.NewToolResultText(fmt.Sprintf("Speaking: %s", text)), nil
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
