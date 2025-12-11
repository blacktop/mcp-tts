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
				"type":        "integer",
				"description": "Speech rate in words per minute (50-500, default: 200)",
				"minimum":     50,
				"maximum":     500,
			},
			"voice": map[string]any{
				"type":        "string",
				"description": "Voice to use for speech synthesis (e.g. 'Alex', 'Samantha', 'Victoria')",
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
				"enum": []string{
					"Achernar", "Achird", "Algenib", "Algieba", "Alnilam",
					"Aoede", "Autonoe", "Callirrhoe", "Charon", "Despina",
					"Enceladus", "Erinome", "Fenrir", "Gacrux", "Iapetus",
					"Kore", "Laomedeia", "Leda", "Orus", "Puck",
					"Pulcherrima", "Rasalgethi", "Sadachbia", "Sadaltager", "Schedar",
					"Sulafat", "Umbriel", "Vindemiatrix", "Zephyr", "Zubenelgenubi",
				},
			},
			"model": map[string]any{
				"type":        "string",
				"description": "TTS model to use (default: 'gemini-2.5-flash-preview-tts')",
				"enum":        []string{"gemini-2.5-flash-preview-tts", "gemini-2.5-pro-preview-tts", "gemini-2.5-flash-lite-preview-tts"},
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
				"enum":        []string{"alloy", "ash", "ballad", "coral", "echo", "fable", "nova", "onyx", "sage", "shimmer", "verse"},
			},
			"model": map[string]any{
				"type":        "string",
				"description": "TTS model to use (gpt-4o-mini-tts, gpt-4o-audio-preview, tts-1, tts-1-hd; default: 'gpt-4o-mini-tts')",
				"enum":        []string{"gpt-4o-mini-tts", "gpt-4o-audio-preview", "tts-1", "tts-1-hd"},
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
