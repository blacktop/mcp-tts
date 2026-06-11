package cmd

// SixtyDBParams is the JSON request body for the 60dB text-to-speech endpoint
// (POST https://api.60db.ai/tts-synthesize). Mirrors the structure used for
// ElevenLabs (see elevenlabs.go), adapted to 60dB's flat field layout.
//
// Note: 60dB scales stability/similarity on 0-100 (unlike ElevenLabs' 0.0-1.0)
// and selects the model via the voice itself ("60db Fast" / "60db Quality"),
// so there is no separate model_id field.
type SixtyDBParams struct {
	Text         string  `json:"text"`
	VoiceID      string  `json:"voice_id,omitempty"`
	Speed        float64 `json:"speed"`
	Stability    int     `json:"stability"`
	Similarity   int     `json:"similarity"`
	Enhance      bool    `json:"enhance"`
	OutputFormat string  `json:"output_format,omitempty"`
}

// SixtyDBResponse is the JSON response body returned by the 60dB
// tts-synthesize endpoint. The synthesized audio is base64-encoded in
// AudioBase64 (unlike ElevenLabs, which streams raw audio bytes).
type SixtyDBResponse struct {
	Success         bool    `json:"success"`
	Message         string  `json:"message"`
	AudioBase64     string  `json:"audio_base64"`
	SampleRate      int     `json:"sample_rate"`
	DurationSeconds float64 `json:"duration_seconds"`
	Encoding        string  `json:"encoding"`
	OutputFormat    string  `json:"output_format"`
}
