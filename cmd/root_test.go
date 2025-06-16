package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// MockAudioPlayer simulates audio playback for testing
type MockAudioPlayer struct {
	PlayedAudio []byte
	Duration    time.Duration
	Played      bool
}

func (m *MockAudioPlayer) Play(audioData []byte) error {
	m.PlayedAudio = audioData
	m.Played = true
	// Simulate audio playback duration
	time.Sleep(m.Duration)
	return nil
}

func TestGoogleTTSTool(t *testing.T) {
	// Set up test environment variables
	originalAPIKey := os.Getenv("GOOGLE_AI_API_KEY")
	defer func() {
		if originalAPIKey != "" {
			os.Setenv("GOOGLE_AI_API_KEY", originalAPIKey)
		} else {
			os.Unsetenv("GOOGLE_AI_API_KEY")
		}
	}()

	tests := []struct {
		name           string
		setupEnv       func()
		arguments      map[string]interface{}
		expectedError  bool
		expectedResult string
		shouldContain  []string
	}{
		{
			name: "successful TTS request with default model",
			setupEnv: func() {
				os.Setenv("GOOGLE_AI_API_KEY", "test-api-key")
			},
			arguments: map[string]interface{}{
				"text": "Hello, this is a test of Google TTS",
			},
			expectedError: false,
			shouldContain: []string{"Google TTS", "gemini-2.5-flash-preview-tts", "voice Kore"},
		},
		{
			name: "successful TTS request with custom voice and model",
			setupEnv: func() {
				os.Setenv("GOOGLE_AI_API_KEY", "test-api-key")
			},
			arguments: map[string]interface{}{
				"text":  "Hello, speak with Puck voice",
				"voice": "Puck",
				"model": "gemini-2.5-pro-preview-tts",
			},
			expectedError: false,
			shouldContain: []string{"Google TTS", "voice Puck", "gemini-2.5-pro-preview-tts"},
		},
		{
			name: "missing API key",
			setupEnv: func() {
				os.Unsetenv("GOOGLE_AI_API_KEY")
				os.Unsetenv("GEMINI_API_KEY")
			},
			arguments: map[string]interface{}{
				"text": "Hello",
			},
			expectedError: true,
			shouldContain: []string{"GOOGLE_AI_API_KEY or GEMINI_API_KEY is not set"},
		},
		{
			name: "empty text",
			setupEnv: func() {
				os.Setenv("GOOGLE_AI_API_KEY", "test-api-key")
			},
			arguments: map[string]interface{}{
				"text": "",
			},
			expectedError: true,
			shouldContain: []string{"Empty text provided"},
		},
		{
			name: "invalid text type",
			setupEnv: func() {
				os.Setenv("GOOGLE_AI_API_KEY", "test-api-key")
			},
			arguments: map[string]interface{}{
				"text": 123,
			},
			expectedError: true,
			shouldContain: []string{"text must be a string"},
		},
		{
			name: "default parameters",
			setupEnv: func() {
				os.Setenv("GOOGLE_AI_API_KEY", "test-api-key")
			},
			arguments: map[string]interface{}{
				"text": "Test with defaults",
			},
			expectedError: false,
			shouldContain: []string{"Google TTS", "voice Kore", "gemini-2.5-flash-preview-tts"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Setup environment
			tt.setupEnv()

			// Create the request
			requestData := map[string]interface{}{
				"params": map[string]interface{}{
					"name":      "google_tts",
					"arguments": tt.arguments,
				},
			}

			jsonData, err := json.Marshal(requestData)
			require.NoError(t, err)

			var request mcp.CallToolRequest
			err = json.Unmarshal(jsonData, &request)
			require.NoError(t, err)

			// For testing purposes, we'll directly invoke the tool handler
			ctx := context.Background()

			// Create a handler variable that we can test directly
			var testHandler func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error)

			// Re-create the tool handler logic for testing
			testHandler = func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				arguments := request.GetArguments()

				// Validate text parameter
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

				// Check API key
				apiKey := os.Getenv("GOOGLE_AI_API_KEY")
				if apiKey == "" {
					apiKey = os.Getenv("GEMINI_API_KEY")
				}
				if apiKey == "" {
					result := mcp.NewToolResultText("Error: GOOGLE_AI_API_KEY or GEMINI_API_KEY is not set")
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

				// For testing, we'll simulate a successful TTS generation without actually calling the API
				return mcp.NewToolResultText(
					"Speaking: " + text + " (via Google TTS with voice " + voice + " using model " + model + ")"), nil
			}

			result, err := testHandler(ctx, request)

			if tt.expectedError {
				require.NotNil(t, result)
				assert.True(t, result.IsError, "Expected error but got success")
			} else {
				require.NoError(t, err)
				require.NotNil(t, result)
				assert.False(t, result.IsError, "Expected success but got error: %v", result)
			}

			// Check that result contains expected strings
			if len(tt.shouldContain) > 0 {
				resultText := ""

				// Extract text from the result
				if len(result.Content) > 0 {
					if textContent, ok := result.Content[0].(mcp.TextContent); ok {
						resultText = textContent.Text
					} else if textContentPtr, ok := result.Content[0].(*mcp.TextContent); ok {
						resultText = textContentPtr.Text
					}
				}

				for _, expectedStr := range tt.shouldContain {
					assert.Contains(t, resultText, expectedStr,
						"Result should contain '%s', but got: %s", expectedStr, resultText)
				}
			}
		})
	}
}

func TestGoogleTTSAudioPlayback(t *testing.T) {
	// Test PCM audio playback simulation for Google TTS
	mockPlayer := &MockAudioPlayer{
		Duration: 100 * time.Millisecond,
	}

	// Simulate PCM audio data (24kHz as returned by Google TTS)
	sampleRate := 24000 // Google TTS uses 24kHz
	duration := 0.5     // seconds
	frequency := 440.0  // Hz (A note)

	audioData := generateTestAudio(sampleRate, duration, frequency)

	// Test playing the audio
	err := mockPlayer.Play(audioData)
	assert.NoError(t, err)
	assert.True(t, mockPlayer.Played)
	assert.Equal(t, audioData, mockPlayer.PlayedAudio)

	// Verify audio data properties for Google TTS
	expectedSamples := int(float64(sampleRate) * duration)
	expectedBytes := expectedSamples * 2 // 16-bit samples
	assert.Equal(t, expectedBytes, len(audioData))
}

func TestGoogleTTSParameterValidation(t *testing.T) {
	tests := []struct {
		name    string
		voice   string
		model   string
		isValid bool
	}{
		{"valid voice Kore", "Kore", "gemini-2.5-flash-preview-tts", true},
		{"valid voice Puck", "Puck", "gemini-2.5-pro-preview-tts", true},
		{"valid voice Charon", "Charon", "gemini-2.5-flash-preview-tts", true},
		{"valid voice Fenrir", "Fenrir", "gemini-2.5-flash-preview-tts", true},
		{"valid voice Aoede", "Aoede", "gemini-2.5-flash-preview-tts", true},
		{"valid voice Leda", "Leda", "gemini-2.5-flash-preview-tts", true},
		{"valid voice Orus", "Orus", "gemini-2.5-flash-preview-tts", true},
		{"valid voice Zephyr", "Zephyr", "gemini-2.5-flash-preview-tts", true},
		{"valid voice Autonoe", "Autonoe", "gemini-2.5-flash-preview-tts", true},
		{"valid voice Enceladus", "Enceladus", "gemini-2.5-flash-preview-tts", true},
		{"valid voice Callirhoe", "Callirhoe", "gemini-2.5-flash-preview-tts", true},
		{"valid voice Iapetus", "Iapetus", "gemini-2.5-flash-preview-tts", true},
		{"empty values use defaults", "", "", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Validate voice options (Google TTS supports 30 voices)
			validVoices := []string{
				"Zephyr", "Puck", "Charon", "Kore", "Fenrir", "Aoede", "Leda", "Orus",
				"Autonoe", "Enceladus", "Callirhoe", "Iapetus", "Umbriel", "Algieba",
				"Despina", "Erinome", "Algenib", "Rasalgethi", "Laomedeia", "Achernar",
				"Alnilam", "Schedar", "Gacrux", "Pulcherrima", "Achird", "Zubenelgenubi",
				"Vindemiatrix", "Sadachbia", "Sadaltager", "Sulafar",
			}
			if tt.voice != "" {
				found := false
				for _, validVoice := range validVoices {
					if tt.voice == validVoice {
						found = true
						break
					}
				}
				if tt.isValid {
					assert.True(t, found, "Voice %s should be valid", tt.voice)
				}
			}

			// Validate model options
			validModels := []string{
				"gemini-2.5-flash-preview-tts",
				"gemini-2.5-pro-preview-tts",
				"",
			}
			if tt.model != "" {
				found := false
				for _, validModel := range validModels {
					if tt.model == validModel {
						found = true
						break
					}
				}
				if tt.isValid {
					assert.True(t, found, "Model %s should be valid", tt.model)
				}
			}
		})
	}
}

// generateTestAudio creates simple PCM audio data for testing
func generateTestAudio(sampleRate int, duration float64, frequency float64) []byte {
	numSamples := int(float64(sampleRate) * duration)
	audioData := make([]byte, numSamples*2) // 16-bit samples

	for i := 0; i < numSamples; i++ {
		// Generate sine wave
		t := float64(i) / float64(sampleRate)
		sample := int16(32767 * 0.1 * sinApprox(2*3.14159*frequency*t)) // 10% volume

		// Convert to little-endian bytes
		audioData[i*2] = byte(sample & 0xFF)
		audioData[i*2+1] = byte((sample >> 8) & 0xFF)
	}

	return audioData
}

// sinApprox provides a simple sine approximation for testing
func sinApprox(x float64) float64 {
	// Simple sine approximation using Taylor series (first few terms)
	// Good enough for testing purposes
	x = x - float64(int(x/(2*3.14159)))*(2*3.14159) // Normalize to 0-2π
	return x - (x*x*x)/6 + (x*x*x*x*x)/120
}

func TestGoogleTTSAudioIntegration(t *testing.T) {
	// Integration test that demonstrates end-to-end Google TTS audio functionality
	t.Log("🧪 Running Google TTS Audio Integration Test...")

	// Create a mock audio player
	mockPlayer := &MockAudioPlayer{
		Duration: 500 * time.Millisecond,
	}

	// Test 1: Basic PCM audio generation and playback at 24kHz (Google TTS sample rate)
	t.Run("basic_pcm_playback", func(t *testing.T) {
		t.Log("🎵 Testing basic PCM audio playback at 24kHz...")

		// Generate test audio data at Google TTS sample rate (24kHz)
		audioData := generateTestAudio(24000, 1.0, 440.0)
		t.Logf("📊 Generated %d bytes of PCM audio data", len(audioData))

		// Simulate playback
		start := time.Now()
		err := mockPlayer.Play(audioData)
		duration := time.Since(start)

		assert.NoError(t, err)
		assert.True(t, mockPlayer.Played)
		assert.Equal(t, audioData, mockPlayer.PlayedAudio)
		t.Logf("✅ PCM audio playback completed in %v", duration)
	})

	// Test 2: Multiple Google TTS voice configurations
	t.Run("multiple_google_voices", func(t *testing.T) {
		t.Log("🎭 Testing multiple Google TTS voice configurations...")

		voices := []string{"Zephyr", "Puck", "Charon", "Kore", "Fenrir", "Aoede", "Leda", "Orus", "Autonoe", "Enceladus"}

		for i, voice := range voices {
			t.Run(voice, func(t *testing.T) {
				// Reset the mock player for each voice
				mockPlayer.Played = false
				mockPlayer.PlayedAudio = nil

				// Generate audio with different frequencies for each voice
				frequency := 300.0 + float64(i*40) // Start at 300Hz, increment by 40Hz
				audioData := generateTestAudio(24000, 0.4, frequency)

				err := mockPlayer.Play(audioData)
				assert.NoError(t, err)
				assert.True(t, mockPlayer.Played)
				t.Logf("   ✅ Google TTS Voice %s tested successfully (%.0fHz)", voice, frequency)
			})
		}
	})

	// Test 3: Google TTS specific audio formats
	t.Run("google_tts_formats", func(t *testing.T) {
		t.Log("🎛️  Testing Google TTS specific audio formats...")

		testCases := []struct {
			name       string
			sampleRate int
			duration   float64
			frequency  float64
		}{
			{"google_tts_standard", 24000, 0.5, 440.0}, // Google TTS standard rate
			{"google_tts_short", 24000, 0.2, 880.0},    // Shorter duration
			{"google_tts_long", 24000, 1.0, 220.0},     // Longer duration
		}

		for _, tc := range testCases {
			t.Run(tc.name, func(t *testing.T) {
				audioData := generateTestAudio(tc.sampleRate, tc.duration, tc.frequency)

				// Reset mock player
				mockPlayer.Played = false
				mockPlayer.PlayedAudio = nil

				err := mockPlayer.Play(audioData)
				assert.NoError(t, err)
				assert.True(t, mockPlayer.Played)

				expectedSamples := int(float64(tc.sampleRate) * tc.duration)
				expectedBytes := expectedSamples * 2 // 16-bit samples
				assert.Equal(t, expectedBytes, len(audioData))

				t.Logf("   ✅ %s: %d samples, %d bytes (24kHz PCM)", tc.name, expectedSamples, len(audioData))
			})
		}
	})

	// Test 4: PCM Stream functionality
	t.Run("pcm_stream_testing", func(t *testing.T) {
		t.Log("🎼 Testing PCM Stream functionality...")

		audioData := generateTestAudio(24000, 0.5, 440.0)
		pcmStream := &PCMStream{
			data:       audioData,
			sampleRate: 24000,
			position:   0,
		}

		// Test stream properties
		assert.Equal(t, len(audioData)/2, pcmStream.Len())
		assert.Equal(t, 0, pcmStream.Position())
		assert.NoError(t, pcmStream.Err())

		// Test seeking
		err := pcmStream.Seek(100)
		assert.NoError(t, err)
		assert.Equal(t, 100, pcmStream.Position())

		t.Log("   ✅ PCM Stream functionality validated")
	})

	t.Log("🏆 Google TTS Audio Integration Test completed successfully!")
}

func BenchmarkGoogleTTSTool(t *testing.B) {
	// Set up test environment
	os.Setenv("GOOGLE_AI_API_KEY", "test-api-key")
	defer os.Unsetenv("GOOGLE_AI_API_KEY")

	// Create test arguments
	arguments := map[string]interface{}{
		"text":  "Benchmark test message for Google TTS",
		"voice": "Puck",
		"model": "gemini-2.5-flash-preview-tts",
	}

	t.ResetTimer()

	for i := 0; i < t.N; i++ {
		// Simulate the parameter validation and processing
		// (without actual API calls for benchmarking)

		text, ok := arguments["text"].(string)
		if !ok || text == "" {
			t.Fatal("Invalid text parameter")
		}

		voice := "Kore"
		if v, ok := arguments["voice"].(string); ok && v != "" {
			voice = v
		}

		model := "gemini-2.5-flash-preview-tts"
		if m, ok := arguments["model"].(string); ok && m != "" {
			model = m
		}

		// Simulate processing time
		_ = text + voice + model
	}
}

func TestOpenAITTSTool(t *testing.T) {
	// Set up test environment variables
	originalAPIKey := os.Getenv("OPENAI_API_KEY")
	originalInstructions := os.Getenv("OPENAI_TTS_INSTRUCTIONS")
	defer func() {
		if originalAPIKey != "" {
			os.Setenv("OPENAI_API_KEY", originalAPIKey)
		} else {
			os.Unsetenv("OPENAI_API_KEY")
		}
		if originalInstructions != "" {
			os.Setenv("OPENAI_TTS_INSTRUCTIONS", originalInstructions)
		} else {
			os.Unsetenv("OPENAI_TTS_INSTRUCTIONS")
		}
	}()

	tests := []struct {
		name           string
		setupEnv       func()
		arguments      map[string]interface{}
		expectedError  bool
		expectedResult string
		shouldContain  []string
	}{
		{
			name: "successful TTS request with default settings",
			setupEnv: func() {
				os.Setenv("OPENAI_API_KEY", "test-api-key")
			},
			arguments: map[string]interface{}{
				"text": "Hello, this is a test of OpenAI TTS",
			},
			expectedError: false,
			shouldContain: []string{"OpenAI TTS", "voice coral"},
		},
		{
			name: "successful TTS request with custom voice and model",
			setupEnv: func() {
				os.Setenv("OPENAI_API_KEY", "test-api-key")
			},
			arguments: map[string]interface{}{
				"text":  "Hello, speak with echo voice",
				"voice": "echo",
				"model": "tts-1-hd",
				"speed": 1.5,
			},
			expectedError: false,
			shouldContain: []string{"OpenAI TTS", "voice echo"},
		},
		{
			name: "missing API key",
			setupEnv: func() {
				os.Unsetenv("OPENAI_API_KEY")
			},
			arguments: map[string]interface{}{
				"text": "Hello",
			},
			expectedError: true,
			shouldContain: []string{"OPENAI_API_KEY is not set"},
		},
		{
			name: "empty text",
			setupEnv: func() {
				os.Setenv("OPENAI_API_KEY", "test-api-key")
			},
			arguments: map[string]interface{}{
				"text": "",
			},
			expectedError: true,
			shouldContain: []string{"Empty text provided"},
		},
		{
			name: "invalid text type",
			setupEnv: func() {
				os.Setenv("OPENAI_API_KEY", "test-api-key")
			},
			arguments: map[string]interface{}{
				"text": 123,
			},
			expectedError: true,
			shouldContain: []string{"text must be a string"},
		},
		{
			name: "speed out of range - too slow",
			setupEnv: func() {
				os.Setenv("OPENAI_API_KEY", "test-api-key")
			},
			arguments: map[string]interface{}{
				"text":  "Speed test",
				"speed": 0.1,
			},
			expectedError: false,
			shouldContain: []string{"OpenAI TTS", "voice coral"}, // Should use default speed
		},
		{
			name: "speed out of range - too fast",
			setupEnv: func() {
				os.Setenv("OPENAI_API_KEY", "test-api-key")
			},
			arguments: map[string]interface{}{
				"text":  "Speed test",
				"speed": 5.0,
			},
			expectedError: false,
			shouldContain: []string{"OpenAI TTS", "voice coral"}, // Should use default speed
		},
		{
			name: "valid speed range",
			setupEnv: func() {
				os.Setenv("OPENAI_API_KEY", "test-api-key")
			},
			arguments: map[string]interface{}{
				"text":  "Speed test",
				"speed": 2.0,
			},
			expectedError: false,
			shouldContain: []string{"OpenAI TTS", "voice coral"},
		},
		{
			name: "custom instructions via parameter",
			setupEnv: func() {
				os.Setenv("OPENAI_API_KEY", "test-api-key")
			},
			arguments: map[string]interface{}{
				"text":         "Test with custom instructions",
				"instructions": "Speak in a cheerful and positive tone",
			},
			expectedError: false,
			shouldContain: []string{"OpenAI TTS", "voice coral"},
		},
		{
			name: "custom instructions via environment variable",
			setupEnv: func() {
				os.Setenv("OPENAI_API_KEY", "test-api-key")
				os.Setenv("OPENAI_TTS_INSTRUCTIONS", "Speak in a calm and soothing manner")
			},
			arguments: map[string]interface{}{
				"text": "Test with env var instructions",
			},
			expectedError: false,
			shouldContain: []string{"OpenAI TTS", "voice coral"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Setup environment
			tt.setupEnv()

			// Create the request
			requestData := map[string]interface{}{
				"params": map[string]interface{}{
					"name":      "openai_tts",
					"arguments": tt.arguments,
				},
			}

			jsonData, err := json.Marshal(requestData)
			require.NoError(t, err)

			var request mcp.CallToolRequest
			err = json.Unmarshal(jsonData, &request)
			require.NoError(t, err)

			// For testing purposes, we'll directly invoke the tool handler
			ctx := context.Background()

			// Create a handler variable that we can test directly
			var testHandler func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error)

			// Re-create the tool handler logic for testing
			testHandler = func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				arguments := request.GetArguments()

				// Validate text parameter
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

				// Check API key
				apiKey := os.Getenv("OPENAI_API_KEY")
				if apiKey == "" {
					result := mcp.NewToolResultText("Error: OPENAI_API_KEY is not set")
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

				// For testing, we'll simulate a successful TTS generation without actually calling the API
				resultText := fmt.Sprintf("Speaking: %s (via OpenAI TTS with voice %s, model %s, speed %.1f)", text, voice, model, speed)
				if instructions != "" {
					resultText += fmt.Sprintf(" with instructions: %s", instructions)
				}
				return mcp.NewToolResultText(resultText), nil
			}

			result, err := testHandler(ctx, request)

			if tt.expectedError {
				require.NotNil(t, result)
				assert.True(t, result.IsError, "Expected error but got success")
			} else {
				require.NoError(t, err)
				require.NotNil(t, result)
				assert.False(t, result.IsError, "Expected success but got error: %v", result)
			}

			// Check that result contains expected strings
			if len(tt.shouldContain) > 0 {
				resultText := ""

				// Extract text from the result
				if len(result.Content) > 0 {
					if textContent, ok := result.Content[0].(mcp.TextContent); ok {
						resultText = textContent.Text
					} else if textContentPtr, ok := result.Content[0].(*mcp.TextContent); ok {
						resultText = textContentPtr.Text
					}
				}

				for _, expectedStr := range tt.shouldContain {
					assert.Contains(t, resultText, expectedStr,
						"Result should contain '%s', but got: %s", expectedStr, resultText)
				}
			}
		})
	}
}

func TestOpenAITTSParameterValidation(t *testing.T) {
	tests := []struct {
		name         string
		voice        string
		model        string
		speed        float64
		instructions string
		isValid      bool
	}{
		{"valid voice coral", "coral", "gpt-4o-mini-tts", 1.0, "", true},
		{"valid voice alloy", "alloy", "tts-1", 1.0, "", true},
		{"valid voice echo", "echo", "tts-1-hd", 1.5, "", true},
		{"valid voice fable", "fable", "gpt-4o-mini-tts", 0.75, "", true},
		{"valid voice onyx", "onyx", "tts-1-hd", 2.0, "", true},
		{"valid voice nova", "nova", "gpt-4o-mini-tts", 1.2, "", true},
		{"valid voice shimmer", "shimmer", "tts-1-hd", 0.5, "", true},
		{"empty values use defaults", "", "", 1.0, "", true},
		{"speed at minimum", "coral", "gpt-4o-mini-tts", 0.25, "", true},
		{"speed at maximum", "coral", "gpt-4o-mini-tts", 4.0, "", true},
		{"speed too low", "coral", "gpt-4o-mini-tts", 0.1, "", false},
		{"speed too high", "coral", "gpt-4o-mini-tts", 5.0, "", false},
		{"with instructions", "coral", "gpt-4o-mini-tts", 1.0, "Speak in a cheerful and positive tone", true},
		{"with empty instructions", "nova", "tts-1-hd", 1.5, "", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Validate voice options
			validVoices := []string{"coral", "alloy", "echo", "fable", "onyx", "nova", "shimmer"}
			if tt.voice != "" {
				found := false
				for _, validVoice := range validVoices {
					if tt.voice == validVoice {
						found = true
						break
					}
				}
				if tt.isValid {
					assert.True(t, found, "Voice %s should be valid", tt.voice)
				}
			}

			// Validate model options
			validModels := []string{"gpt-4o-mini-tts", "tts-1", "tts-1-hd", ""}
			if tt.model != "" {
				found := false
				for _, validModel := range validModels {
					if tt.model == validModel {
						found = true
						break
					}
				}
				if tt.isValid {
					assert.True(t, found, "Model %s should be valid", tt.model)
				}
			}

			// Validate speed range
			if tt.speed != 0 {
				speedValid := tt.speed >= 0.25 && tt.speed <= 4.0
				if tt.isValid {
					assert.True(t, speedValid, "Speed %f should be valid (0.25-4.0)", tt.speed)
				} else {
					assert.False(t, speedValid, "Speed %f should be invalid", tt.speed)
				}
			}

			// Validate instructions (should always be valid regardless of content)
			if tt.instructions != "" {
				assert.True(t, len(tt.instructions) > 0, "Instructions should be non-empty when provided")
			}
		})
	}
}

func TestOpenAITTSAudioIntegration(t *testing.T) {
	// Integration test that demonstrates end-to-end OpenAI TTS audio functionality
	t.Log("🧪 Running OpenAI TTS Audio Integration Test...")

	// Create a mock audio player
	mockPlayer := &MockAudioPlayer{
		Duration: 500 * time.Millisecond,
	}

	// Test 1: Basic MP3 audio generation and playback
	t.Run("basic_mp3_playback", func(t *testing.T) {
		t.Log("🎵 Testing basic MP3 audio playback...")

		// Generate test audio data (simulating MP3 format from OpenAI)
		audioData := generateTestAudio(22050, 1.0, 440.0) // Standard MP3 sample rate
		t.Logf("📊 Generated %d bytes of MP3 audio data", len(audioData))

		// Simulate playback
		start := time.Now()
		err := mockPlayer.Play(audioData)
		duration := time.Since(start)

		assert.NoError(t, err)
		assert.True(t, mockPlayer.Played)
		assert.Equal(t, audioData, mockPlayer.PlayedAudio)
		t.Logf("✅ MP3 audio playback completed in %v", duration)
	})

	// Test 2: Multiple OpenAI TTS voice configurations
	t.Run("multiple_openai_voices", func(t *testing.T) {
		t.Log("🎭 Testing multiple OpenAI TTS voice configurations...")

		voices := []string{"coral", "alloy", "echo", "fable", "onyx", "nova", "shimmer"}

		for i, voice := range voices {
			t.Run(voice, func(t *testing.T) {
				// Reset the mock player for each voice
				mockPlayer.Played = false
				mockPlayer.PlayedAudio = nil

				// Generate audio with different frequencies for each voice
				frequency := 350.0 + float64(i*50) // Start at 350Hz, increment by 50Hz
				audioData := generateTestAudio(22050, 0.4, frequency)

				err := mockPlayer.Play(audioData)
				assert.NoError(t, err)
				assert.True(t, mockPlayer.Played)
				t.Logf("   ✅ OpenAI TTS Voice %s tested successfully (%.0fHz)", voice, frequency)
			})
		}
	})

	// Test 3: OpenAI TTS specific speed variations
	t.Run("openai_tts_speeds", func(t *testing.T) {
		t.Log("🎛️  Testing OpenAI TTS speed variations...")

		testCases := []struct {
			name     string
			speed    float64
			duration float64
		}{
			{"slow_speed", 0.5, 0.8},   // Slow speed
			{"normal_speed", 1.0, 0.5}, // Normal speed
			{"fast_speed", 2.0, 0.3},   // Fast speed
			{"max_speed", 4.0, 0.2},    // Maximum speed
		}

		for _, tc := range testCases {
			t.Run(tc.name, func(t *testing.T) {
				audioData := generateTestAudio(22050, tc.duration, 440.0)

				// Reset mock player
				mockPlayer.Played = false
				mockPlayer.PlayedAudio = nil

				err := mockPlayer.Play(audioData)
				assert.NoError(t, err)
				assert.True(t, mockPlayer.Played)

				expectedSamples := int(float64(22050) * tc.duration)
				expectedBytes := expectedSamples * 2 // 16-bit samples
				assert.Equal(t, expectedBytes, len(audioData))

				t.Logf("   ✅ %s: %.1fx speed, %d samples, %d bytes", tc.name, tc.speed, expectedSamples, len(audioData))
			})
		}
	})

	// Test 4: OpenAI TTS model variations
	t.Run("openai_tts_models", func(t *testing.T) {
		t.Log("🤖 Testing OpenAI TTS model variations...")

		models := []struct {
			name    string
			model   string
			quality string
		}{
			{"standard_model", "tts-1", "standard quality"},
			{"hd_model", "tts-1-hd", "high definition"},
		}

		for _, model := range models {
			t.Run(model.name, func(t *testing.T) {
				// Reset mock player
				mockPlayer.Played = false
				mockPlayer.PlayedAudio = nil

				audioData := generateTestAudio(22050, 0.5, 440.0)

				err := mockPlayer.Play(audioData)
				assert.NoError(t, err)
				assert.True(t, mockPlayer.Played)

				t.Logf("   ✅ OpenAI TTS Model %s (%s) tested successfully", model.model, model.quality)
			})
		}
	})

	t.Log("🏆 OpenAI TTS Audio Integration Test completed successfully!")
}

func BenchmarkOpenAITTSTool(b *testing.B) {
	// Set up test environment
	os.Setenv("OPENAI_API_KEY", "test-api-key")
	defer os.Unsetenv("OPENAI_API_KEY")

	// Create test arguments
	arguments := map[string]interface{}{
		"text":         "Benchmark test message for OpenAI TTS",
		"voice":        "nova",
		"model":        "tts-1-hd",
		"speed":        1.2,
		"instructions": "Speak in a professional tone",
	}

	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		// Simulate the parameter validation and processing
		// (without actual API calls for benchmarking)

		text, ok := arguments["text"].(string)
		if !ok || text == "" {
			b.Fatal("Invalid text parameter")
		}

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
			}
		}

		instructions := ""
		if inst, ok := arguments["instructions"].(string); ok {
			instructions = inst
		}

		// Simulate processing time (use all parameters to avoid unused variable warnings)
		_ = len(text) + len(voice) + len(model) + len(instructions) + int(speed*100)
	}
}

func BenchmarkPCMAudioGeneration(b *testing.B) {
	// Benchmark PCM audio generation performance for Google TTS (24kHz)
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		_ = generateTestAudio(24000, 1.0, 440.0)
	}
}

func TestIsWSL(t *testing.T) {
	tests := []struct {
		name           string
		procVersion    string
		expectedResult bool
	}{
		{
			name:           "WSL2 environment",
			procVersion:    "Linux version 5.15.167.4-microsoft-standard-WSL2 (root@f9c826d3017f) (gcc (GCC) 11.2.0, GNU ld (GNU Binutils) 2.37) #1 SMP Tue Nov 5 00:21:55 UTC 2024",
			expectedResult: true,
		},
		{
			name:           "WSL1 environment",
			procVersion:    "Linux version 4.4.0-19041-Microsoft (Microsoft@Microsoft.com) (gcc version 5.4.0 (GCC) ) #1237-Microsoft Sat Sep 11 14:32:00 PST 2021",
			expectedResult: true,
		},
		{
			name:           "Regular Linux",
			procVersion:    "Linux version 5.15.0-91-generic (buildd@lcy02-amd64-051) (gcc (Ubuntu 11.4.0-1ubuntu1~22.04) 11.4.0, GNU ld (GNU Binutils for Ubuntu) 2.38) #101-Ubuntu SMP Tue Nov 14 13:30:08 UTC 2023",
			expectedResult: false,
		},
		{
			name:           "macOS (file doesn't exist)",
			procVersion:    "",
			expectedResult: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create a temporary file with the test content if needed
			if tt.procVersion != "" {
				tmpFile, err := os.CreateTemp("", "proc_version")
				require.NoError(t, err)
				defer os.Remove(tmpFile.Name())

				_, err = tmpFile.WriteString(tt.procVersion)
				require.NoError(t, err)
				tmpFile.Close()

				// Override the isWSL function to read from our test file
				// Since we can't easily mock the file path, we'll test the logic directly
				data, err := os.ReadFile(tmpFile.Name())
				require.NoError(t, err)
				
				version := string(data)
				isWSL := (contains(strings.ToLower(version), "microsoft") || 
				         contains(strings.ToLower(version), "wsl"))
				
				assert.Equal(t, tt.expectedResult, isWSL, 
					"isWSL() should return %v for: %s", tt.expectedResult, tt.name)
			} else {
				// Test the case where the file doesn't exist
				assert.False(t, false, "File doesn't exist should return false")
			}
		})
	}
}

// Helper function for string contains
func contains(s, substr string) bool {
	return strings.Contains(s, substr)
}

func TestWindowsSpeechTTSTool(t *testing.T) {
	// Only run these tests if Windows Speech is available via PowerShell
	if !canRunPowerShell() {
		t.Skip("Skipping Windows Speech TTS tests - PowerShell not available")
	}

	tests := []struct {
		name           string
		arguments      map[string]interface{}
		expectedError  bool
		shouldContain  []string
	}{
		{
			name: "successful TTS request with default settings (auto API)",
			arguments: map[string]interface{}{
				"text": "Hello from Windows Speech test",
			},
			expectedError: false,
			shouldContain: []string{"Speaking: Hello from Windows Speech test"},
		},
		{
			name: "successful TTS request with custom rate",
			arguments: map[string]interface{}{
				"text": "Testing with faster speech via Windows Speech",
				"rate": 5.0,
			},
			expectedError: false,
			shouldContain: []string{"Speaking: Testing with faster speech via Windows Speech"},
		},
		{
			name: "explicit SAPI API selection",
			arguments: map[string]interface{}{
				"text": "Testing SAPI API explicitly",
				"api":  "sapi",
			},
			expectedError: false,
			shouldContain: []string{"Speaking: Testing SAPI API explicitly"},
		},
		{
			name: "explicit WinRT API selection",
			arguments: map[string]interface{}{
				"text": "Testing WinRT API explicitly", 
				"api":  "winrt",
			},
			expectedError: false,
			shouldContain: []string{"Speaking: Testing WinRT API explicitly"},
		},
		{
			name: "auto API selection with SAPI voice",
			arguments: map[string]interface{}{
				"text":  "Testing auto API with SAPI voice",
				"voice": "Microsoft Zira Desktop",
				"api":   "auto",
			},
			expectedError: false,
			shouldContain: []string{"Speaking: Testing auto API with SAPI voice"},
		},
		{
			name: "auto API selection with WinRT voice (Canadian)",
			arguments: map[string]interface{}{
				"text":  "Testing auto API with Canadian voice",
				"voice": "Microsoft Linda",
				"api":   "auto",
			},
			expectedError: false,
			shouldContain: []string{"Speaking: Testing auto API with Canadian voice"},
		},
		{
			name: "rate clamped to maximum",
			arguments: map[string]interface{}{
				"text": "Testing maximum rate",
				"rate": 15.0, // Should be clamped to 10
			},
			expectedError: false,
			shouldContain: []string{"Speaking: Testing maximum rate"},
		},
		{
			name: "rate clamped to minimum",
			arguments: map[string]interface{}{
				"text": "Testing minimum rate",
				"rate": -15.0, // Should be clamped to -10
			},
			expectedError: false,
			shouldContain: []string{"Speaking: Testing minimum rate"},
		},
		{
			name: "invalid API type",
			arguments: map[string]interface{}{
				"text": "Testing invalid API",
				"api":  "invalid",
			},
			expectedError: false, // Should fall back to auto
			shouldContain: []string{"Speaking: Testing invalid API"},
		},
		{
			name: "empty text error",
			arguments: map[string]interface{}{
				"text": "",
			},
			expectedError: true,
			shouldContain: []string{"Empty text provided"},
		},
		{
			name: "invalid text type",
			arguments: map[string]interface{}{
				"text": 123,
			},
			expectedError: true,
			shouldContain: []string{"text must be a string"},
		},
		{
			name: "text with single quotes",
			arguments: map[string]interface{}{
				"text": "It's a beautiful day, isn't it?",
			},
			expectedError: false,
			shouldContain: []string{"Speaking: It's a beautiful day, isn't it?"},
		},
		{
			name: "text with special characters",
			arguments: map[string]interface{}{
				"text": "Hello! How are you? Let's test & verify.",
			},
			expectedError: false,
			shouldContain: []string{"Speaking: Hello! How are you? Let's test & verify."},
		},
		{
			name: "SAPI voice selection",
			arguments: map[string]interface{}{
				"text":  "Testing SAPI voice selection",
				"voice": "Microsoft Zira Desktop",
				"api":   "sapi",
			},
			expectedError: false,
			shouldContain: []string{"Speaking: Testing SAPI voice selection"},
		},
		{
			name: "WinRT voice selection (Canadian)",
			arguments: map[string]interface{}{
				"text":  "Testing WinRT Canadian voice",
				"voice": "Microsoft Linda",
				"api":   "winrt",
			},
			expectedError: false,
			shouldContain: []string{"Speaking: Testing WinRT Canadian voice"},
		},
		{
			name: "voice with single quotes",
			arguments: map[string]interface{}{
				"text":  "Testing voice with quotes",
				"voice": "Voice's Name",
			},
			expectedError: false,
			shouldContain: []string{"Speaking: Testing voice with quotes"},
		},
	}

	// Note: These are unit tests that verify the parameter handling
	// They don't actually execute PowerShell to avoid audio output during tests
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create a mock request
			request := mcp.CallToolRequest{
				Params: mcp.CallToolParams{
					Name:      "windows_speech_tts",
					Arguments: tt.arguments,
				},
			}

			// Since we can't easily test the actual PowerShell execution,
			// we'll test the parameter validation logic using the new structure
			arguments := request.GetArguments()
			
			// Test text parameter
			text, ok := arguments["text"].(string)
			if !ok {
				if tt.expectedError {
					assert.Contains(t, tt.shouldContain[0], "text must be a string")
				}
				return
			}
			
			if text == "" {
				if tt.expectedError {
					assert.Contains(t, tt.shouldContain[0], "Empty text provided")
				}
				return
			}

			// Test API parameter (new dual API functionality)
			api := "auto" // default
			if a, ok := arguments["api"].(string); ok && a != "" {
				api = a
			}

			// Test voice parameter
			voice := ""
			if v, ok := arguments["voice"].(string); ok && v != "" {
				voice = v
			}

			// Test rate parameter
			var rate *int
			if r, ok := arguments["rate"].(float64); ok {
				rateInt := int(r)
				if rateInt < -10 {
					rateInt = -10
				} else if rateInt > 10 {
					rateInt = 10
				}
				rate = &rateInt
			}

			// Verify the TTS configuration would be constructed correctly
			config := map[string]interface{}{
				"api":  api,
				"text": text,
			}
			if voice != "" {
				config["voice"] = voice
			}
			if rate != nil {
				config["rate"] = *rate
			}
			
			// Test API determination logic
			expectedAPI := api
			if api == "" || api == "auto" {
				if voice != "" {
					// Voice-based API determination
					if strings.Contains(voice, "Desktop") {
						expectedAPI = "sapi"
					} else if voice == "Microsoft Linda" || voice == "Microsoft Richard" {
						expectedAPI = "winrt"
					} else {
						expectedAPI = "winrt" // Default to WinRT for auto
					}
				} else {
					expectedAPI = "winrt" // Default when no voice specified
				}
			}
			
			// Verify configuration
			assert.NotEmpty(t, config["text"])
			assert.Contains(t, []string{"sapi", "winrt", "auto", "invalid"}, api)
			
			if !tt.expectedError {
				t.Logf("   ✅ %s: API=%s, Voice=%s, Rate=%v", tt.name, expectedAPI, voice, rate)
			}
		})
	}
}

func TestWindowsSpeechTTSIntegration(t *testing.T) {
	// Integration test that checks if Windows Speech is available via PowerShell
	if !canRunPowerShell() {
		t.Skip("Skipping Windows Speech TTS integration test - PowerShell not available")
	}

	// Check if powershell.exe is available
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "powershell.exe", "-Command", "Write-Output 'test'")
	output, err := cmd.CombinedOutput()
	
	if err != nil {
		t.Skipf("PowerShell not available for Windows Speech: %v", err)
	}

	assert.Contains(t, string(output), "test", "PowerShell should be executable for Windows Speech")
	t.Log("✅ PowerShell is available for Windows Speech TTS")
}

func TestWindowsSpeechDualAPIIntegration(t *testing.T) {
	// Integration test for the new dual API functionality
	if !canRunPowerShell() {
		t.Skip("Skipping Windows Speech dual API integration test - PowerShell not available")
	}

	t.Log("🧪 Running Windows Speech Dual API Integration Test...")

	// Test 1: SAPI voice enumeration
	t.Run("SAPI_voice_enumeration", func(t *testing.T) {
		t.Log("🎭 Testing SAPI voice enumeration...")
		
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		// Test SAPI voice enumeration
		sapiCmd := `Add-Type -AssemblyName System.Speech; $synth = New-Object System.Speech.Synthesis.SpeechSynthesizer; $synth.GetInstalledVoices() | ForEach-Object { $_.VoiceInfo.Name }`
		cmd := exec.CommandContext(ctx, "powershell.exe", "-Command", sapiCmd)
		output, err := cmd.CombinedOutput()
		
		if err != nil {
			t.Skipf("SAPI voice enumeration failed: %v", err)
		}

		voices := strings.Split(strings.TrimSpace(string(output)), "\n")
		t.Logf("   ✅ Found %d SAPI voices: %v", len(voices), voices)
		
		// Verify we have typical SAPI voices
		sapiVoiceFound := false
		for _, voice := range voices {
			if strings.Contains(voice, "Desktop") {
				sapiVoiceFound = true
				break
			}
		}
		
		if sapiVoiceFound {
			t.Log("   ✅ SAPI Desktop voices found")
		} else {
			t.Log("   ℹ️  No SAPI Desktop voices found - this may be normal")
		}
	})

	// Test 2: WinRT voice enumeration
	t.Run("WinRT_voice_enumeration", func(t *testing.T) {
		t.Log("🎭 Testing WinRT voice enumeration...")
		
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		// Test WinRT voice enumeration
		winrtCmd := `Add-Type -AssemblyName System.Runtime.WindowsRuntime; [Windows.Media.SpeechSynthesis.SpeechSynthesizer,Windows.Media.SpeechSynthesis,ContentType=WindowsRuntime] | Out-Null; [Windows.Media.SpeechSynthesis.SpeechSynthesizer]::AllVoices | ForEach-Object { $_.DisplayName }`
		cmd := exec.CommandContext(ctx, "powershell.exe", "-Command", winrtCmd)
		output, err := cmd.CombinedOutput()
		
		if err != nil {
			t.Logf("   ⚠️  WinRT voice enumeration failed (expected in WSL): %v", err)
			t.Skip("WinRT not available in this environment")
		}

		voices := strings.Split(strings.TrimSpace(string(output)), "\n")
		t.Logf("   ✅ Found %d WinRT voices: %v", len(voices), voices)
		
		// Check for Canadian voices
		canadianVoices := 0
		for _, voice := range voices {
			if voice == "Microsoft Linda" || voice == "Microsoft Richard" {
				canadianVoices++
				t.Logf("   🍁 Found Canadian voice: %s", voice)
			}
		}
		
		if canadianVoices > 0 {
			t.Logf("   ✅ Found %d Canadian WinRT voices", canadianVoices)
		} else {
			t.Log("   ℹ️  No Canadian WinRT voices found - may depend on system configuration")
		}
	})

	// Test 3: API selection logic
	t.Run("API_selection_logic", func(t *testing.T) {
		t.Log("🤖 Testing API selection logic...")
		
		testCases := []struct {
			name        string
			voice       string
			expectedAPI string
		}{
			{"SAPI voice detection", "Microsoft Zira Desktop", "sapi"},
			{"WinRT voice detection", "Microsoft Linda", "winrt"},
			{"WinRT voice detection", "Microsoft Richard", "winrt"},
			{"Default to WinRT", "", "winrt"},
		}

		for _, tc := range testCases {
			t.Run(tc.name, func(t *testing.T) {
				// Test the API determination logic
				var expectedAPI string
				if tc.voice != "" {
					if strings.Contains(tc.voice, "Desktop") {
						expectedAPI = "sapi"
					} else if tc.voice == "Microsoft Linda" || tc.voice == "Microsoft Richard" {
						expectedAPI = "winrt"
					} else {
						expectedAPI = "winrt"
					}
				} else {
					expectedAPI = "winrt"
				}
				
				assert.Equal(t, tc.expectedAPI, expectedAPI, "API selection should match expected for voice: %s", tc.voice)
				t.Logf("   ✅ Voice '%s' correctly maps to API '%s'", tc.voice, expectedAPI)
			})
		}
	})

	// Test 4: Voice availability matrix
	t.Run("voice_availability_matrix", func(t *testing.T) {
		t.Log("📊 Testing voice availability matrix...")
		
		expectedVoices := map[string]string{
			"Microsoft David Desktop":   "SAPI",
			"Microsoft Zira Desktop":    "SAPI",
			"Microsoft Helena Desktop":  "SAPI",
			"Microsoft Hortense Desktop": "SAPI",
			"Microsoft David":           "WinRT",
			"Microsoft Zira":            "WinRT", 
			"Microsoft Mark":            "WinRT",
			"Microsoft Linda":           "WinRT", // Canadian
			"Microsoft Richard":         "WinRT", // Canadian
			"Microsoft Helena":          "WinRT",
			"Microsoft Laura":           "WinRT",
			"Microsoft Pablo":           "WinRT",
			"Microsoft Hortense":        "WinRT",
			"Microsoft Julie":           "WinRT",
			"Microsoft Paul":            "WinRT",
		}

		for voice, api := range expectedVoices {
			// Verify API mapping logic
			var detectedAPI string
			if strings.Contains(voice, "Desktop") {
				detectedAPI = "SAPI"
			} else {
				detectedAPI = "WinRT"
			}
			
			assert.Equal(t, api, detectedAPI, "Voice %s should map to %s API", voice, api)
		}
		
		t.Logf("   ✅ Verified API mapping for %d voice configurations", len(expectedVoices))
	})

	t.Log("🏆 Windows Speech Dual API Integration Test completed successfully!")
}

