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
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/charmbracelet/log"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// canElicit returns true if the request has a session capable of elicitation.
func canElicit(req *mcp.CallToolRequest) bool {
	return req != nil && req.Session != nil
}

type elicitationStatus uint8

const (
	elicitUnavailable elicitationStatus = iota
	elicitAccepted
	elicitRejected
	elicitFailed
)

type elicitationResult struct {
	Status  elicitationStatus
	Content map[string]any
	Err     error
}

func (r elicitationResult) Accepted() bool {
	return r.Status == elicitAccepted
}

func (r elicitationResult) Rejected() bool {
	return r.Status == elicitRejected
}

func (r elicitationResult) Failed() bool {
	return r.Status == elicitFailed
}

func (r elicitationResult) Cancelled() bool {
	return errors.Is(r.Err, context.Canceled) || errors.Is(r.Err, context.DeadlineExceeded)
}

func isUnsupportedElicitationError(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "does not support") && strings.Contains(msg, "elicitation")
}

// elicitForm sends a form elicitation request and classifies the outcome.
// Unsupported clients return unavailable, explicit user decline/cancel returns
// rejected, and transport/runtime errors return failed.
func elicitForm(
	ctx context.Context,
	session *mcp.ServerSession,
	message string,
	schema map[string]any,
) elicitationResult {
	if session == nil {
		return elicitationResult{Status: elicitUnavailable}
	}

	result, err := session.Elicit(ctx, &mcp.ElicitParams{
		Message:         message,
		RequestedSchema: schema,
	})
	if err != nil {
		if isUnsupportedElicitationError(err) {
			log.Debug("Elicitation not available", "error", err)
			return elicitationResult{Status: elicitUnavailable}
		}
		log.Warn("Elicitation failed", "error", err, "message", message)
		return elicitationResult{Status: elicitFailed, Err: err}
	}
	if result.Action != "accept" {
		log.Debug("User declined elicitation", "action", result.Action)
		return elicitationResult{Status: elicitRejected}
	}
	return elicitationResult{
		Status:  elicitAccepted,
		Content: result.Content,
	}
}

func elicitString(content map[string]any, key string) string {
	if content == nil {
		return ""
	}
	v, ok := content[key]
	if !ok {
		return ""
	}
	s, ok := v.(string)
	if !ok {
		return ""
	}
	return s
}

func elicitInt(content map[string]any, key string) (int, bool) {
	if content == nil {
		return 0, false
	}
	v, ok := content[key]
	if !ok {
		return 0, false
	}
	switch n := v.(type) {
	case float64:
		return int(n), true
	case int:
		return n, true
	}
	return 0, false
}

func elicitFloat64(content map[string]any, key string) (float64, bool) {
	if content == nil {
		return 0, false
	}
	v, ok := content[key]
	if !ok {
		return 0, false
	}
	f, ok := v.(float64)
	if !ok {
		return 0, false
	}
	return f, true
}

func chooseProvider(providers []providerOption, result elicitationResult) (providerOption, bool, error) {
	if len(providers) == 0 {
		return providerOption{}, false, fmt.Errorf("no TTS providers configured")
	}
	if result.Failed() {
		return providerOption{}, false, result.Err
	}
	if result.Rejected() {
		return providerOption{}, true, nil
	}

	selected := elicitString(result.Content, "provider")
	if selected == "" {
		if result.Accepted() {
			return providerOption{}, false, fmt.Errorf("no TTS provider selected")
		}
		return providers[0], false, nil
	}

	for _, provider := range providers {
		if provider.Name == selected {
			return provider, false, nil
		}
	}

	return providerOption{}, false, fmt.Errorf("unsupported TTS provider selection: %s", selected)
}

func providerSelectionSchema(providers []providerOption) map[string]any {
	enumNames := make([]string, len(providers))
	for i, provider := range providers {
		enumNames[i] = provider.Name
	}

	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"provider": map[string]any{
				"type":        "string",
				"title":       "TTS Provider",
				"description": "Choose your preferred TTS provider",
				"enum":        enumNames,
			},
		},
		"required": []string{"provider"},
	}
}

// Elicitation schemas for each provider's settings

func saySettingsSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"voice": map[string]any{
				"type":        "string",
				"title":       "Voice",
				"description": "macOS voice (e.g. Alex, Samantha, Victoria, Daniel)",
			},
			"rate": map[string]any{
				"type":        "integer",
				"title":       "Speech Rate (WPM)",
				"description": "Words per minute, 50-500 (default: 200)",
			},
		},
	}
}

func googleSettingsSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"voice": map[string]any{
				"type":  "string",
				"title": "Voice",
				"enum":  GoogleVoices,
			},
			"model": map[string]any{
				"type":  "string",
				"title": "Model",
				"enum":  GoogleModels,
			},
		},
	}
}

func openAISettingsSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"voice": map[string]any{
				"type":  "string",
				"title": "Voice",
				"enum":  OpenAIVoices,
			},
			"model": map[string]any{
				"type":  "string",
				"title": "Model",
				"enum":  OpenAIModels,
			},
			"speed": map[string]any{
				"type":        "number",
				"title":       "Speed",
				"description": "0.25-4.0 (default: 1.0)",
			},
		},
	}
}

func settingsSchemaForProvider(providerID string) map[string]any {
	switch providerID {
	case ProviderSay:
		return saySettingsSchema()
	case ProviderGoogle:
		return googleSettingsSchema()
	case ProviderOpenAI:
		return openAISettingsSchema()
	default:
		return nil
	}
}

func applySaySettings(input *SayTTSParams, content map[string]any) {
	if input == nil {
		return
	}
	if v := elicitString(content, "voice"); v != "" {
		input.Voice = &v
	}
	if r, ok := elicitInt(content, "rate"); ok {
		input.Rate = &r
	}
}

func applyGoogleSettings(input *GoogleTTSParams, content map[string]any) {
	if input == nil {
		return
	}
	if v := elicitString(content, "voice"); v != "" {
		input.Voice = &v
	}
	if m := elicitString(content, "model"); m != "" {
		input.Model = &m
	}
}

func applyOpenAISettings(input *OpenAITTSParams, content map[string]any) {
	if input == nil {
		return
	}
	if v := elicitString(content, "voice"); v != "" {
		input.Voice = &v
	}
	if m := elicitString(content, "model"); m != "" {
		input.Model = &m
	}
	if s, ok := elicitFloat64(content, "speed"); ok {
		input.Speed = &s
	}
}

func sayRecommendationArgs(input SayTTSParams) map[string]any {
	args := map[string]any{
		"text": input.Text,
		"rate": DefaultSayRate,
	}
	if input.Rate != nil {
		args["rate"] = *input.Rate
	}
	if input.Voice != nil && *input.Voice != "" {
		args["voice"] = *input.Voice
	}
	return args
}

func googleRecommendationArgs(input GoogleTTSParams) map[string]any {
	args := map[string]any{
		"text":  input.Text,
		"voice": DefaultGoogleVoice,
		"model": DefaultGoogleModel,
	}
	if input.Voice != nil && *input.Voice != "" {
		args["voice"] = *input.Voice
	}
	if input.Model != nil && *input.Model != "" {
		args["model"] = *input.Model
	}
	return args
}

func openAIRecommendationArgs(input OpenAITTSParams) map[string]any {
	args := map[string]any{
		"text":  input.Text,
		"voice": DefaultOpenAIVoice,
		"model": DefaultOpenAIModel,
		"speed": DefaultOpenAISpeed,
	}
	if input.Voice != nil && *input.Voice != "" {
		args["voice"] = *input.Voice
	}
	if input.Model != nil && *input.Model != "" {
		args["model"] = *input.Model
	}
	if input.Speed != nil {
		args["speed"] = *input.Speed
	}
	if input.Instructions != nil && *input.Instructions != "" {
		args["instructions"] = *input.Instructions
	}
	return args
}

func providerRecommendationArgs(providerID, text string, content map[string]any) map[string]any {
	switch providerID {
	case ProviderSay:
		input := SayTTSParams{Text: text}
		applySaySettings(&input, content)
		return sayRecommendationArgs(input)
	case ProviderGoogle:
		input := GoogleTTSParams{Text: text}
		applyGoogleSettings(&input, content)
		return googleRecommendationArgs(input)
	case ProviderOpenAI:
		input := OpenAITTSParams{Text: text}
		applyOpenAISettings(&input, content)
		return openAIRecommendationArgs(input)
	default:
		return map[string]any{"text": text}
	}
}

func elicitationStopResult(result elicitationResult, action string) (*mcp.CallToolResult, bool) {
	if result.Rejected() {
		return textResult("Request cancelled"), true
	}
	if !result.Failed() {
		return nil, false
	}
	if result.Cancelled() {
		return textResult("Request cancelled"), true
	}
	return errorResult(fmt.Sprintf("Error: Failed to %s: %v", action, result.Err)), true
}

func maybeElicitContent(
	ctx context.Context,
	req *mcp.CallToolRequest,
	action string,
	message string,
	schema map[string]any,
) (map[string]any, *mcp.CallToolResult, bool) {
	if !canElicit(req) {
		return nil, nil, false
	}

	result := elicitForm(ctx, req.Session, message, schema)
	if stopResult, stop := elicitationStopResult(result, action); stop {
		return nil, stopResult, true
	}

	return result.Content, nil, false
}

// buildProviderRecommendation formats a structured recommendation
// for the LLM to call the specific provider tool.
func buildProviderRecommendation(toolID, displayName string, args map[string]any) string {
	b, _ := json.Marshal(args)
	return fmt.Sprintf(
		"User selected %s. Please call %s with arguments: %s",
		displayName, toolID, string(b),
	)
}

func buildTTSSchema() json.RawMessage {
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"text": map[string]any{
				"type":        "string",
				"description": "The text to speak aloud",
			},
		},
		"required": []string{"text"},
	}
	data, err := json.Marshal(schema)
	if err != nil {
		panic(fmt.Sprintf("failed to marshal tts schema: %v", err))
	}
	return data
}
