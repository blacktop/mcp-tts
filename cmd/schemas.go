package cmd

import (
	"github.com/modelcontextprotocol/go-sdk/jsonschema"
)

// Custom schema builders that create LM Studio-compatible schemas
// These avoid using complex additionalProperties objects

func buildSayTTSSchema() *jsonschema.Schema {
	// Create a schema that explicitly sets AdditionalProperties to false
	// to avoid LM Studio compatibility issues
	return &jsonschema.Schema{
		Type: "object",
		Properties: map[string]*jsonschema.Schema{
			"text": {
				Type:        "string",
				Description: "The text to speak aloud",
			},
			"rate": {
				Type:        "integer",
				Description: "Speech rate in words per minute (50-500, default: 200)",
				Minimum:     &[]float64{50}[0],
				Maximum:     &[]float64{500}[0],
			},
			"voice": {
				Type:        "string",
				Description: "Voice to use for speech synthesis (e.g. 'Alex', 'Samantha', 'Victoria')",
			},
		},
		Required: []string{"text"},
		// Set AdditionalProperties to nil (allows additional properties)
		// This avoids LM Studio compatibility issues
		AdditionalProperties: nil,
	}
}

func buildElevenLabsTTSSchema() *jsonschema.Schema {
	return &jsonschema.Schema{
		Type: "object",
		Properties: map[string]*jsonschema.Schema{
			"text": {
				Type:        "string",
				Description: "The text to convert to speech using ElevenLabs API",
			},
		},
		Required:             []string{"text"},
		AdditionalProperties: nil,
	}
}

func buildGoogleTTSSchema() *jsonschema.Schema {
	return &jsonschema.Schema{
		Type: "object",
		Properties: map[string]*jsonschema.Schema{
			"text": {
				Type:        "string",
				Description: "The text to convert to speech using Google TTS",
			},
			"voice": {
				Type:        "string",
				Description: "Voice name to use (e.g. 'Kore', 'Aoede', 'Fenrir', default: 'Kore')",
			},
			"model": {
				Type:        "string",
				Description: "TTS model to use (default: 'gemini-2.5-flash-preview-tts')",
			},
		},
		Required:             []string{"text"},
		AdditionalProperties: nil,
	}
}

func buildOpenAITTSSchema() *jsonschema.Schema {
	return &jsonschema.Schema{
		Type: "object",
		Properties: map[string]*jsonschema.Schema{
			"text": {
				Type:        "string",
				Description: "The text to convert to speech using OpenAI TTS",
			},
			"voice": {
				Type:        "string",
				Description: "Voice to use (alloy, ash, ballad, coral, echo, fable, nova, onyx, sage, shimmer, verse; default: 'alloy')",
				Enum:        []any{"alloy", "ash", "ballad", "coral", "echo", "fable", "nova", "onyx", "sage", "shimmer", "verse"},
			},
			"model": {
				Type:        "string",
				Description: "TTS model to use (gpt-4o-mini-tts, gpt-4o-audio-preview; default: 'gpt-4o-mini-tts')",
				Enum:        []any{"gpt-4o-mini-tts", "gpt-4o-audio-preview"},
			},
			"speed": {
				Type:        "number",
				Description: "Speech speed (0.25-4.0, default: 1.0)",
				Minimum:     &[]float64{0.25}[0],
				Maximum:     &[]float64{4.0}[0],
			},
			"instructions": {
				Type:        "string",
				Description: "Instructions for voice modulation and style",
			},
		},
		Required:             []string{"text"},
		AdditionalProperties: nil,
	}
}
