package cmd

type SynthesisOptions struct {
	Stability       float64 `json:"stability"`
	SimilarityBoost float64 `json:"similarity_boost"`
	Style           float64 `json:"style"`
	UseSpeakerBoost bool    `json:"use_speaker_boost"`
}

type ElevenLabsParams struct {
	VoiceID       string           `json:"voice_id"`
	ModelID       string           `json:"model_id,omitempty"`
	Text          string           `json:"text"`
	PreviousText  string           `json:"previous_text,omitempty"`
	NextText      string           `json:"next_text,omitempty"`
	VoiceSettings SynthesisOptions `json:"voice_settings,omitempty"`
	Stream        bool             `json:"stream,omitempty"`
}
