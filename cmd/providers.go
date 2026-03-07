/*
Copyright © 2025 blacktop

Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction, including without limitation the rights
to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
copies of the Software, and to permit persons to whom the Software is
furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in
all copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN
THE SOFTWARE.
*/
package cmd

import (
	"os"
	"runtime"
)

// Provider IDs used in tool registration and elicitation routing.
const (
	ProviderSay        = "say_tts"
	ProviderElevenLabs = "elevenlabs_tts"
	ProviderGoogle     = "google_tts"
	ProviderOpenAI     = "openai_tts"
)

// Default values for provider-specific settings.
const (
	DefaultSayRate     = 200
	DefaultGoogleVoice = "Kore"
	DefaultGoogleModel = "gemini-2.5-flash-preview-tts"
	DefaultOpenAIVoice = "alloy"
	DefaultOpenAIModel = "gpt-4o-mini-tts-2025-12-15"
	DefaultOpenAISpeed = 1.0
)

// Voice and model lists shared by tool schemas (schemas.go) and
// elicitation forms (elicitation.go). Update these when providers
// add or remove options.
var (
	GoogleVoices = []string{
		"Achernar", "Achird", "Algenib", "Algieba", "Alnilam",
		"Aoede", "Autonoe", "Callirrhoe", "Charon", "Despina",
		"Enceladus", "Erinome", "Fenrir", "Gacrux", "Iapetus",
		"Kore", "Laomedeia", "Leda", "Orus", "Puck",
		"Pulcherrima", "Rasalgethi", "Sadachbia", "Sadaltager", "Schedar",
		"Sulafat", "Umbriel", "Vindemiatrix", "Zephyr", "Zubenelgenubi",
	}
	GoogleModels = []string{
		"gemini-2.5-flash-preview-tts",
		"gemini-2.5-pro-preview-tts",
		"gemini-2.5-flash-lite-preview-tts",
	}
	OpenAIVoices = []string{
		"alloy", "ash", "ballad", "coral", "echo",
		"fable", "nova", "onyx", "sage", "shimmer", "verse",
	}
	OpenAIModels = []string{
		"gpt-4o-mini-tts-2025-12-15", "tts-1", "tts-1-hd",
	}
)

type providerOption struct {
	ID   string
	Name string
}

func availableProviders() []providerOption {
	var providers []providerOption
	if runtime.GOOS == "darwin" {
		providers = append(providers, providerOption{ProviderSay, "macOS Say"})
	}
	if os.Getenv("ELEVENLABS_API_KEY") != "" {
		providers = append(providers, providerOption{ProviderElevenLabs, "ElevenLabs"})
	}
	if os.Getenv("GOOGLE_AI_API_KEY") != "" || os.Getenv("GEMINI_API_KEY") != "" {
		providers = append(providers, providerOption{ProviderGoogle, "Google Gemini"})
	}
	if os.Getenv("OPENAI_API_KEY") != "" {
		providers = append(providers, providerOption{ProviderOpenAI, "OpenAI"})
	}
	return providers
}
