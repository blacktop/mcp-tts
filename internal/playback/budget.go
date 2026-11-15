package playback

import "time"

const (
	// HardCap is the longest any single playback may run.
	HardCap = 15 * time.Minute

	knownSlack = 15 * time.Second

	// Speech of unknown length gets a budget from its text. The rate is the
	// slowest delivery worth planning for, so real speech finishes well inside.
	slowestCharsPerSecond = 3
	unknownSlack          = time.Minute
	unknownFloor          = 2 * time.Minute

	// voiceStartupAllowance covers local model loading before the first audio.
	voiceStartupAllowance = 5 * time.Minute
)

// BudgetForDuration returns the time allowed for audio whose length is known.
// Callers must not pass an unknown (zero) duration; use BudgetForText.
func BudgetForDuration(audio time.Duration) time.Duration {
	return min(HardCap, audio+audio/2+knownSlack)
}

// BudgetForText returns the time allowed for speech whose audio length is not
// known, estimated from the number of characters spoken.
func BudgetForText(chars int) time.Duration {
	estimate := time.Duration(chars/slowestCharsPerSecond)*time.Second + unknownSlack
	return min(HardCap, max(unknownFloor, estimate))
}

// BudgetForVoice returns the time allowed for a local-model voice, which loads
// its model before speaking.
func BudgetForVoice(chars int) time.Duration {
	return min(HardCap, BudgetForText(chars)+voiceStartupAllowance)
}
