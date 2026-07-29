/*
Copyright © 2026 blacktop

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
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"

	"github.com/charmbracelet/log"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const voiceSayBinary = "voice-say"

var (
	voiceSayLookPath = exec.LookPath
	voiceSayRunner   = runVoiceSayCommand
	voiceTTSLock     = acquireGlobalTTSLock
)

type VoiceTTSParams struct {
	Text     string  `json:"text" mcp:"The text to speak aloud locally with Voice"`
	Voice    *string `json:"voice,omitempty,omitzero" mcp:"Preset voice: Ryan or Aiden"`
	Tier     *string `json:"tier,omitempty,omitzero" mcp:"Model tier: small or large"`
	Style    *string `json:"style,omitempty,omitzero" mcp:"Free-text delivery style"`
	Describe *string `json:"describe,omitempty,omitzero" mcp:"Free-text voice description; conflicts with voice and forces the 1.7B model"`
}

func buildVoiceTTSSchema() json.RawMessage {
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"text": map[string]any{
				"type":        "string",
				"description": "The text to speak aloud locally with Voice",
			},
			"voice": map[string]any{
				"type":        "string",
				"description": "Preset voice. Conflicts with describe.",
				"enum":        []string{"Ryan", "Aiden"},
			},
			"tier": map[string]any{
				"type":        "string",
				"description": "Model tier. small is faster; large is higher quality.",
				"enum":        []string{"small", "large"},
			},
			"style": map[string]any{
				"type":        "string",
				"description": "Free-text delivery style, for example \"calm and unhurried\".",
			},
			"describe": map[string]any{
				"type":        "string",
				"description": "Create a voice from a description. Conflicts with voice and always uses the 1.7B VoiceDesign model.",
			},
		},
		"required": []string{"text"},
	}
	data, err := json.Marshal(schema)
	if err != nil {
		panic(fmt.Sprintf("failed to marshal voice_tts schema: %v", err))
	}
	return data
}

func registerVoiceTTSTool(server *mcp.Server) {
	binaryPath, err := voiceSayLookPath(voiceSayBinary)
	if err != nil || binaryPath == "" {
		return
	}

	tool := &mcp.Tool{
		Name:  "voice_tts",
		Title: "Voice Local TTS",
		Description: "Speaks with local Qwen3-TTS models through MLX and requires no API key. " +
			"Model startup takes several seconds, so prefer it for summaries and announcements rather than time-critical alerts. " +
			"Playback is direct; server audio-file saving is unavailable.",
		InputSchema: buildVoiceTTSSchema(),
		Annotations: &mcp.ToolAnnotations{
			Title:          "Voice Local Text-to-Speech",
			ReadOnlyHint:   false,
			IdempotentHint: false,
		},
	}

	mcp.AddTool(server, tool, func(
		ctx context.Context,
		_ *mcp.CallToolRequest,
		input VoiceTTSParams,
	) (*mcp.CallToolResult, any, error) {
		return callVoiceTTS(ctx, binaryPath, input), nil, nil
	})
}

func callVoiceTTS(ctx context.Context, binaryPath string, input VoiceTTSParams) *mcp.CallToolResult {
	select {
	case <-ctx.Done():
		return textResult("Request cancelled")
	default:
	}

	args, err := voiceSayArgs(input)
	if err != nil {
		return errorResult(fmt.Sprintf("Error: %v", err))
	}
	if !shouldPlay() {
		return errorResult("Error: voice_tts cannot run with --no-play because voice-say does not expose audio-file output")
	}

	release, err := voiceTTSLock(ctx)
	if err != nil {
		if ctx.Err() != nil {
			return textResult("Request cancelled")
		}
		return errorResult(fmt.Sprintf("Error: Failed to acquire TTS lock: %v", err))
	}
	defer release()

	log.Debug("Voice TTS tool called", "path", binaryPath, "args", args)
	if err := voiceSayRunner(ctx, binaryPath, args, input.Text); err != nil {
		if ctx.Err() != nil {
			return textResult("Request cancelled")
		}
		return errorResult(fmt.Sprintf("Error: voice-say failed: %v", err))
	}

	return textResult(formatSaveResult(input.Text, "", true))
}

func voiceSayArgs(input VoiceTTSParams) ([]string, error) {
	if input.Text == "" {
		return nil, fmt.Errorf("empty text provided")
	}
	if input.Voice != nil && input.Describe != nil {
		return nil, fmt.Errorf("voice and describe cannot be used together")
	}

	args := []string{"--quiet", "--wait"}
	if input.Voice != nil {
		switch *input.Voice {
		case "Ryan", "Aiden":
			args = append(args, "--voice", *input.Voice)
		default:
			return nil, fmt.Errorf("unsupported voice %q; expected Ryan or Aiden", *input.Voice)
		}
	}
	if input.Tier != nil {
		switch *input.Tier {
		case "small", "large":
			args = append(args, "--tier", *input.Tier)
		default:
			return nil, fmt.Errorf("unsupported tier %q; expected small or large", *input.Tier)
		}
	}
	if input.Style != nil {
		args = append(args, "--style", *input.Style)
	}
	if input.Describe != nil {
		args = append(args, "--describe", *input.Describe)
	}
	return args, nil
}

func runVoiceSayCommand(ctx context.Context, binaryPath string, args []string, text string) error {
	command := exec.CommandContext(ctx, binaryPath, args...)
	command.Stdin = strings.NewReader(text)
	output, err := command.CombinedOutput()
	if err == nil {
		return nil
	}
	if detail := strings.TrimSpace(string(output)); detail != "" {
		return fmt.Errorf("%w: %s", err, detail)
	}
	return err
}
