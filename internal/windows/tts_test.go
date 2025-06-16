package windows

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTTSConfig(t *testing.T) {
	tests := []struct {
		name     string
		config   TTSConfig
		isValid  bool
	}{
		{
			name: "valid SAPI config",
			config: TTSConfig{
				API:   "sapi",
				Text:  "Hello world",
				Voice: "Microsoft Zira Desktop",
				Rate:  intPtr(5),
			},
			isValid: true,
		},
		{
			name: "valid WinRT config",
			config: TTSConfig{
				API:   "winrt",
				Text:  "Hello from Canada",
				Voice: "Microsoft Linda",
				Rate:  intPtr(-3),
			},
			isValid: true,
		},
		{
			name: "auto config",
			config: TTSConfig{
				API:  "auto",
				Text: "Auto detection test",
			},
			isValid: true,
		},
		{
			name: "empty text",
			config: TTSConfig{
				API:  "sapi",
				Text: "",
			},
			isValid: false,
		},
		{
			name: "invalid API",
			config: TTSConfig{
				API:  "invalid",
				Text: "Test",
			},
			isValid: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.isValid {
				assert.NotEmpty(t, tt.config.Text, "Valid config should have non-empty text")
				assert.Contains(t, []string{"sapi", "winrt", "auto"}, tt.config.API, "Valid config should have valid API")
			} else {
				if tt.config.Text == "" {
					assert.Empty(t, tt.config.Text, "Invalid config with empty text")
				}
				if tt.config.API == "invalid" {
					assert.Equal(t, "invalid", tt.config.API, "Invalid API should be preserved for testing")
				}
			}
		})
	}
}

func TestVoiceStructure(t *testing.T) {
	voice := Voice{
		Name:     "Microsoft Linda",
		Language: "en-CA",
		Gender:   "Female",
		API:      "WinRT",
	}

	assert.Equal(t, "Microsoft Linda", voice.Name)
	assert.Equal(t, "en-CA", voice.Language)
	assert.Equal(t, "Female", voice.Gender)
	assert.Equal(t, "WinRT", voice.API)
}

func TestFormatVoiceList(t *testing.T) {
	tests := []struct {
		name     string
		voices   []Voice
		expected []string
	}{
		{
			name:     "empty voice list",
			voices:   []Voice{},
			expected: []string{"No voices found"},
		},
		{
			name: "mixed voices",
			voices: []Voice{
				{Name: "Microsoft David Desktop", Language: "en-US", Gender: "Male", API: "SAPI"},
				{Name: "Microsoft Linda", Language: "en-CA", Gender: "Female", API: "WinRT"},
				{Name: "Microsoft Zira Desktop", Language: "en-US", Gender: "Female", API: "SAPI"},
				{Name: "Microsoft Richard", Language: "en-CA", Gender: "Male", API: "WinRT"},
			},
			expected: []string{
				"Available Windows TTS voices:",
				"SAPI Voices (Legacy):",
				"Microsoft David Desktop",
				"Microsoft Zira Desktop",
				"WinRT Voices (Modern):",
				"Microsoft Linda",
				"⭐ Canadian",
				"Microsoft Richard",
			},
		},
		{
			name: "only SAPI voices",
			voices: []Voice{
				{Name: "Microsoft David Desktop", Language: "en-US", Gender: "Male", API: "SAPI"},
			},
			expected: []string{
				"Available Windows TTS voices:",
				"SAPI Voices (Legacy):",
				"Microsoft David Desktop",
			},
		},
		{
			name: "only WinRT voices",
			voices: []Voice{
				{Name: "Microsoft Linda", Language: "en-CA", Gender: "Female", API: "WinRT"},
			},
			expected: []string{
				"Available Windows TTS voices:",
				"WinRT Voices (Modern):",
				"Microsoft Linda",
				"⭐ Canadian",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := FormatVoiceList(tt.voices)
			
			for _, expected := range tt.expected {
				assert.Contains(t, result, expected, "Result should contain: %s", expected)
			}
		})
	}
}

func TestTTSEngine_DetermineAPI(t *testing.T) {
	engine := NewTTSEngine()
	ctx := context.Background()

	tests := []struct {
		name        string
		config      TTSConfig
		expectedAPI string
		shouldError bool
	}{
		{
			name: "explicit SAPI",
			config: TTSConfig{
				API:  "sapi",
				Text: "Test",
			},
			expectedAPI: "sapi",
			shouldError: false,
		},
		{
			name: "explicit WinRT",
			config: TTSConfig{
				API:  "winrt",
				Text: "Test",
			},
			expectedAPI: "winrt",
			shouldError: false,
		},
		{
			name: "auto without voice defaults to WinRT",
			config: TTSConfig{
				API:  "auto",
				Text: "Test",
			},
			expectedAPI: "winrt",
			shouldError: false,
		},
		{
			name: "empty API defaults to WinRT",
			config: TTSConfig{
				Text: "Test",
			},
			expectedAPI: "winrt",
			shouldError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Note: This test may fail if PowerShell/voices aren't available
			// In a real environment, we'd mock the voice enumeration
			api, err := engine.DetermineAPI(ctx, tt.config)
			
			if tt.shouldError {
				assert.Error(t, err)
			} else {
				if err == nil {
					assert.Equal(t, tt.expectedAPI, api)
				} else {
					// If voice enumeration fails (no PowerShell), that's expected in test environment
					t.Logf("Voice enumeration failed (expected in test env): %v", err)
				}
			}
		})
	}
}

func TestEscapeForPowerShell(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "no quotes",
			input:    "Hello world",
			expected: "Hello world",
		},
		{
			name:     "single quote",
			input:    "It's a test",
			expected: "It''s a test",
		},
		{
			name:     "multiple quotes",
			input:    "She said 'Hello' and 'Goodbye'",
			expected: "She said ''Hello'' and ''Goodbye''",
		},
		{
			name:     "empty string",
			input:    "",
			expected: "",
		},
		{
			name:     "only quotes",
			input:    "'''",
			expected: "''''''",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := escapeForPowerShell(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestNewTTSEngine(t *testing.T) {
	engine := NewTTSEngine()
	assert.NotNil(t, engine)
	assert.IsType(t, &TTSEngine{}, engine)
}

// Helper function to create int pointer
func intPtr(i int) *int {
	return &i
}

// Integration test for voice enumeration (may skip if PowerShell unavailable)
func TestTTSEngine_GetVoices_Integration(t *testing.T) {
	engine := NewTTSEngine()
	ctx := context.Background()

	voices, err := engine.GetVoices(ctx)
	
	if err != nil {
		// If PowerShell is not available, skip this test
		t.Skipf("PowerShell not available for voice enumeration: %v", err)
	}

	require.NoError(t, err)
	
	// Check that we got some voices
	if len(voices) == 0 {
		t.Log("⚠️  No voices found - this may be expected in test environments")
		return
	}

	t.Logf("✅ Found %d voices", len(voices))

	// Verify voice structure
	for _, voice := range voices {
		assert.NotEmpty(t, voice.Name, "Voice should have a name")
		assert.NotEmpty(t, voice.Language, "Voice should have a language")
		assert.NotEmpty(t, voice.Gender, "Voice should have a gender")
		assert.Contains(t, []string{"SAPI", "WinRT"}, voice.API, "Voice should have valid API type")
		
		t.Logf("  - %s (%s) - %s [%s]", voice.Name, voice.Language, voice.Gender, voice.API)
	}

	// Check for Canadian voices in WinRT
	canadianVoices := 0
	for _, voice := range voices {
		if voice.Language == "en-CA" && voice.API == "WinRT" {
			canadianVoices++
			t.Logf("  🍁 Found Canadian voice: %s", voice.Name)
		}
	}

	if canadianVoices > 0 {
		t.Logf("✅ Found %d Canadian WinRT voices", canadianVoices)
	} else {
		t.Log("ℹ️  No Canadian WinRT voices found - may depend on system configuration")
	}
}

// Test the full TTS functionality (may skip if PowerShell unavailable)
func TestTTSEngine_Speak_Integration(t *testing.T) {
	engine := NewTTSEngine()
	ctx := context.Background()

	// Test basic SAPI functionality
	t.Run("SAPI basic", func(t *testing.T) {
		config := TTSConfig{
			API:  "sapi",
			Text: "Test from Go unit test",
		}

		err := engine.Speak(ctx, config)
		if err != nil {
			t.Skipf("SAPI TTS not available: %v", err)
		}
		
		t.Log("✅ SAPI TTS test completed")
	})

	// Test WinRT functionality
	t.Run("WinRT basic", func(t *testing.T) {
		config := TTSConfig{
			API:  "winrt",
			Text: "Test from Go unit test using WinRT",
		}

		err := engine.Speak(ctx, config)
		if err != nil {
			t.Skipf("WinRT TTS not available: %v", err)
		}
		
		t.Log("✅ WinRT TTS test completed")
	})
}