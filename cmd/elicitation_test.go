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

	t.Run("unavailable elicitation falls back to first provider", func(t *testing.T) {
		provider, cancelled, err := chooseProvider(providers, elicitationResult{Status: elicitUnavailable})
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

func TestIsUnsupportedElicitationError(t *testing.T) {
	assert.True(t, isUnsupportedElicitationError(errors.New("client does not support elicitation")))
	assert.True(t, isUnsupportedElicitationError(errors.New(`client does not support "form" elicitation`)))
	assert.False(t, isUnsupportedElicitationError(context.Canceled))
	assert.False(t, isUnsupportedElicitationError(errors.New("socket closed")))
}

func TestElicitationStopResult(t *testing.T) {
	t.Run("rejected elicitation returns cancellation text", func(t *testing.T) {
		result, stop := elicitationStopResult(elicitationResult{Status: elicitRejected}, "elicit provider selection")
		require.True(t, stop)
		require.NotNil(t, result)
		assert.False(t, result.IsError)
		assert.Equal(t, "Request cancelled", result.Content[0].(*mcp.TextContent).Text)
	})

	t.Run("cancellation returns non-error cancellation text", func(t *testing.T) {
		result, stop := elicitationStopResult(elicitationResult{
			Status: elicitFailed,
			Err:    context.Canceled,
		}, "elicit provider selection")
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

	t.Run("non-failure keeps the caller running", func(t *testing.T) {
		result, stop := elicitationStopResult(elicitationResult{Status: elicitUnavailable}, "elicit provider selection")
		assert.False(t, stop)
		assert.Nil(t, result)
	})
}
