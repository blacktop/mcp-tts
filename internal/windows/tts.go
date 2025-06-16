package windows

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"

	"github.com/blacktop/mcp-tts/internal/powershell"
)

// TTSConfig represents configuration for Windows TTS
type TTSConfig struct {
	API   string  // "sapi" or "winrt" (required)
	Text  string  // Text to speak
	Voice string  // Voice name
	Rate  *int    // Speech rate (-10 to 10)
}

// Voice represents a TTS voice
type Voice struct {
	Name     string `json:"Name"`
	Language string `json:"Language"`
	Gender   string `json:"Gender"`
	API      string `json:"API"`
}

// TTSEngine handles Windows TTS operations
type TTSEngine struct{}

// NewTTSEngine creates a new TTS engine
func NewTTSEngine() *TTSEngine {
	return &TTSEngine{}
}

// GetVoices returns all available voices from both SAPI and WinRT APIs
func (e *TTSEngine) GetVoices(ctx context.Context) ([]Voice, error) {
	cmd := exec.CommandContext(ctx, "powershell.exe", "-Command", powershell.VoiceEnumerationScript)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("failed to enumerate voices: %v", err)
	}

	var voices []Voice
	if err := json.Unmarshal(output, &voices); err != nil {
		return nil, fmt.Errorf("failed to parse voice data: %v", err)
	}

	return voices, nil
}

// DetermineAPI determines which API to use based on explicit config
func (e *TTSEngine) DetermineAPI(ctx context.Context, config TTSConfig) (string, error) {
	// API must be explicitly specified
	if config.API == "sapi" || config.API == "winrt" {
		return config.API, nil
	}

	// No auto-detection - user must choose
	return "", fmt.Errorf("API must be explicitly specified: use 'sapi' or 'winrt'")
}

// Speak synthesizes and plays text using the configured API
func (e *TTSEngine) Speak(ctx context.Context, config TTSConfig) error {
	// DEBUG: Log incoming text
	fmt.Printf("DEBUG TTSEngine.Speak: text='%s', isSSML=%v\n", config.Text, isSSML(config.Text))
	
	// Determine which API to use
	apiToUse, err := e.DetermineAPI(ctx, config)
	if err != nil {
		return err
	}


	// Build PowerShell command
	var script string
	switch apiToUse {
	case "sapi":
		script = powershell.SAPIScript
	case "winrt":
		script = powershell.WinRTScript
	default:
		return fmt.Errorf("unsupported API: %s", apiToUse)
	}

	// Build PowerShell command with parameters  
	// Use here-string for SSML to avoid quote escaping issues
	var psCommand string
	if isSSML(config.Text) {
		psCommand = fmt.Sprintf("$text = @'\n%s\n'@", config.Text)
	} else {
		psCommand = fmt.Sprintf("$text = '%s'", escapeForPowerShell(config.Text))
	}
	
	// Check if text is SSML and set flag
	if isSSML(config.Text) {
		psCommand += "; $isSSML = $true"
	} else {
		psCommand += "; $isSSML = $false"
	}
	
	if config.Voice != "" {
		psCommand += fmt.Sprintf("; $voice = '%s'", escapeForPowerShell(config.Voice))
	}
	if config.Rate != nil {
		psCommand += fmt.Sprintf("; $rate = %d", *config.Rate)
	}
	psCommand += "; " + script

	// Execute PowerShell command
	cmd := exec.CommandContext(ctx, "powershell.exe", "-Command", psCommand)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("TTS execution failed (%s): %v - %s", apiToUse, err, string(output))
	}
	

	return nil
}

// isSSML checks if the text contains SSML markup
func isSSML(text string) bool {
	return strings.Contains(text, "<speak") && strings.Contains(text, "</speak>")
}

// escapeForPowerShell escapes single quotes for PowerShell string literals
func escapeForPowerShell(s string) string {
	return strings.ReplaceAll(s, "'", "''")
}

// FormatVoiceList formats a list of voices for display
func FormatVoiceList(voices []Voice) string {
	if len(voices) == 0 {
		return "No voices found"
	}

	var result strings.Builder
	result.WriteString("Available Windows TTS voices:\n\n")

	// Group by API
	sapiVoices := make([]Voice, 0)
	winrtVoices := make([]Voice, 0)

	for _, voice := range voices {
		if voice.API == "SAPI" {
			sapiVoices = append(sapiVoices, voice)
		} else if voice.API == "WinRT" {
			winrtVoices = append(winrtVoices, voice)
		}
	}

	// Display SAPI voices
	if len(sapiVoices) > 0 {
		result.WriteString("SAPI Voices (Legacy):\n")
		for i, voice := range sapiVoices {
			result.WriteString(fmt.Sprintf("  %d. %s (%s) - %s\n", i+1, voice.Name, voice.Language, voice.Gender))
		}
		result.WriteString("\n")
	}

	// Display WinRT voices
	if len(winrtVoices) > 0 {
		result.WriteString("WinRT Voices (Modern):\n")
		for i, voice := range winrtVoices {
			result.WriteString(fmt.Sprintf("  %d. %s (%s) - %s", i+1, voice.Name, voice.Language, voice.Gender))
			// Highlight special voices
			if strings.Contains(voice.Language, "en-CA") {
				result.WriteString(" ⭐ Canadian")
			}
			result.WriteString("\n")
		}
	}

	return result.String()
}