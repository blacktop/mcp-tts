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
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"
)

// generateFilename creates a unique filename for saved audio.
// Format: tts_{unix_millis}_{8char_hash}.{ext}
func generateFilename(text, ext string) string {
	millis := time.Now().UnixMilli()
	hash := sha256.Sum256([]byte(fmt.Sprintf("%d_%s", millis, text)))
	hashStr := fmt.Sprintf("%x", hash[:4]) // 8 hex chars
	return fmt.Sprintf("tts_%d_%s.%s", millis, hashStr, ext)
}

// shouldSave returns true if audio should be saved to disk.
func shouldSave() bool {
	return outputDir != ""
}

// shouldPlay returns true if audio should be played.
// Always plays if no output dir is set (default behavior).
func shouldPlay() bool {
	return !noPlay || outputDir == ""
}

// saveMP3 saves MP3 audio data to the output directory.
// Returns the full path to the saved file.
func saveMP3(data []byte, text string) (string, error) {
	if !shouldSave() {
		return "", nil
	}
	filename := generateFilename(text, "mp3")
	fpath := filepath.Join(outputDir, filename)
	if err := os.WriteFile(fpath, data, 0644); err != nil {
		return "", fmt.Errorf("failed to save MP3 file: %w", err)
	}
	return fpath, nil
}

// saveWAV saves PCM audio data as a WAV file to the output directory.
// The PCM data is expected to be 16-bit mono at the specified sample rate.
// Returns the full path to the saved file.
func saveWAV(pcmData []byte, sampleRate int, text string) (string, error) {
	if !shouldSave() {
		return "", nil
	}
	filename := generateFilename(text, "wav")
	fpath := filepath.Join(outputDir, filename)

	f, err := os.Create(fpath)
	if err != nil {
		return "", fmt.Errorf("failed to create WAV file: %w", err)
	}
	defer f.Close()

	// Write WAV header and data
	const channels = 1
	const bitsPerSample = 16
	if err := writeWAVHeader(f, len(pcmData), sampleRate, channels, bitsPerSample); err != nil {
		return "", fmt.Errorf("failed to write WAV header: %w", err)
	}
	if _, err := f.Write(pcmData); err != nil {
		return "", fmt.Errorf("failed to write WAV data: %w", err)
	}

	return fpath, nil
}

// writeWAVHeader writes a standard WAV file header.
func writeWAVHeader(w io.Writer, dataSize, sampleRate, channels, bitsPerSample int) error {
	byteRate := sampleRate * channels * bitsPerSample / 8
	blockAlign := channels * bitsPerSample / 8
	chunkSize := 36 + dataSize

	// RIFF header
	if _, err := w.Write([]byte("RIFF")); err != nil {
		return err
	}
	if err := binary.Write(w, binary.LittleEndian, uint32(chunkSize)); err != nil {
		return err
	}
	if _, err := w.Write([]byte("WAVE")); err != nil {
		return err
	}

	// fmt subchunk
	if _, err := w.Write([]byte("fmt ")); err != nil {
		return err
	}
	if err := binary.Write(w, binary.LittleEndian, uint32(16)); err != nil { // Subchunk1Size for PCM
		return err
	}
	if err := binary.Write(w, binary.LittleEndian, uint16(1)); err != nil { // AudioFormat: PCM
		return err
	}
	if err := binary.Write(w, binary.LittleEndian, uint16(channels)); err != nil {
		return err
	}
	if err := binary.Write(w, binary.LittleEndian, uint32(sampleRate)); err != nil {
		return err
	}
	if err := binary.Write(w, binary.LittleEndian, uint32(byteRate)); err != nil {
		return err
	}
	if err := binary.Write(w, binary.LittleEndian, uint16(blockAlign)); err != nil {
		return err
	}
	if err := binary.Write(w, binary.LittleEndian, uint16(bitsPerSample)); err != nil {
		return err
	}

	// data subchunk
	if _, err := w.Write([]byte("data")); err != nil {
		return err
	}
	if err := binary.Write(w, binary.LittleEndian, uint32(dataSize)); err != nil {
		return err
	}

	return nil
}

// formatSaveResult formats the result message based on save and play modes.
func formatSaveResult(text, savedPath string, played bool) string {
	if suppressSpeakingOutput {
		if savedPath != "" && !played {
			return fmt.Sprintf("Saved: %s", savedPath)
		} else if savedPath != "" {
			return fmt.Sprintf("Speech completed\nSaved: %s", savedPath)
		}
		return "Speech completed"
	}

	if savedPath != "" && !played {
		return fmt.Sprintf("Saved: %s", savedPath)
	} else if savedPath != "" {
		return fmt.Sprintf("Speaking: %s\nSaved: %s", text, savedPath)
	}
	return fmt.Sprintf("Speaking: %s", text)
}
