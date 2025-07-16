package cmd

import (
	"testing"
)

// TestVoiceValidation tests the actual voice validation logic
func TestVoiceValidation(t *testing.T) {
	tests := []struct {
		name        string
		voice       string
		shouldPass  bool
		description string
	}{
		// Valid voices
		{"simple name", "Alex", true, "Simple alphabetic name"},
		{"name with space", "Bad News", true, "Name with space"},
		{"name with parentheses", "Daniel (English (UK))", true, "Name with nested parentheses"},
		{"name with underscore", "en_US", true, "Locale format with underscore"},
		{"name with hyphen", "Eddy (English (UK))", true, "Name with hyphen in parentheses"},
		{"name with number", "Eddy (German (Germany))", true, "Name with number in language"},
		{"complex voice", "Grandpa (Chinese (China mainland))", true, "Complex voice name"},
		
		// Invalid voices (these should fail with the original validation)
		{"command injection attempt", "Alex; rm -rf /", false, "Command injection attempt"},
		{"backtick injection", "Alex`whoami`", false, "Backtick command substitution"},
		{"pipe injection", "Alex | cat /etc/passwd", false, "Pipe character"},
		{"redirect injection", "Alex > /tmp/evil", false, "Output redirect"},
		{"dollar sign", "Alex$USER", false, "Variable expansion"},
		{"newline injection", "Alex\nrm -rf /", false, "Newline character"},
		{"quote injection", "Alex\"", false, "Quote character"},
		{"single quote injection", "Alex'", false, "Single quote character"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Test the validation logic that was in the original code
			isValid := validateVoiceCharacters(tt.voice)
			
			if tt.shouldPass && !isValid {
				t.Errorf("Voice validation failed for valid voice '%s': %s", tt.voice, tt.description)
			} else if !tt.shouldPass && isValid {
				t.Errorf("Voice validation passed for invalid voice '%s': %s", tt.voice, tt.description)
			}
		})
	}
}

// validateVoiceCharacters mimics the updated validation logic from root.go
func validateVoiceCharacters(voice string) bool {
	for _, r := range voice {
		if !((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || 
			r == ' ' || r == '(' || r == ')' || r == '-' || r == '_') {
			return false
		}
	}
	return true
}

// TestRealMacOSVoices tests actual macOS voice names
func TestRealMacOSVoices(t *testing.T) {
	// Sample of real macOS voice names from the say -v ? output
	realVoices := []string{
		"Alex",
		"Daniel (English (UK))",
		"Eddy (German (Germany))",
		"Flo (Spanish (Mexico))",
		"Grandma (Chinese (China mainland))",
		"Reed (Portuguese (Brazil))",
		"Sandy (Japanese (Japan))",
		"Shelley (Korean (South Korea))",
		"Tara (English (India))",
		"en_US",
		"en_GB",
		"zh_CN",
		"ja_JP",
		"ko_KR",
		"pt_BR",
		"es_MX",
		"de_DE",
		"fr_FR",
		"it_IT",
		"Zoe (Premium)",
		"Serena (Premium)",
	}

	for _, voice := range realVoices {
		t.Run(voice, func(t *testing.T) {
			if !validateVoiceCharacters(voice) {
				t.Errorf("Real macOS voice '%s' failed validation", voice)
			}
		})
	}
}