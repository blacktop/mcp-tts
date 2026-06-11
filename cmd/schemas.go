package cmd

import (
	"encoding/json"
	"fmt"
)

// Custom schema builders that create LM Studio-compatible schemas
// These avoid using complex additionalProperties objects
// Returns json.RawMessage that can be used directly as Tool.InputSchema

func buildSayTTSSchema() json.RawMessage {
	// Note: AdditionalProperties behavior is handled by the MCP SDK
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"text": map[string]any{
				"type":        "string",
				"description": "The text to speak aloud",
			},
			"rate": map[string]any{
				// No JSON-schema "default" here on purpose: the SDK injects schema
				// defaults into the decoded struct, which would make Rate non-nil
				// and defeat the "all optional fields unset" elicitation check.
				// The handler applies DefaultSayRate when Rate is nil.
				"type":        "integer",
				"description": "Speech rate in words per minute. RECOMMENDED: 200-250 for natural speech. Only increase to 275-300 if user explicitly requests faster speech. Do NOT set above 300 unless specifically asked. (default: 200)",
				"minimum":     50,
				"maximum":     500,
			},
			"voice": map[string]any{
				// No enum: macOS hosts have any of ~180 voices (legacy like
				// "Samantha"/"Alex" plus downloadable Premium/Enhanced ones). An
				// enum would reject installed voices and offer un-downloaded ones.
				// The handler validates the name and checks IsVoiceInstalled.
				"type":        "string",
				"description": "Voice to use for speech synthesis (e.g. 'Samantha', 'Alex', 'Daniel'). Leave unset to use the host's configured System Voice.",
			},
		},
		"required": []string{"text"},
	}
	data, err := json.Marshal(schema)
	if err != nil {
		// This should never happen with our simple map structure, but handle it defensively
		panic(fmt.Sprintf("failed to marshal say_tts schema: %v", err))
	}
	return data
}

func buildElevenLabsTTSSchema() json.RawMessage {
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"text": map[string]any{
				"type":        "string",
				"description": "The text to convert to speech using ElevenLabs API",
			},
		},
		"required": []string{"text"},
	}
	data, err := json.Marshal(schema)
	if err != nil {
		panic(fmt.Sprintf("failed to marshal elevenlabs_tts schema: %v", err))
	}
	return data
}

func buildSixtyDBTTSSchema() json.RawMessage {
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"text": map[string]any{
				"type":        "string",
				"description": "The text to convert to speech using the 60dB API",
			},
		},
		"required": []string{"text"},
	}
	data, err := json.Marshal(schema)
	if err != nil {
		panic(fmt.Sprintf("failed to marshal sixtydb_tts schema: %v", err))
	}
	return data
}

func buildGoogleTTSSchema() json.RawMessage {
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"text": map[string]any{
				"type":        "string",
				"description": "The text to convert to speech using Google TTS",
			},
			"voice": map[string]any{
				"type":        "string",
				"description": "Voice name to use (default: 'Kore')",
				"enum":        GoogleVoices,
			},
			"model": map[string]any{
				"type":        "string",
				"description": "TTS model to use (default: 'gemini-3.1-flash-tts-preview')",
				"enum":        GoogleModels,
			},
		},
		"required": []string{"text"},
	}
	data, err := json.Marshal(schema)
	if err != nil {
		panic(fmt.Sprintf("failed to marshal google_tts schema: %v", err))
	}
	return data
}

func buildOpenAITTSSchema() json.RawMessage {
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"text": map[string]any{
				"type":        "string",
				"description": "The text to convert to speech using OpenAI TTS",
			},
			"voice": map[string]any{
				"type":        "string",
				"description": "Voice to use (alloy, ash, ballad, coral, echo, fable, nova, onyx, sage, shimmer, verse; default: 'alloy')",
				"enum":        OpenAIVoices,
			},
			"model": map[string]any{
				"type":        "string",
				"description": "TTS model to use (gpt-4o-mini-tts-2025-12-15, tts-1, tts-1-hd; default: 'gpt-4o-mini-tts-2025-12-15')",
				"enum":        OpenAIModels,
			},
			"speed": map[string]any{
				"type":        "number",
				"description": "Speech speed (0.25-4.0, default: 1.0)",
				"minimum":     0.25,
				"maximum":     4.0,
			},
			"instructions": map[string]any{
				"type":        "string",
				"description": "Instructions for voice modulation and style",
			},
		},
		"required": []string{"text"},
	}
	data, err := json.Marshal(schema)
	if err != nil {
		panic(fmt.Sprintf("failed to marshal openai_tts schema: %v", err))
	}
	return data
}
