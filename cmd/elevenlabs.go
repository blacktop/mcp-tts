package cmd

type SynthesisOptions struct {
	Stability       float64 `json:"stability,omitempty"`
	SimilarityBoost float64 `json:"similarity_boost,omitempty"`
	Style           float64 `json:"style,omitempty"`
	UseSpeakerBoost bool    `json:"use_speaker_boost,omitempty"`
	// Speed           float64 `json:"speed,omitempty"`
}

type ElevenLabsParams struct {
	Text          string           `json:"text"`
	ModelID       string           `json:"model_id,omitempty"`
	LanguageCode  string           `json:"language_code,omitempty"`
	PreviousText  string           `json:"previous_text,omitempty"`
	NextText      string           `json:"next_text,omitempty"`
	VoiceSettings SynthesisOptions `json:"voice_settings,omitempty"`
}
