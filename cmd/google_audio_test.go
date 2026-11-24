package cmd

import (
	"encoding/binary"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/genai"
)

func wavChunk(id string, body []byte) []byte {
	out := binary.LittleEndian.AppendUint32([]byte(id), uint32(len(body)))
	out = append(out, body...)
	if len(body)%2 == 1 {
		out = append(out, 0)
	}
	return out
}

func wavFmt(format, channels uint16, rate uint32, bits uint16) []byte {
	body := binary.LittleEndian.AppendUint16(nil, format)
	body = binary.LittleEndian.AppendUint16(body, channels)
	body = binary.LittleEndian.AppendUint32(body, rate)
	body = binary.LittleEndian.AppendUint32(body, rate*uint32(channels)*uint32(bits)/8)
	body = binary.LittleEndian.AppendUint16(body, channels*bits/8)
	body = binary.LittleEndian.AppendUint16(body, bits)
	return wavChunk("fmt ", body)
}

func riffWave(chunks ...[]byte) []byte {
	var body []byte
	for _, c := range chunks {
		body = append(body, c...)
	}
	out := binary.LittleEndian.AppendUint32([]byte("RIFF"), uint32(4+len(body)))
	return append(append(out, "WAVE"...), body...)
}

func TestGoogleSpeechPCMDropsNonAudioChunks(t *testing.T) {
	samples := []byte{1, 0, 2, 0, 3, 0}
	wav := riffWave(
		wavFmt(1, 1, 24000, 16),
		wavChunk("LIST", []byte{9, 9, 9}),
		wavChunk("data", samples),
		wavChunk("C2PA", []byte("signed content credentials")),
	)

	pcm, rate, err := googleSpeechPCM(&genai.Blob{MIMEType: "audio/wav", Data: wav})

	require.NoError(t, err)
	assert.Equal(t, samples, pcm)
	assert.Equal(t, 24000, rate)
}

func TestGoogleSpeechPCMPassesL16Through(t *testing.T) {
	samples := []byte{1, 0, 2, 0}
	tests := []struct {
		mimeType string
		rate     int
	}{
		{"audio/L16;codec=pcm;rate=16000", 16000},
		{"audio/L16;codec=pcm", defaultGoogleSampleRate},
	}
	for _, tt := range tests {
		t.Run(tt.mimeType, func(t *testing.T) {
			pcm, rate, err := googleSpeechPCM(&genai.Blob{MIMEType: tt.mimeType, Data: samples})

			require.NoError(t, err)
			assert.Equal(t, samples, pcm)
			assert.Equal(t, tt.rate, rate)
		})
	}
}

func TestGoogleSpeechPCMRejectsUnplayableAudio(t *testing.T) {
	data := wavChunk("data", []byte{1, 0})
	tests := []struct {
		name     string
		mimeType string
		audio    []byte
		wantErr  string
	}{
		{"missing type", "", nil, "reading Google TTS audio type"},
		{"unknown type", "audio/mpeg", nil, `unsupported Google TTS audio type "audio/mpeg"`},
		{"bad L16 rate", "audio/L16;rate=fast", nil, "invalid sample rate"},
		{"zero L16 rate", "audio/L16;rate=0", nil, "invalid sample rate"},
		{"not a WAV", "audio/wav", []byte("ID3 not riff"), "not a RIFF/WAVE file"},
		{"no data chunk", "audio/wav", riffWave(wavFmt(1, 1, 24000, 16)), "no data chunk"},
		{"data before fmt", "audio/wav", riffWave(data, wavFmt(1, 1, 24000, 16)), "before its fmt"},
		{"stereo", "audio/wav", riffWave(wavFmt(1, 2, 24000, 16), data), "2 channels"},
		{"8-bit", "audio/wav", riffWave(wavFmt(1, 1, 24000, 8), data), "8-bit"},
		{"float samples", "audio/wav", riffWave(wavFmt(3, 1, 24000, 32), data), "format 3"},
		{"short fmt", "audio/wav", riffWave(wavChunk("fmt ", []byte{1, 0}), data), "fmt chunk is 2"},
		{
			"truncated chunk", "audio/wav",
			riffWave(wavFmt(1, 1, 24000, 16), []byte("data\xff\xff\xff\xff\x01\x00")),
			`"data" chunk claims 4294967295 bytes but only 2 remain`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pcm, _, err := googleSpeechPCM(&genai.Blob{MIMEType: tt.mimeType, Data: tt.audio})

			require.ErrorContains(t, err, tt.wantErr)
			assert.Nil(t, pcm)
		})
	}
}
