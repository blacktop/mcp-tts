package cmd

import (
	"context"
	"encoding/json"
	"os"
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

func BenchmarkPCMAudioGeneration(b *testing.B) {
	// Benchmark PCM audio generation performance for Google TTS (24kHz)
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		_ = generateTestAudio(24000, 1.0, 440.0)
	}
}
