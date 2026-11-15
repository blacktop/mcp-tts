package playback

import (
	"fmt"
	"os"
	"path/filepath"
)

// DefaultIncidentDir returns the private directory that holds diagnostics for
// players that overran their budget.
func DefaultIncidentDir() (string, error) {
	base, err := os.UserCacheDir()
	if err != nil {
		return "", fmt.Errorf("locating user cache directory: %w", err)
	}
	return filepath.Join(base, "mcp-tts", "incidents"), nil
}
