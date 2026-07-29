package cmd

import (
	"context"
	"errors"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestChooseProvider(t *testing.T) {
	providers := []providerOption{
		{ID: ProviderSay, Name: "macOS Say"},
		{ID: ProviderGoogle, Name: "Google Gemini"},
	}

	t.Run("accepted selection uses requested provider", func(t *testing.T) {
		provider, cancelled, err := chooseProvider(providers, elicitationResult{
			Status:  elicitAccepted,
			Content: map[string]any{"provider": "Google Gemini"},
		})
		require.NoError(t, err)
		assert.False(t, cancelled)
		assert.Equal(t, ProviderGoogle, provider.ID)
	})

	t.Run("rejected selection cancels the flow", func(t *testing.T) {
		_, cancelled, err := chooseProvider(providers, elicitationResult{Status: elicitRejected})
		require.NoError(t, err)
		assert.True(t, cancelled)
	})

	t.Run("empty result falls back to first provider", func(t *testing.T) {
		provider, cancelled, err := chooseProvider(providers, elicitationResult{})
		require.NoError(t, err)
		assert.False(t, cancelled)
		assert.Equal(t, ProviderSay, provider.ID)
	})

	t.Run("accepted form without a selection errors", func(t *testing.T) {
		_, cancelled, err := chooseProvider(providers, elicitationResult{Status: elicitAccepted})
		require.Error(t, err)
		assert.False(t, cancelled)
		assert.Contains(t, err.Error(), "no TTS provider selected")
	})

	t.Run("elicitation failure is returned instead of falling back", func(t *testing.T) {
		expectedErr := errors.New("transport closed")
		_, cancelled, err := chooseProvider(providers, elicitationResult{
			Status: elicitFailed,
			Err:    expectedErr,
		})
		require.ErrorIs(t, err, expectedErr)
		assert.False(t, cancelled)
	})
}

func TestProviderSelectionSchema(t *testing.T) {
	schema := providerSelectionSchema([]providerOption{
		{ID: ProviderSay, Name: "macOS Say"},
		{ID: ProviderGoogle, Name: "Google Gemini"},
	})

	properties := schema["properties"].(map[string]any)
	provider := properties["provider"].(map[string]any)
	assert.Equal(t, []string{"macOS Say", "Google Gemini"}, provider["enum"])
	assert.Equal(t, []string{"provider"}, schema["required"])
}

func TestProviderRecommendationArgs(t *testing.T) {
	t.Run("say defaults survive accepted empty settings", func(t *testing.T) {
		args := providerRecommendationArgs(ProviderSay, "hello", nil)
		assert.Equal(t, "hello", args["text"])
		assert.Equal(t, DefaultSayRate, args["rate"])
		_, hasVoice := args["voice"]
		assert.False(t, hasVoice)
	})

	t.Run("google defaults survive accepted empty settings", func(t *testing.T) {
		args := providerRecommendationArgs(ProviderGoogle, "hello", nil)
		assert.Equal(t, "hello", args["text"])
		assert.Equal(t, DefaultGoogleVoice, args["voice"])
		assert.Equal(t, DefaultGoogleModel, args["model"])
	})

	t.Run("openai defaults survive accepted empty settings", func(t *testing.T) {
		args := providerRecommendationArgs(ProviderOpenAI, "hello", nil)
		assert.Equal(t, "hello", args["text"])
		assert.Equal(t, DefaultOpenAIVoice, args["voice"])
		assert.Equal(t, DefaultOpenAIModel, args["model"])
		assert.Equal(t, DefaultOpenAISpeed, args["speed"])
	})

	t.Run("provider overrides replace defaults", func(t *testing.T) {
		sayArgs := providerRecommendationArgs(ProviderSay, "hello", map[string]any{
			"rate":  float64(240),
			"voice": "Samantha",
		})
		assert.Equal(t, 240, sayArgs["rate"])
		assert.Equal(t, "Samantha", sayArgs["voice"])

		googleArgs := providerRecommendationArgs(ProviderGoogle, "hello", map[string]any{
			"voice": "Puck",
			"model": "gemini-2.5-pro-preview-tts",
		})
		assert.Equal(t, "Puck", googleArgs["voice"])
		assert.Equal(t, "gemini-2.5-pro-preview-tts", googleArgs["model"])

		openAIArgs := providerRecommendationArgs(ProviderOpenAI, "hello", map[string]any{
			"voice": "verse",
			"model": "tts-1-hd",
			"speed": 1.25,
		})
		assert.Equal(t, "verse", openAIArgs["voice"])
		assert.Equal(t, "tts-1-hd", openAIArgs["model"])
		assert.Equal(t, 1.25, openAIArgs["speed"])
	})
}

func TestSupportsFormElicitation(t *testing.T) {
	tests := []struct {
		name   string
		params *mcp.InitializeParams
		want   bool
	}{
		{name: "missing initialization"},
		{
			name:   "missing capabilities",
			params: &mcp.InitializeParams{},
		},
		{
			name: "missing elicitation capability",
			params: &mcp.InitializeParams{
				Capabilities: &mcp.ClientCapabilities{},
			},
		},
		{
			name: "modern form capability",
			params: &mcp.InitializeParams{
				ProtocolVersion: "2025-11-25",
				Capabilities: &mcp.ClientCapabilities{
					Elicitation: &mcp.ElicitationCapabilities{
						Form: &mcp.FormElicitationCapabilities{},
					},
				},
			},
			want: true,
		},
		{
			name: "modern URL-only capability",
			params: &mcp.InitializeParams{
				ProtocolVersion: "2025-11-25",
				Capabilities: &mcp.ClientCapabilities{
					Elicitation: &mcp.ElicitationCapabilities{
						URL: &mcp.URLElicitationCapabilities{},
					},
				},
			},
		},
		{
			name: "modern empty capability means form",
			params: &mcp.InitializeParams{
				ProtocolVersion: "2025-11-25",
				Capabilities: &mcp.ClientCapabilities{
					Elicitation: &mcp.ElicitationCapabilities{},
				},
			},
			want: true,
		},
		{
			name: "legacy empty capability means form",
			params: &mcp.InitializeParams{
				ProtocolVersion: "2024-11-05",
				Capabilities: &mcp.ClientCapabilities{
					Elicitation: &mcp.ElicitationCapabilities{},
				},
			},
			want: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			assert.Equal(t, test.want, supportsFormElicitation(test.params))
		})
	}
}

func TestFormElicitationRequest(t *testing.T) {
	schema := map[string]any{"type": "object"}
	result := formElicitationRequest("voice", "say/settings", "Choose a voice", schema)

	assert.Equal(t, "say/settings", result.RequestState)
	require.Len(t, result.InputRequests, 1)
	request, ok := result.InputRequests["voice"].(*mcp.ElicitParams)
	require.True(t, ok)
	assert.Equal(t, "form", request.Mode)
	assert.Equal(t, "Choose a voice", request.Message)
	assert.Equal(t, schema, request.RequestedSchema)
}

func TestFormElicitationResponse(t *testing.T) {
	request := func(response mcp.InputResponse) *mcp.CallToolRequest {
		return &mcp.CallToolRequest{
			Params: &mcp.CallToolParamsRaw{
				InputResponses: mcp.InputResponseMap{"voice": response},
			},
		}
	}

	t.Run("accepted response returns content", func(t *testing.T) {
		content := map[string]any{"voice": "Samantha"}
		result := formElicitationResponse(request(&mcp.ElicitResult{
			Action:  "accept",
			Content: content,
		}), "voice")
		assert.True(t, result.Accepted())
		assert.Equal(t, content, result.Content)
	})

	for _, action := range []string{"decline", "cancel"} {
		t.Run(action+" response rejects the request", func(t *testing.T) {
			result := formElicitationResponse(request(&mcp.ElicitResult{Action: action}), "voice")
			assert.True(t, result.Rejected())
		})
	}

	t.Run("missing response fails", func(t *testing.T) {
		result := formElicitationResponse(&mcp.CallToolRequest{
			Params: &mcp.CallToolParamsRaw{},
		}, "voice")
		assert.True(t, result.Failed())
		assert.ErrorContains(t, result.Err, `missing elicitation response "voice"`)
	})

	t.Run("unexpected response type fails", func(t *testing.T) {
		result := formElicitationResponse(request(&mcp.ListRootsResult{}), "voice")
		assert.True(t, result.Failed())
		assert.ErrorContains(t, result.Err, "unexpected elicitation response type")
	})

	t.Run("unexpected action fails", func(t *testing.T) {
		result := formElicitationResponse(request(&mcp.ElicitResult{Action: "unknown"}), "voice")
		assert.True(t, result.Failed())
		assert.ErrorContains(t, result.Err, `unexpected elicitation action "unknown"`)
	})
}

func TestElicitationStopResult(t *testing.T) {
	t.Run("rejected elicitation returns cancellation text", func(t *testing.T) {
		result, stop := elicitationStopResult(elicitationResult{Status: elicitRejected}, "elicit provider selection")
		require.True(t, stop)
		require.NotNil(t, result)
		assert.False(t, result.IsError)
		assert.Equal(t, "Request cancelled", result.Content[0].(*mcp.TextContent).Text)
	})

	t.Run("runtime failure returns an error result", func(t *testing.T) {
		result, stop := elicitationStopResult(elicitationResult{
			Status: elicitFailed,
			Err:    errors.New("transport closed"),
		}, "elicit provider selection")
		require.True(t, stop)
		require.NotNil(t, result)
		assert.True(t, result.IsError)
		assert.Contains(t, result.Content[0].(*mcp.TextContent).Text, "Failed to elicit provider selection")
	})

	t.Run("context cancellation returns non-error cancellation text", func(t *testing.T) {
		result, stop := elicitationStopResult(elicitationResult{
			Status: elicitFailed,
			Err:    context.Canceled,
		}, "elicit provider selection")
		require.True(t, stop)
		require.NotNil(t, result)
		assert.False(t, result.IsError)
		assert.Equal(t, "Request cancelled", result.Content[0].(*mcp.TextContent).Text)
	})

	t.Run("non-failure keeps the caller running", func(t *testing.T) {
		result, stop := elicitationStopResult(elicitationResult{}, "elicit provider selection")
		assert.False(t, stop)
		assert.Nil(t, result)
	})
}
