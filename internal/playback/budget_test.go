package playback

import (
	"testing"
	"time"
)

func TestBudgetForDuration(t *testing.T) {
	tests := []struct {
		name  string
		audio time.Duration
		want  time.Duration
	}{
		{"short clip keeps the fixed slack", 2 * time.Second, 18 * time.Second},
		{"long clip scales", 6 * time.Minute, 9*time.Minute + 15*time.Second},
		{"very long clip is capped", time.Hour, HardCap},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := BudgetForDuration(tt.audio); got != tt.want {
				t.Fatalf("BudgetForDuration(%s) = %s, want %s", tt.audio, got, tt.want)
			}
		})
	}
}

func TestBudgetForTextNeverUndercutsRealSpeech(t *testing.T) {
	if got := BudgetForText(0); got != unknownFloor {
		t.Fatalf("BudgetForText(0) = %s, want the floor %s", got, unknownFloor)
	}
	// 3000 characters at a slow 3 chars/s is about 17 minutes of speech.
	if got := BudgetForText(3000); got != HardCap {
		t.Fatalf("BudgetForText(3000) = %s, want the hard cap", got)
	}
	// 900 characters: 300s of speech plus the slack.
	if got, want := BudgetForText(900), 6*time.Minute; got != want {
		t.Fatalf("BudgetForText(900) = %s, want %s", got, want)
	}
}

func TestBudgetForVoiceAllowsModelStartup(t *testing.T) {
	if got, want := BudgetForVoice(0), unknownFloor+voiceStartupAllowance; got != want {
		t.Fatalf("BudgetForVoice(0) = %s, want %s", got, want)
	}
	if got := BudgetForVoice(1 << 20); got != HardCap {
		t.Fatalf("BudgetForVoice(huge) = %s, want the hard cap", got)
	}
}
