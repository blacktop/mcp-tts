package cmd

import (
	"encoding/binary"
	"errors"
	"fmt"
	"mime"
	"strconv"

	"google.golang.org/genai"
)

// defaultGoogleSampleRate is the rate Gemini uses for L16 audio that does not
// state one.
const defaultGoogleSampleRate = 24000

// googleSpeechPCM returns the 16-bit mono PCM samples in a Gemini TTS audio part
// and their sample rate. Gemini 3.8 models return a WAV file that ends in a C2PA
// content-credentials chunk, which must not be played as sound; older models
// return bare L16 samples.
func googleSpeechPCM(blob *genai.Blob) ([]byte, int, error) {
	mediaType, params, err := mime.ParseMediaType(blob.MIMEType)
	if err != nil {
		return nil, 0, fmt.Errorf("reading Google TTS audio type %q: %w", blob.MIMEType, err)
	}
	switch mediaType {
	case "audio/wav", "audio/x-wav":
		return pcmFromWAV(blob.Data)
	case "audio/l16":
		rate := defaultGoogleSampleRate
		if value, ok := params["rate"]; ok {
			rate, err = strconv.Atoi(value)
			if err != nil || rate <= 0 {
				return nil, 0, fmt.Errorf("invalid sample rate in Google TTS audio type %q", blob.MIMEType)
			}
		}
		return blob.Data, rate, nil
	default:
		return nil, 0, fmt.Errorf("unsupported Google TTS audio type %q", blob.MIMEType)
	}
}

// pcmFromWAV returns the samples and sample rate of a 16-bit mono PCM WAV file,
// skipping every chunk that is not audio.
func pcmFromWAV(data []byte) ([]byte, int, error) {
	if !isRIFFWave(data) {
		return nil, 0, errors.New("audio is not a RIFF/WAVE file")
	}
	rate := 0
	for rest := data[12:]; len(rest) >= 8; {
		id, body, next, err := nextWAVChunk(rest)
		if err != nil {
			return nil, 0, err
		}
		switch id {
		case "fmt ":
			if rate, err = pcmFormatRate(body); err != nil {
				return nil, 0, err
			}
		case "data":
			if rate == 0 {
				return nil, 0, errors.New("WAV data chunk comes before its fmt chunk")
			}
			return body, rate, nil
		}
		rest = next
	}
	return nil, 0, errors.New("WAV file has no data chunk")
}

func isRIFFWave(data []byte) bool {
	return len(data) >= 12 && string(data[0:4]) == "RIFF" && string(data[8:12]) == "WAVE"
}

// nextWAVChunk splits the chunk at the start of b from the chunks after it.
// Chunks are padded to an even length.
func nextWAVChunk(b []byte) (string, []byte, []byte, error) {
	id := string(b[0:4])
	size := uint64(binary.LittleEndian.Uint32(b[4:8]))
	b = b[8:]
	if size > uint64(len(b)) {
		return "", nil, nil, fmt.Errorf(
			"WAV %q chunk claims %d bytes but only %d remain", id, size, len(b),
		)
	}
	padded := min(size+size%2, uint64(len(b)))
	return id, b[:size], b[padded:], nil
}

// pcmFormatRate checks that a WAV fmt chunk describes the 16-bit mono PCM the
// players expect and returns its sample rate.
func pcmFormatRate(fmtChunk []byte) (int, error) {
	if len(fmtChunk) < 16 {
		return 0, fmt.Errorf("WAV fmt chunk is %d bytes, expected at least 16", len(fmtChunk))
	}
	format := binary.LittleEndian.Uint16(fmtChunk[0:2])
	channels := binary.LittleEndian.Uint16(fmtChunk[2:4])
	rate := binary.LittleEndian.Uint32(fmtChunk[4:8])
	bits := binary.LittleEndian.Uint16(fmtChunk[14:16])
	if format != 1 || channels != 1 || bits != 16 || rate == 0 {
		return 0, fmt.Errorf(
			"unsupported WAV audio (format %d, %d channels, %d-bit, %d Hz); expected 16-bit mono PCM",
			format, channels, bits, rate,
		)
	}
	return int(rate), nil
}
