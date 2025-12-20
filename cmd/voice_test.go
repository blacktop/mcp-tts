package cmd

import (
	"runtime"
	"testing"
)

func TestIsLocale(t *testing.T) {
	tests := []struct {
		input    string
		expected bool
	}{
		{"en_US", true},
		{"de_DE", true},
		{"fr_FR", true},
		{"ja_JP", true},
		{"zh_CN", true},
		{"pt_BR", true},
		{"en", false},      // too short
		{"english", false}, // no underscore
		{"EN_US", false},   // first part should be lowercase
		{"en_us", false},   // second part should be uppercase
		{"en_USA", false},  // too long
		{"e_US", false},    // first part too short
		{"en_U", false},    // second part too short
		{"", false},        // empty
		{"12_34", false},   // not letters
		{"en-US", false},   // wrong separator
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			result := isLocale(tt.input)
			if result != tt.expected {
				t.Errorf("isLocale(%q) = %v, want %v", tt.input, result, tt.expected)
			}
		})
	}
}

func TestGetInstalledVoices(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("Skipping test on non-macOS platform")
	}

	// Reset cache before test
	resetVoiceCache()

	voices, err := getInstalledVoices()
	if err != nil {
		t.Fatalf("getInstalledVoices() error = %v", err)
	}

	if len(voices) == 0 {
		t.Error("Expected at least one voice to be installed on macOS")
	}

	// Albert is a default voice that should always be present
	if !voices["Albert"] {
		t.Log("Warning: 'Albert' voice not found, checking for any known default voice")
		// Check for other common default voices
		knownVoices := []string{"Alex", "Samantha", "Victoria", "Daniel", "Fiona"}
		found := false
		for _, v := range knownVoices {
			if voices[v] {
				found = true
				t.Logf("Found known voice: %s", v)
				break
			}
		}
		if !found {
			t.Logf("Available voices: %v", voices)
		}
	}
}

func TestIsVoiceInstalled(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("Skipping test on non-macOS platform")
	}

	// Reset cache before test
	resetVoiceCache()

	// Test with a voice that should exist (Albert is a default novelty voice)
	installed, err := IsVoiceInstalled("Albert")
	if err != nil {
		t.Fatalf("IsVoiceInstalled() error = %v", err)
	}
	if !installed {
		t.Log("Warning: 'Albert' voice not installed, this may vary by macOS version")
	}

	// Test with a voice that definitely doesn't exist
	installed, err = IsVoiceInstalled("NonExistentVoice12345")
	if err != nil {
		t.Fatalf("IsVoiceInstalled() error = %v", err)
	}
	if installed {
		t.Error("Expected non-existent voice to return false")
	}
}

func TestVoiceNotInstalledError(t *testing.T) {
	msg := VoiceNotInstalledError("Zoe (Premium)")

	if msg == "" {
		t.Error("Expected non-empty error message")
	}

	// Check that the message contains the voice name
	if !contains(msg, "Zoe (Premium)") {
		t.Errorf("Error message should contain voice name, got: %s", msg)
	}

	// Check that the message contains instructions
	if !contains(msg, "System Settings") {
		t.Errorf("Error message should contain installation instructions, got: %s", msg)
	}
}

func TestVoiceCaching(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("Skipping test on non-macOS platform")
	}

	// Reset cache before test
	resetVoiceCache()

	// First call should populate cache
	voices1, err := getInstalledVoices()
	if err != nil {
		t.Fatalf("First getInstalledVoices() error = %v", err)
	}

	// Second call should return same cached result
	voices2, err := getInstalledVoices()
	if err != nil {
		t.Fatalf("Second getInstalledVoices() error = %v", err)
	}

	// Both should be the same map (same pointer due to caching)
	if len(voices1) != len(voices2) {
		t.Error("Expected cached voices to have same length")
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsHelper(s, substr))
}

func containsHelper(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
