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

const (
	multiRoundTripProtocolVersion = "2026-07-28"

	settingsInputID = "settings"

	saySettingsState    = "say_tts/settings"
	googleSettingsState = "google_tts/settings"
	openAISettingsState = "openai_tts/settings"

	ttsProviderInputID     = "provider"
	ttsProviderState       = "tts/provider"
	ttsSettingsStatePrefix = "tts/settings/"
	ttsSettingsMessage     = "Configure voice settings (or accept defaults):"
)

// canElicit returns true if elicitation is enabled and the request has a
// session that advertises form elicitation. Elicitation is opt-in (--elicit /
// MCP_TTS_ELICIT) because it interrupts the normal agent flow of calling tools
// with explicit arguments.
func canElicit(req *mcp.CallToolRequest) bool {
	if !elicitEnabled || req == nil || req.Session == nil {
		return false
	}
	return supportsFormElicitation(req.Session.InitializeParams())
}

func supportsFormElicitation(params *mcp.InitializeParams) bool {
	if params == nil || params.Capabilities == nil || params.Capabilities.Elicitation == nil {
		return false
	}

	// The SDK treats an empty elicitation capability as form support for
	// backward compatibility. A URL-only capability explicitly excludes it.
	elicitation := params.Capabilities.Elicitation
	return elicitation.Form != nil || elicitation.URL == nil
}

func supportsMultiRoundTrip(req *mcp.CallToolRequest) bool {
	if req == nil || req.Session == nil {
		return false
	}
	params := req.Session.InitializeParams()
	return params != nil && params.ProtocolVersion >= multiRoundTripProtocolVersion
}

type elicitationStatus uint8

const (
	elicitAccepted elicitationStatus = iota + 1
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

func formElicitationRequest(
	inputID string,
	requestState string,
	message string,
	schema map[string]any,
) *mcp.CallToolResult {
	return &mcp.CallToolResult{
		InputRequests: mcp.InputRequestMap{
			inputID: &mcp.ElicitParams{
				Mode:            "form",
				Message:         message,
				RequestedSchema: schema,
			},
		},
		RequestState: requestState,
	}
}

func formElicitationResponse(req *mcp.CallToolRequest, inputID string) elicitationResult {
	if req == nil || req.Params == nil {
		return elicitationResult{
			Status: elicitFailed,
			Err:    fmt.Errorf("missing tool request parameters"),
		}
	}

	response, ok := req.Params.InputResponses[inputID]
	if !ok {
		return elicitationResult{
			Status: elicitFailed,
			Err:    fmt.Errorf("missing elicitation response %q", inputID),
		}
	}
	result, ok := response.(*mcp.ElicitResult)
	if !ok || result == nil {
		return elicitationResult{
			Status: elicitFailed,
			Err:    fmt.Errorf("unexpected elicitation response type %T for %q", response, inputID),
		}
	}

	return elicitationResponse(result)
}

func elicitationResponse(result *mcp.ElicitResult) elicitationResult {
	if result == nil {
		return elicitationResult{
			Status: elicitFailed,
			Err:    fmt.Errorf("missing elicitation result"),
		}
	}

	switch result.Action {
	case "accept":
		return elicitationResult{
			Status:  elicitAccepted,
			Content: result.Content,
		}
	case "decline", "cancel":
		log.Debug("User declined elicitation", "action", result.Action)
		return elicitationResult{Status: elicitRejected}
	default:
		return elicitationResult{
			Status: elicitFailed,
			Err:    fmt.Errorf("unexpected elicitation action %q", result.Action),
		}
	}
}

func legacyElicitForm(
	ctx context.Context,
	req *mcp.CallToolRequest,
	message string,
	schema map[string]any,
) elicitationResult {
	if req == nil || req.Session == nil {
		return elicitationResult{
			Status: elicitFailed,
			Err:    fmt.Errorf("missing MCP session"),
		}
	}
	if supportsMultiRoundTrip(req) {
		return elicitationResult{
			Status: elicitFailed,
			Err:    fmt.Errorf("legacy elicitation attempted with a multi-round-trip client"),
		}
	}

	result, err := req.Session.Elicit(ctx, &mcp.ElicitParams{
		Mode:            "form",
		Message:         message,
		RequestedSchema: schema,
	})
	if err != nil {
		return elicitationResult{Status: elicitFailed, Err: err}
	}
	return elicitationResponse(result)
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
	req *mcp.CallToolRequest,
	inputID string,
	requestState string,
	action string,
	message string,
	schema map[string]any,
) (map[string]any, *mcp.CallToolResult, bool) {
	if !canElicit(req) {
		return nil, nil, false
	}

	if req.Params == nil {
		return nil, errorResult(fmt.Sprintf("Error: Failed to %s: missing tool request parameters", action)), true
	}
	if req.Params.RequestState == "" {
		return nil, formElicitationRequest(inputID, requestState, message, schema), true
	}
	if req.Params.RequestState != requestState {
		return nil, errorResult(fmt.Sprintf(
			"Error: Failed to %s: unexpected request state %q",
			action,
			req.Params.RequestState,
		)), true
	}

	result := formElicitationResponse(req, inputID)
	if stopResult, stop := elicitationStopResult(result, action); stop {
		return nil, stopResult, true
	}

	return result.Content, nil, false
}

func providerByID(providers []providerOption, providerID string) (providerOption, bool) {
	for _, provider := range providers {
		if provider.ID == providerID {
			return provider, true
		}
	}
	return providerOption{}, false
}

func providerRecommendationResult(
	text string,
	provider providerOption,
	content map[string]any,
) *mcp.CallToolResult {
	return textResult(buildProviderRecommendation(
		provider.ID,
		provider.Name,
		providerRecommendationArgs(provider.ID, text, content),
	))
}

func ttsSettingsOrRecommendation(text string, provider providerOption) *mcp.CallToolResult {
	settingsSchema := settingsSchemaForProvider(provider.ID)
	if settingsSchema == nil {
		return providerRecommendationResult(text, provider, nil)
	}

	return formElicitationRequest(
		settingsInputID,
		ttsSettingsStatePrefix+provider.ID,
		ttsSettingsMessage,
		settingsSchema,
	)
}

func legacyTTSSettingsOrRecommendation(
	ctx context.Context,
	req *mcp.CallToolRequest,
	text string,
	provider providerOption,
) *mcp.CallToolResult {
	settingsSchema := settingsSchemaForProvider(provider.ID)
	if settingsSchema == nil {
		return providerRecommendationResult(text, provider, nil)
	}

	settings := legacyElicitForm(
		ctx,
		req,
		ttsSettingsMessage,
		settingsSchema,
	)
	if result, stop := elicitationStopResult(settings, "elicit TTS voice settings"); stop {
		return result
	}
	return providerRecommendationResult(text, provider, settings.Content)
}

func interactiveTTSResult(
	ctx context.Context,
	req *mcp.CallToolRequest,
	text string,
	providers []providerOption,
) *mcp.CallToolResult {
	if len(providers) == 0 {
		return errorResult("Error: No TTS providers configured")
	}
	if !canElicit(req) {
		return providerRecommendationResult(text, providers[0], nil)
	}
	if req.Params == nil {
		return errorResult("Error: Failed to elicit TTS settings: missing tool request parameters")
	}

	switch {
	case req.Params.RequestState == "":
		if len(providers) == 1 {
			return ttsSettingsOrRecommendation(text, providers[0])
		}
		return formElicitationRequest(
			ttsProviderInputID,
			ttsProviderState,
			"Which TTS provider would you like to use?",
			providerSelectionSchema(providers),
		)

	case req.Params.RequestState == ttsProviderState:
		selection := formElicitationResponse(req, ttsProviderInputID)
		if result, stop := elicitationStopResult(selection, "elicit TTS provider selection"); stop {
			return result
		}
		provider, cancelled, err := chooseProvider(providers, selection)
		if err != nil {
			return errorResult(fmt.Sprintf("Error: %v", err))
		}
		if cancelled {
			return textResult("Request cancelled")
		}
		if !supportsMultiRoundTrip(req) {
			// The SDK's compatibility middleware reinvokes legacy handlers only
			// once. Complete the second step with the legacy nested request API;
			// modern clients never enter this branch.
			return legacyTTSSettingsOrRecommendation(ctx, req, text, provider)
		}
		return ttsSettingsOrRecommendation(text, provider)

	case strings.HasPrefix(req.Params.RequestState, ttsSettingsStatePrefix):
		providerID := strings.TrimPrefix(req.Params.RequestState, ttsSettingsStatePrefix)
		provider, ok := providerByID(providers, providerID)
		if !ok {
			return errorResult(fmt.Sprintf(
				"Error: TTS provider %q is no longer available",
				providerID,
			))
		}

		settings := formElicitationResponse(req, settingsInputID)
		if result, stop := elicitationStopResult(settings, "elicit TTS voice settings"); stop {
			return result
		}
		return providerRecommendationResult(text, provider, settings.Content)

	default:
		return errorResult(fmt.Sprintf(
			"Error: Unexpected interactive TTS request state %q",
			req.Params.RequestState,
		))
	}
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
