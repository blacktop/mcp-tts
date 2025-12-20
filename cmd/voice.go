package cmd

import (
	"os/exec"
	"runtime"
	"strings"
	"sync"
)

// VoiceAvailability holds cached voice availability data
type VoiceAvailability struct {
	voices map[string]bool
	once   sync.Once
	err    error
}

var voiceCache VoiceAvailability

// getInstalledVoices runs `say -v?` and parses the output to get all installed voices.
// Results are cached for the lifetime of the process.
func getInstalledVoices() (map[string]bool, error) {
	voiceCache.once.Do(func() {
		voiceCache.voices = make(map[string]bool)

		if runtime.GOOS != "darwin" {
			// Not on macOS, no voices available
			return
		}

		out, err := exec.Command("/usr/bin/say", "-v?").Output()
		if err != nil {
			voiceCache.err = err
			return
		}

		// Parse output - each line starts with voice name, followed by locale
		// Example: "Albert              en_US    # Hello! My name is Albert."
		// Example: "Eddy (German (Germany)) de_DE    # Hallo! Ich heiße Eddy."
		for line := range strings.SplitSeq(string(out), "\n") {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}

			// Find the locale pattern (xx_XX) to determine where the voice name ends
			// The voice name is everything before the locale
			parts := strings.Fields(line)
			if len(parts) < 2 {
				continue
			}

			// Find the locale column - it's the first field that matches xx_XX pattern
			voiceNameParts := []string{}
			for i, part := range parts {
				if isLocale(part) {
					// Everything before this is the voice name
					voiceNameParts = parts[:i]
					break
				}
			}

			if len(voiceNameParts) == 0 {
				// Fallback: just use the first part as voice name
				voiceNameParts = parts[:1]
			}

			voiceName := strings.Join(voiceNameParts, " ")
			voiceCache.voices[voiceName] = true
		}
	})

	return voiceCache.voices, voiceCache.err
}

// isLocale checks if a string looks like a locale code (e.g., en_US, de_DE)
func isLocale(s string) bool {
	if len(s) != 5 {
		return false
	}
	// Pattern: xx_XX where x is lowercase and X is uppercase
	return s[0] >= 'a' && s[0] <= 'z' &&
		s[1] >= 'a' && s[1] <= 'z' &&
		s[2] == '_' &&
		s[3] >= 'A' && s[3] <= 'Z' &&
		s[4] >= 'A' && s[4] <= 'Z'
}

// IsVoiceInstalled checks if a specific voice is installed on the system.
// Returns (isInstalled, error). If error is non-nil, voice availability couldn't be determined.
func IsVoiceInstalled(voiceName string) (bool, error) {
	voices, err := getInstalledVoices()
	if err != nil {
		return false, err
	}

	return voices[voiceName], nil
}

// VoiceNotInstalledError returns a user-friendly error message for missing voices
func VoiceNotInstalledError(voiceName string) string {
	return "Voice \"" + voiceName + "\" is not installed. " +
		"To download additional voices, go to: System Settings → Accessibility → Spoken Content → System Voice → Manage Voices"
}

// resetVoiceCache is used for testing to reset the cached voice data
func resetVoiceCache() {
	voiceCache = VoiceAvailability{}
}
