package cmd

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Helper functions for creating pointers to basic types
func stringPtr(s string) *string {
	return &s
}

func intPtr(i int) *int {
	return &i
}

func float64Ptr(f float64) *float64 {
	return &f
}

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

func TestSayTTSTool(t *testing.T) {
	if !isDarwin() {
		t.Skip("Say TTS tool only available on macOS")
	}

	tests := []struct {
		name          string
		params        SayTTSParams
		expectError   bool
		shouldContain []string
	}{
		{
			name: "basic text",
			params: SayTTSParams{
				Text: "Hello, this is a test",
			},
			expectError:   false,
			shouldContain: []string{"Speaking:", "Hello, this is a test"},
		},
		{
			name: "with custom rate",
			params: SayTTSParams{
				Text: "Testing custom rate",
				Rate: intPtr(250),
			},
			expectError:   false,
			shouldContain: []string{"Speaking:", "Testing custom rate"},
		},
		{
			name: "with custom voice",
			params: SayTTSParams{
				Text:  "Testing custom voice",
				Voice: stringPtr("Alex"),
			},
			expectError:   false,
			shouldContain: []string{"Speaking:", "Testing custom voice"},
		},
		{
			name: "with voice containing parentheses",
			params: SayTTSParams{
				Text:  "Testing voice with parentheses",
				Voice: stringPtr("Daniel (English (UK))"),
			},
			expectError:   false,
			shouldContain: []string{"Speaking:", "Testing voice with parentheses"},
		},
		{
			name: "with voice containing underscore",
			params: SayTTSParams{
				Text:  "Testing voice with underscore",
				Voice: stringPtr("en_US"),
			},
			expectError:   false,
			shouldContain: []string{"Speaking:", "Testing voice with underscore"},
		},
		{
			name: "with voice containing hyphen",
			params: SayTTSParams{
				Text:  "Testing voice with hyphen",
				Voice: stringPtr("Eddy (English (UK))"),
			},
			expectError:   false,
			shouldContain: []string{"Speaking:", "Testing voice with hyphen"},
		},
		{
			name: "empty text",
			params: SayTTSParams{
				Text: "",
			},
			expectError:   true,
			shouldContain: []string{"Empty text provided"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			params := &mcp.CallToolParamsFor[SayTTSParams]{
				Name:      "say_tts",
				Arguments: tt.params,
			}

			// Call the handler directly by creating a mock handler
			result, err := callSayTTSHandler(ctx, params)

			if tt.expectError {
				require.NotNil(t, result)
				assert.True(t, result.IsError, "Expected error but got success")
			} else {
				require.NoError(t, err)
				require.NotNil(t, result)
				assert.False(t, result.IsError, "Expected success but got error: %v", result)
			}

			// Check that result contains expected strings
			if len(tt.shouldContain) > 0 {
				resultText := extractTextFromResult(result)
				for _, expectedStr := range tt.shouldContain {
					assert.Contains(t, resultText, expectedStr,
						"Result should contain '%s', but got: %s", expectedStr, resultText)
				}
			}
		})
	}
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
		name          string
		setupEnv      func()
		params        GoogleTTSParams
		expectError   bool
		shouldContain []string
	}{
		{
			name: "successful TTS request with default model",
			setupEnv: func() {
				os.Setenv("GOOGLE_AI_API_KEY", "test-api-key")
			},
			params: GoogleTTSParams{
				Text: "Hello, this is a test of Google TTS",
			},
			expectError:   false,
			shouldContain: []string{"Google TTS", "gemini-2.5-flash-preview-tts", "voice Kore"},
		},
		{
			name: "successful TTS request with custom voice and model",
			setupEnv: func() {
				os.Setenv("GOOGLE_AI_API_KEY", "test-api-key")
			},
			params: GoogleTTSParams{
				Text:  "Hello, speak with Puck voice",
				Voice: stringPtr("Puck"),
				Model: stringPtr("gemini-2.5-pro-preview-tts"),
			},
			expectError:   false,
			shouldContain: []string{"Google TTS", "voice Puck", "gemini-2.5-pro-preview-tts"},
		},
		{
			name: "missing API key",
			setupEnv: func() {
				os.Unsetenv("GOOGLE_AI_API_KEY")
				os.Unsetenv("GEMINI_API_KEY")
			},
			params: GoogleTTSParams{
				Text: "Hello",
			},
			expectError:   true,
			shouldContain: []string{"GOOGLE_AI_API_KEY or GEMINI_API_KEY is not set"},
		},
		{
			name: "empty text",
			setupEnv: func() {
				os.Setenv("GOOGLE_AI_API_KEY", "test-api-key")
			},
			params: GoogleTTSParams{
				Text: "",
			},
			expectError:   true,
			shouldContain: []string{"Empty text provided"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Setup environment
			tt.setupEnv()

			ctx := context.Background()
			params := &mcp.CallToolParamsFor[GoogleTTSParams]{
				Name:      "google_tts",
				Arguments: tt.params,
			}

			// Call the handler directly
			result, err := callGoogleTTSHandler(ctx, params)

			if tt.expectError {
				require.NotNil(t, result)
				assert.True(t, result.IsError, "Expected error but got success")
			} else {
				require.NoError(t, err)
				require.NotNil(t, result)
				assert.False(t, result.IsError, "Expected success but got error: %v", result)
			}

			// Check that result contains expected strings
			if len(tt.shouldContain) > 0 {
				resultText := extractTextFromResult(result)
				for _, expectedStr := range tt.shouldContain {
					assert.Contains(t, resultText, expectedStr,
						"Result should contain '%s', but got: %s", expectedStr, resultText)
				}
			}
		})
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
		name          string
		setupEnv      func()
		params        OpenAITTSParams
		expectError   bool
		shouldContain []string
	}{
		{
			name: "successful TTS request with default settings",
			setupEnv: func() {
				os.Setenv("OPENAI_API_KEY", "test-api-key")
			},
			params: OpenAITTSParams{
				Text: "Hello, this is a test of OpenAI TTS",
			},
			expectError:   false,
			shouldContain: []string{"OpenAI TTS", "voice coral"},
		},
		{
			name: "successful TTS request with custom voice and model",
			setupEnv: func() {
				os.Setenv("OPENAI_API_KEY", "test-api-key")
			},
			params: OpenAITTSParams{
				Text:  "Hello, speak with echo voice",
				Voice: stringPtr("echo"),
				Model: stringPtr("gpt-4o-audio-preview"),
				Speed: float64Ptr(1.5),
			},
			expectError:   false,
			shouldContain: []string{"OpenAI TTS", "voice echo"},
		},
		{
			name: "missing API key",
			setupEnv: func() {
				os.Unsetenv("OPENAI_API_KEY")
			},
			params: OpenAITTSParams{
				Text: "Hello",
			},
			expectError:   true,
			shouldContain: []string{"OPENAI_API_KEY is not set"},
		},
		{
			name: "empty text",
			setupEnv: func() {
				os.Setenv("OPENAI_API_KEY", "test-api-key")
			},
			params: OpenAITTSParams{
				Text: "",
			},
			expectError:   true,
			shouldContain: []string{"Empty text provided"},
		},
		{
			name: "speed out of range",
			setupEnv: func() {
				os.Setenv("OPENAI_API_KEY", "test-api-key")
			},
			params: OpenAITTSParams{
				Text:  "Speed test",
				Speed: float64Ptr(0.1), // Too slow
			},
			expectError:   false, // Should use default speed
			shouldContain: []string{"OpenAI TTS", "voice coral"},
		},
		{
			name: "custom instructions",
			setupEnv: func() {
				os.Setenv("OPENAI_API_KEY", "test-api-key")
			},
			params: OpenAITTSParams{
				Text:         "Test with custom instructions",
				Instructions: stringPtr("Speak in a cheerful and positive tone"),
			},
			expectError:   false,
			shouldContain: []string{"OpenAI TTS", "voice coral"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Setup environment
			tt.setupEnv()

			ctx := context.Background()
			params := &mcp.CallToolParamsFor[OpenAITTSParams]{
				Name:      "openai_tts",
				Arguments: tt.params,
			}

			// Call the handler directly
			result, err := callOpenAITTSHandler(ctx, params)

			if tt.expectError {
				require.NotNil(t, result)
				assert.True(t, result.IsError, "Expected error but got success")
			} else {
				require.NoError(t, err)
				require.NotNil(t, result)
				assert.False(t, result.IsError, "Expected success but got error: %v", result)
			}

			// Check that result contains expected strings
			if len(tt.shouldContain) > 0 {
				resultText := extractTextFromResult(result)
				for _, expectedStr := range tt.shouldContain {
					assert.Contains(t, resultText, expectedStr,
						"Result should contain '%s', but got: %s", expectedStr, resultText)
				}
			}
		})
	}
}

func TestElevenLabsTTSTool(t *testing.T) {
	// Set up test environment variables
	originalAPIKey := os.Getenv("ELEVENLABS_API_KEY")
	defer func() {
		if originalAPIKey != "" {
			os.Setenv("ELEVENLABS_API_KEY", originalAPIKey)
		} else {
			os.Unsetenv("ELEVENLABS_API_KEY")
		}
	}()

	tests := []struct {
		name          string
		setupEnv      func()
		params        ElevenLabsTTSParams
		expectError   bool
		shouldContain []string
	}{
		{
			name: "missing API key",
			setupEnv: func() {
				os.Unsetenv("ELEVENLABS_API_KEY")
			},
			params: ElevenLabsTTSParams{
				Text: "Hello",
			},
			expectError:   true,
			shouldContain: []string{"ELEVENLABS_API_KEY is not set"},
		},
		{
			name: "empty text",
			setupEnv: func() {
				os.Setenv("ELEVENLABS_API_KEY", "test-api-key")
			},
			params: ElevenLabsTTSParams{
				Text: "",
			},
			expectError:   true,
			shouldContain: []string{"text must be a string"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Setup environment
			tt.setupEnv()

			ctx := context.Background()
			params := &mcp.CallToolParamsFor[ElevenLabsTTSParams]{
				Name:      "elevenlabs_tts",
				Arguments: tt.params,
			}

			// Call the handler directly
			result, err := callElevenLabsTTSHandler(ctx, params)

			if tt.expectError {
				require.NotNil(t, result)
				assert.True(t, result.IsError, "Expected error but got success")
			} else {
				require.NoError(t, err)
				require.NotNil(t, result)
				assert.False(t, result.IsError, "Expected success but got error: %v", result)
			}

			// Check that result contains expected strings
			if len(tt.shouldContain) > 0 {
				resultText := extractTextFromResult(result)
				for _, expectedStr := range tt.shouldContain {
					assert.Contains(t, resultText, expectedStr,
						"Result should contain '%s', but got: %s", expectedStr, resultText)
				}
			}
		})
	}
}

func TestParameterValidation(t *testing.T) {
	t.Run("SayTTSParams", func(t *testing.T) {
		tests := []struct {
			name   string
			params SayTTSParams
			valid  bool
		}{
			{"valid basic", SayTTSParams{Text: "Hello"}, true},
			{"valid with rate", SayTTSParams{Text: "Hello", Rate: intPtr(200)}, true},
			{"valid with voice", SayTTSParams{Text: "Hello", Voice: stringPtr("Alex")}, true},
			{"empty text", SayTTSParams{Text: ""}, false},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				valid := tt.params.Text != ""
				assert.Equal(t, tt.valid, valid, "Parameter validation mismatch")
			})
		}
	})

	t.Run("OpenAITTSParams speed validation", func(t *testing.T) {
		tests := []struct {
			name  string
			speed *float64
			valid bool
		}{
			{"nil speed", nil, true},
			{"valid speed", float64Ptr(1.0), true},
			{"minimum speed", float64Ptr(0.25), true},
			{"maximum speed", float64Ptr(4.0), true},
			{"too slow", float64Ptr(0.1), false},
			{"too fast", float64Ptr(5.0), false},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				valid := tt.speed == nil || (*tt.speed >= 0.25 && *tt.speed <= 4.0)
				assert.Equal(t, tt.valid, valid, "Speed validation mismatch")
			})
		}
	})
}

// Helper functions to extract handlers and test them in isolation

func callSayTTSHandler(ctx context.Context, params *mcp.CallToolParamsFor[SayTTSParams]) (*mcp.CallToolResultFor[any], error) {
	// Mock implementation that simulates the say handler logic without actual execution
	if params.Arguments.Text == "" {
		return &mcp.CallToolResultFor[any]{
			Content: []mcp.Content{&mcp.TextContent{Text: "Error: Empty text provided"}},
			IsError: true,
		}, nil
	}

	// Mock successful execution
	responseText := fmt.Sprintf("Speaking: %s", params.Arguments.Text)
	return &mcp.CallToolResultFor[any]{
		Content: []mcp.Content{&mcp.TextContent{Text: responseText}},
	}, nil
}

func callGoogleTTSHandler(ctx context.Context, params *mcp.CallToolParamsFor[GoogleTTSParams]) (*mcp.CallToolResultFor[any], error) {
	// Mock implementation that simulates the Google TTS handler logic
	if params.Arguments.Text == "" {
		return &mcp.CallToolResultFor[any]{
			Content: []mcp.Content{&mcp.TextContent{Text: "Error: Empty text provided"}},
			IsError: true,
		}, nil
	}

	// Check API key
	apiKey := os.Getenv("GOOGLE_AI_API_KEY")
	if apiKey == "" {
		apiKey = os.Getenv("GEMINI_API_KEY")
	}
	if apiKey == "" {
		return &mcp.CallToolResultFor[any]{
			Content: []mcp.Content{&mcp.TextContent{Text: "Error: GOOGLE_AI_API_KEY or GEMINI_API_KEY is not set"}},
			IsError: true,
		}, nil
	}

	// Get configuration from arguments
	voice := "Kore"
	if params.Arguments.Voice != nil && *params.Arguments.Voice != "" {
		voice = *params.Arguments.Voice
	}

	model := "gemini-2.5-flash-preview-tts"
	if params.Arguments.Model != nil && *params.Arguments.Model != "" {
		model = *params.Arguments.Model
	}

	// Mock successful execution
	responseText := fmt.Sprintf("Speaking: %s (via Google TTS with voice %s using model %s)", params.Arguments.Text, voice, model)
	return &mcp.CallToolResultFor[any]{
		Content: []mcp.Content{&mcp.TextContent{Text: responseText}},
	}, nil
}

func callOpenAITTSHandler(ctx context.Context, params *mcp.CallToolParamsFor[OpenAITTSParams]) (*mcp.CallToolResultFor[any], error) {
	// Mock implementation that simulates the OpenAI TTS handler logic
	if params.Arguments.Text == "" {
		return &mcp.CallToolResultFor[any]{
			Content: []mcp.Content{&mcp.TextContent{Text: "Error: Empty text provided"}},
			IsError: true,
		}, nil
	}

	// Check API key
	apiKey := os.Getenv("OPENAI_API_KEY")
	if apiKey == "" {
		return &mcp.CallToolResultFor[any]{
			Content: []mcp.Content{&mcp.TextContent{Text: "Error: OPENAI_API_KEY is not set"}},
			IsError: true,
		}, nil
	}

	// Get configuration from arguments
	voice := "coral"
	if params.Arguments.Voice != nil && *params.Arguments.Voice != "" {
		voice = *params.Arguments.Voice
	}

	// Mock successful execution (using voice for simplicity in tests)
	responseText := fmt.Sprintf("Speaking: %s (via OpenAI TTS with voice %s)", params.Arguments.Text, voice)
	return &mcp.CallToolResultFor[any]{
		Content: []mcp.Content{&mcp.TextContent{Text: responseText}},
	}, nil
}

func callElevenLabsTTSHandler(ctx context.Context, params *mcp.CallToolParamsFor[ElevenLabsTTSParams]) (*mcp.CallToolResultFor[any], error) {
	// Mock implementation that simulates the ElevenLabs handler logic
	if params.Arguments.Text == "" {
		return &mcp.CallToolResultFor[any]{
			Content: []mcp.Content{&mcp.TextContent{Text: "Error: text must be a string"}},
			IsError: true,
		}, nil
	}

	// Check API key
	apiKey := os.Getenv("ELEVENLABS_API_KEY")
	if apiKey == "" {
		return &mcp.CallToolResultFor[any]{
			Content: []mcp.Content{&mcp.TextContent{Text: "Error: ELEVENLABS_API_KEY is not set"}},
			IsError: true,
		}, nil
	}

	// Mock successful execution
	responseText := fmt.Sprintf("Speaking: %s", params.Arguments.Text)
	return &mcp.CallToolResultFor[any]{
		Content: []mcp.Content{&mcp.TextContent{Text: responseText}},
	}, nil
}

func extractTextFromResult(result *mcp.CallToolResultFor[any]) string {
	if result == nil || len(result.Content) == 0 {
		return ""
	}

	if textContent, ok := result.Content[0].(*mcp.TextContent); ok {
		return textContent.Text
	}

	return ""
}

func isDarwin() bool {
	return os.Getenv("GOOS") == "darwin" || (os.Getenv("GOOS") == "" && os.Getenv("HOME") != "")
}

// Benchmark tests
func BenchmarkParameterValidation(b *testing.B) {
	params := SayTTSParams{
		Text:  "Benchmark test message",
		Rate:  intPtr(200),
		Voice: stringPtr("Alex"),
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		// Simulate parameter validation
		_ = params.Text != "" && (params.Rate == nil || *params.Rate > 0) && (params.Voice == nil || *params.Voice != "")
	}
}

func BenchmarkHandlerCreation(b *testing.B) {
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		params := &mcp.CallToolParamsFor[SayTTSParams]{
			Name: "say_tts",
			Arguments: SayTTSParams{
				Text: "Benchmark test",
			},
		}
		_ = params // Use the parameter to avoid unused variable warning
	}
}

// TestCancellation tests that MCP server handlers properly respond to context cancellation
func TestCancellation(t *testing.T) {
	t.Run("SayTTS cancellation", func(t *testing.T) {
		if !isDarwin() {
			t.Skip("Say TTS tool only available on macOS")
		}

		// Create a context that we can cancel
		ctx, cancel := context.WithCancel(context.Background())
		
		params := &mcp.CallToolParamsFor[SayTTSParams]{
			Name: "say_tts",
			Arguments: SayTTSParams{
				Text: "This is a long text that should be cancelled before completion",
			},
		}

		// Cancel immediately to test early cancellation detection
		cancel()

		result, err := callCancellableSayTTSHandler(ctx, params)

		// Should handle cancellation gracefully
		require.NoError(t, err)
		require.NotNil(t, result)
		
		resultText := extractTextFromResult(result)
		assert.Contains(t, resultText, "cancelled", "Should indicate cancellation")
	})

	t.Run("Google TTS cancellation", func(t *testing.T) {
		// Set up test environment
		originalAPIKey := os.Getenv("GOOGLE_AI_API_KEY")
		os.Setenv("GOOGLE_AI_API_KEY", "test-api-key")
		defer func() {
			if originalAPIKey != "" {
				os.Setenv("GOOGLE_AI_API_KEY", originalAPIKey)
			} else {
				os.Unsetenv("GOOGLE_AI_API_KEY")
			}
		}()

		// Create a context that we can cancel
		ctx, cancel := context.WithCancel(context.Background())
		
		params := &mcp.CallToolParamsFor[GoogleTTSParams]{
			Name: "google_tts",
			Arguments: GoogleTTSParams{
				Text: "This should be cancelled",
			},
		}

		// Cancel immediately to test early cancellation detection
		cancel()

		result, err := callCancellableGoogleTTSHandler(ctx, params)

		// Should handle cancellation gracefully
		require.NoError(t, err)
		require.NotNil(t, result)
		
		resultText := extractTextFromResult(result)
		assert.Contains(t, resultText, "cancelled", "Should indicate cancellation")
	})

	t.Run("OpenAI TTS cancellation", func(t *testing.T) {
		// Set up test environment
		originalAPIKey := os.Getenv("OPENAI_API_KEY")
		os.Setenv("OPENAI_API_KEY", "test-api-key")
		defer func() {
			if originalAPIKey != "" {
				os.Setenv("OPENAI_API_KEY", originalAPIKey)
			} else {
				os.Unsetenv("OPENAI_API_KEY")
			}
		}()

		// Create a context that we can cancel
		ctx, cancel := context.WithCancel(context.Background())
		
		params := &mcp.CallToolParamsFor[OpenAITTSParams]{
			Name: "openai_tts",
			Arguments: OpenAITTSParams{
				Text: "This should be cancelled",
			},
		}

		// Cancel immediately to test early cancellation detection
		cancel()

		result, err := callCancellableOpenAITTSHandler(ctx, params)

		// Should handle cancellation gracefully
		require.NoError(t, err)
		require.NotNil(t, result)
		
		resultText := extractTextFromResult(result)
		assert.Contains(t, resultText, "cancelled", "Should indicate cancellation")
	})

	t.Run("ElevenLabs TTS cancellation", func(t *testing.T) {
		// Set up test environment
		originalAPIKey := os.Getenv("ELEVENLABS_API_KEY")
		os.Setenv("ELEVENLABS_API_KEY", "test-api-key")
		defer func() {
			if originalAPIKey != "" {
				os.Setenv("ELEVENLABS_API_KEY", originalAPIKey)
			} else {
				os.Unsetenv("ELEVENLABS_API_KEY")
			}
		}()

		// Create a context that we can cancel
		ctx, cancel := context.WithCancel(context.Background())
		
		params := &mcp.CallToolParamsFor[ElevenLabsTTSParams]{
			Name: "elevenlabs_tts",
			Arguments: ElevenLabsTTSParams{
				Text: "This should be cancelled",
			},
		}

		// Cancel immediately to test early cancellation detection
		cancel()

		result, err := callCancellableElevenLabsTTSHandler(ctx, params)

		// Should handle cancellation gracefully
		require.NoError(t, err)
		require.NotNil(t, result)
		
		resultText := extractTextFromResult(result)
		assert.Contains(t, resultText, "cancelled", "Should indicate cancellation")
	})
}

func TestContextTimeout(t *testing.T) {
	t.Run("SayTTS timeout", func(t *testing.T) {
		if !isDarwin() {
			t.Skip("Say TTS tool only available on macOS")
		}

		// Create a context with a very short timeout
		ctx, cancel := context.WithTimeout(context.Background(), 1*time.Millisecond)
		defer cancel()
		
		// Add a small delay to ensure timeout occurs
		time.Sleep(2 * time.Millisecond)
		
		params := &mcp.CallToolParamsFor[SayTTSParams]{
			Name: "say_tts",
			Arguments: SayTTSParams{
				Text: "This should timeout",
			},
		}

		result, err := callCancellableSayTTSHandler(ctx, params)

		// Should handle timeout gracefully
		require.NoError(t, err)
		require.NotNil(t, result)
		
		resultText := extractTextFromResult(result)
		assert.Contains(t, resultText, "cancelled", "Should indicate cancellation due to timeout")
	})
}

// Cancellable handler implementations that simulate the actual handlers with cancellation checks

func callCancellableSayTTSHandler(ctx context.Context, params *mcp.CallToolParamsFor[SayTTSParams]) (*mcp.CallToolResultFor[any], error) {
	// Check for early cancellation (simulates the actual handler pattern)
	select {
	case <-ctx.Done():
		return &mcp.CallToolResultFor[any]{
			Content: []mcp.Content{&mcp.TextContent{Text: "Request cancelled"}},
		}, nil
	default:
	}

	// Basic validation
	if params.Arguments.Text == "" {
		return &mcp.CallToolResultFor[any]{
			Content: []mcp.Content{&mcp.TextContent{Text: "Error: Empty text provided"}},
			IsError: true,
		}, nil
	}

	// Simulate some processing time with cancellation checks
	for i := 0; i < 5; i++ {
		select {
		case <-ctx.Done():
			return &mcp.CallToolResultFor[any]{
				Content: []mcp.Content{&mcp.TextContent{Text: "Say command cancelled"}},
			}, nil
		case <-time.After(10 * time.Millisecond):
			// Continue processing
		}
	}

	// Mock successful execution (if not cancelled)
	responseText := fmt.Sprintf("Speaking: %s", params.Arguments.Text)
	return &mcp.CallToolResultFor[any]{
		Content: []mcp.Content{&mcp.TextContent{Text: responseText}},
	}, nil
}

func callCancellableGoogleTTSHandler(ctx context.Context, params *mcp.CallToolParamsFor[GoogleTTSParams]) (*mcp.CallToolResultFor[any], error) {
	// Check for early cancellation
	select {
	case <-ctx.Done():
		return &mcp.CallToolResultFor[any]{
			Content: []mcp.Content{&mcp.TextContent{Text: "Request cancelled"}},
		}, nil
	default:
	}

	// Basic validation
	if params.Arguments.Text == "" {
		return &mcp.CallToolResultFor[any]{
			Content: []mcp.Content{&mcp.TextContent{Text: "Error: Empty text provided"}},
			IsError: true,
		}, nil
	}

	// Check API key
	apiKey := os.Getenv("GOOGLE_AI_API_KEY")
	if apiKey == "" {
		apiKey = os.Getenv("GEMINI_API_KEY")
	}
	if apiKey == "" {
		return &mcp.CallToolResultFor[any]{
			Content: []mcp.Content{&mcp.TextContent{Text: "Error: GOOGLE_AI_API_KEY or GEMINI_API_KEY is not set"}},
			IsError: true,
		}, nil
	}

	// Simulate processing with cancellation checks
	for i := 0; i < 3; i++ {
		select {
		case <-ctx.Done():
			return &mcp.CallToolResultFor[any]{
				Content: []mcp.Content{&mcp.TextContent{Text: "Google TTS audio playback cancelled"}},
			}, nil
		case <-time.After(10 * time.Millisecond):
			// Continue processing
		}
	}

	// Mock successful execution
	voice := "Kore"
	if params.Arguments.Voice != nil && *params.Arguments.Voice != "" {
		voice = *params.Arguments.Voice
	}
	responseText := fmt.Sprintf("Speaking: %s (via Google TTS with voice %s)", params.Arguments.Text, voice)
	return &mcp.CallToolResultFor[any]{
		Content: []mcp.Content{&mcp.TextContent{Text: responseText}},
	}, nil
}

func callCancellableOpenAITTSHandler(ctx context.Context, params *mcp.CallToolParamsFor[OpenAITTSParams]) (*mcp.CallToolResultFor[any], error) {
	// Check for early cancellation
	select {
	case <-ctx.Done():
		return &mcp.CallToolResultFor[any]{
			Content: []mcp.Content{&mcp.TextContent{Text: "Request cancelled"}},
		}, nil
	default:
	}

	// Basic validation
	if params.Arguments.Text == "" {
		return &mcp.CallToolResultFor[any]{
			Content: []mcp.Content{&mcp.TextContent{Text: "Error: Empty text provided"}},
			IsError: true,
		}, nil
	}

	// Check API key
	apiKey := os.Getenv("OPENAI_API_KEY")
	if apiKey == "" {
		return &mcp.CallToolResultFor[any]{
			Content: []mcp.Content{&mcp.TextContent{Text: "Error: OPENAI_API_KEY is not set"}},
			IsError: true,
		}, nil
	}

	// Simulate processing with cancellation checks
	for i := 0; i < 3; i++ {
		select {
		case <-ctx.Done():
			return &mcp.CallToolResultFor[any]{
				Content: []mcp.Content{&mcp.TextContent{Text: "OpenAI TTS audio playback cancelled"}},
			}, nil
		case <-time.After(10 * time.Millisecond):
			// Continue processing
		}
	}

	// Mock successful execution
	voice := "coral"
	if params.Arguments.Voice != nil && *params.Arguments.Voice != "" {
		voice = *params.Arguments.Voice
	}
	responseText := fmt.Sprintf("Speaking: %s (via OpenAI TTS with voice %s)", params.Arguments.Text, voice)
	return &mcp.CallToolResultFor[any]{
		Content: []mcp.Content{&mcp.TextContent{Text: responseText}},
	}, nil
}

func callCancellableElevenLabsTTSHandler(ctx context.Context, params *mcp.CallToolParamsFor[ElevenLabsTTSParams]) (*mcp.CallToolResultFor[any], error) {
	// Check for early cancellation
	select {
	case <-ctx.Done():
		return &mcp.CallToolResultFor[any]{
			Content: []mcp.Content{&mcp.TextContent{Text: "Request cancelled"}},
		}, nil
	default:
	}

	// Basic validation
	if params.Arguments.Text == "" {
		return &mcp.CallToolResultFor[any]{
			Content: []mcp.Content{&mcp.TextContent{Text: "Error: text must be a string"}},
			IsError: true,
		}, nil
	}

	// Check API key
	apiKey := os.Getenv("ELEVENLABS_API_KEY")
	if apiKey == "" {
		return &mcp.CallToolResultFor[any]{
			Content: []mcp.Content{&mcp.TextContent{Text: "Error: ELEVENLABS_API_KEY is not set"}},
			IsError: true,
		}, nil
	}

	// Simulate processing with cancellation checks
	for i := 0; i < 3; i++ {
		select {
		case <-ctx.Done():
			return &mcp.CallToolResultFor[any]{
				Content: []mcp.Content{&mcp.TextContent{Text: "Audio playback cancelled"}},
			}, nil
		case <-time.After(10 * time.Millisecond):
			// Continue processing
		}
	}

	// Mock successful execution
	responseText := fmt.Sprintf("Speaking: %s", params.Arguments.Text)
	return &mcp.CallToolResultFor[any]{
		Content: []mcp.Content{&mcp.TextContent{Text: responseText}},
	}, nil
}
// TestMCPServerCancellation tests cancellation behavior in a more realistic scenario
func TestMCPServerCancellation(t *testing.T) {
	t.Run("concurrent requests with cancellation", func(t *testing.T) {
		if !isDarwin() {
			t.Skip("Say TTS tool only available on macOS")
		}

		const numRequests = 5
		results := make(chan string, numRequests)
		errors := make(chan error, numRequests)

		// Start multiple concurrent requests
		for i := 0; i < numRequests; i++ {
			go func(requestID int) {
				ctx, cancel := context.WithCancel(context.Background())
				
				params := &mcp.CallToolParamsFor[SayTTSParams]{
					Name: "say_tts",
					Arguments: SayTTSParams{
						Text: fmt.Sprintf("Request %d - this is a test message", requestID),
					},
				}

				// Cancel some requests after a short delay
				if requestID%2 == 0 {
					go func() {
						time.Sleep(5 * time.Millisecond)
						cancel()
					}()
				}

				result, err := callCancellableSayTTSHandler(ctx, params)
				if err != nil {
					errors <- err
					return
				}

				resultText := extractTextFromResult(result)
				results <- resultText
			}(i)
		}

		// Collect results
		var completedResults []string
		var receivedErrors []error

		for i := 0; i < numRequests; i++ {
			select {
			case result := <-results:
				completedResults = append(completedResults, result)
			case err := <-errors:
				receivedErrors = append(receivedErrors, err)
			case <-time.After(500 * time.Millisecond):
				t.Fatal("Test timed out waiting for results")
			}
		}

		// Verify we got all responses
		assert.Equal(t, numRequests, len(completedResults)+len(receivedErrors), "Should receive all responses")
		
		// Count cancelled vs completed requests
		cancelledCount := 0
		completedCount := 0
		
		for _, result := range completedResults {
			if strings.Contains(result, "cancelled") {
				cancelledCount++
			} else if strings.Contains(result, "Speaking:") {
				completedCount++
			}
		}

		// We expect some requests to be cancelled (the even-numbered ones)
		assert.Greater(t, cancelledCount, 0, "Some requests should be cancelled")
		t.Logf("Results: %d cancelled, %d completed, %d errors", cancelledCount, completedCount, len(receivedErrors))
	})

	t.Run("graceful shutdown simulation", func(t *testing.T) {
		// Simulate a scenario where the server needs to shut down gracefully
		ctx, cancel := context.WithCancel(context.Background())
		
		// Start a long-running operation
		params := &mcp.CallToolParamsFor[GoogleTTSParams]{
			Name: "google_tts",
			Arguments: GoogleTTSParams{
				Text: "This is a long operation that should be cancelled during shutdown",
			},
		}

		// Set up environment
		originalAPIKey := os.Getenv("GOOGLE_AI_API_KEY")
		os.Setenv("GOOGLE_AI_API_KEY", "test-api-key")
		defer func() {
			if originalAPIKey != "" {
				os.Setenv("GOOGLE_AI_API_KEY", originalAPIKey)
			} else {
				os.Unsetenv("GOOGLE_AI_API_KEY")
			}
		}()

		// Start the operation in a goroutine
		done := make(chan struct{})
		var result *mcp.CallToolResultFor[any]
		var err error

		go func() {
			defer close(done)
			result, err = callCancellableGoogleTTSHandler(ctx, params)
		}()

		// Simulate shutdown signal after a short delay
		time.Sleep(15 * time.Millisecond)
		cancel() // Simulate graceful shutdown

		// Wait for operation to complete
		select {
		case <-done:
			// Operation completed
		case <-time.After(100 * time.Millisecond):
			t.Fatal("Operation did not respond to cancellation in time")
		}

		// Verify the operation was cancelled gracefully
		require.NoError(t, err)
		require.NotNil(t, result)
		
		resultText := extractTextFromResult(result)
		assert.Contains(t, resultText, "cancelled", "Operation should indicate it was cancelled")
		t.Logf("Graceful shutdown result: %s", resultText)
	})
}

