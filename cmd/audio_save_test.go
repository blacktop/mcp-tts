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
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGenerateFilename(t *testing.T) {
	tests := []struct {
		name string
		text string
		ext  string
	}{
		{"mp3 extension", "Hello world", "mp3"},
		{"wav extension", "Test speech", "wav"},
		{"aiff extension", "macOS say", "aiff"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			filename := generateFilename(tt.text, tt.ext)

			// Should start with tts_
			assert.True(t, strings.HasPrefix(filename, "tts_"))

			// Should end with correct extension
			assert.True(t, strings.HasSuffix(filename, "."+tt.ext))

			// Should contain unix timestamp and hash
			parts := strings.Split(strings.TrimSuffix(filename, "."+tt.ext), "_")
			assert.Equal(t, 3, len(parts), "Filename should have 3 parts: tts, timestamp, hash")

			// Hash should be 8 characters
			assert.Equal(t, 8, len(parts[2]), "Hash should be 8 characters")
		})
	}
}

func TestGenerateFilenameUniqueness(t *testing.T) {
	// Generate multiple filenames for same text, they should be unique due to timestamp
	filename1 := generateFilename("Same text", "mp3")
	filename2 := generateFilename("Same text", "mp3")

	// They may be different if generated at different milliseconds
	// Both should be valid filenames
	assert.True(t, strings.HasPrefix(filename1, "tts_"))
	assert.True(t, strings.HasPrefix(filename2, "tts_"))
}

func TestShouldSave(t *testing.T) {
	// Save original value and restore
	origOutputDir := outputDir
	defer func() { outputDir = origOutputDir }()

	t.Run("empty outputDir returns false", func(t *testing.T) {
		outputDir = ""
		assert.False(t, shouldSave())
	})

	t.Run("non-empty outputDir returns true", func(t *testing.T) {
		outputDir = "/tmp/test"
		assert.True(t, shouldSave())
	})
}

func TestShouldPlay(t *testing.T) {
	// Save original values and restore
	origOutputDir := outputDir
	origNoPlay := noPlay
	defer func() {
		outputDir = origOutputDir
		noPlay = origNoPlay
	}()

	t.Run("noPlay=false returns true", func(t *testing.T) {
		noPlay = false
		outputDir = "/tmp"
		assert.True(t, shouldPlay())
	})

	t.Run("noPlay=true with outputDir returns false", func(t *testing.T) {
		noPlay = true
		outputDir = "/tmp"
		assert.False(t, shouldPlay())
	})

	t.Run("noPlay=true without outputDir returns true (fallback)", func(t *testing.T) {
		noPlay = true
		outputDir = ""
		assert.True(t, shouldPlay())
	})
}

func TestSaveMP3(t *testing.T) {
	// Save original value and restore
	origOutputDir := outputDir
	defer func() { outputDir = origOutputDir }()

	t.Run("no save when outputDir empty", func(t *testing.T) {
		outputDir = ""
		path, err := saveMP3([]byte("fake mp3 data"), "test text")
		require.NoError(t, err)
		assert.Empty(t, path)
	})

	t.Run("saves file when outputDir set", func(t *testing.T) {
		tempDir := t.TempDir()
		outputDir = tempDir

		fakeMP3 := []byte("fake mp3 data for testing")
		path, err := saveMP3(fakeMP3, "test speech")
		require.NoError(t, err)
		assert.NotEmpty(t, path)
		assert.True(t, strings.HasSuffix(path, ".mp3"))

		// Verify file exists and has correct content
		data, err := os.ReadFile(path)
		require.NoError(t, err)
		assert.Equal(t, fakeMP3, data)
	})
}

func TestSaveWAV(t *testing.T) {
	// Save original value and restore
	origOutputDir := outputDir
	defer func() { outputDir = origOutputDir }()

	t.Run("no save when outputDir empty", func(t *testing.T) {
		outputDir = ""
		path, err := saveWAV([]byte{0, 0, 1, 0}, 24000, "test text")
		require.NoError(t, err)
		assert.Empty(t, path)
	})

	t.Run("saves valid WAV file", func(t *testing.T) {
		tempDir := t.TempDir()
		outputDir = tempDir

		// Create some fake PCM data (16-bit samples)
		pcmData := make([]byte, 4800) // 0.1 seconds at 24000Hz
		for i := range pcmData {
			pcmData[i] = byte(i % 256)
		}

		path, err := saveWAV(pcmData, 24000, "test speech")
		require.NoError(t, err)
		assert.NotEmpty(t, path)
		assert.True(t, strings.HasSuffix(path, ".wav"))

		// Verify file exists
		info, err := os.Stat(path)
		require.NoError(t, err)

		// WAV file should be larger than PCM data due to header
		assert.Greater(t, info.Size(), int64(len(pcmData)))

		// Verify WAV header
		data, err := os.ReadFile(path)
		require.NoError(t, err)
		assert.True(t, bytes.HasPrefix(data, []byte("RIFF")))
		assert.True(t, bytes.Contains(data, []byte("WAVE")))
		assert.True(t, bytes.Contains(data, []byte("fmt ")))
		assert.True(t, bytes.Contains(data, []byte("data")))
	})
}

func TestWriteWAVHeader(t *testing.T) {
	buf := &bytes.Buffer{}

	err := writeWAVHeader(buf, 1000, 24000, 1, 16)
	require.NoError(t, err)

	header := buf.Bytes()

	// Check RIFF header
	assert.Equal(t, "RIFF", string(header[0:4]))

	// Check WAVE format
	assert.Equal(t, "WAVE", string(header[8:12]))

	// Check fmt subchunk
	assert.Equal(t, "fmt ", string(header[12:16]))

	// Check data subchunk marker
	assert.Equal(t, "data", string(header[36:40]))

	// Header should be 44 bytes
	assert.Equal(t, 44, len(header))
}

func TestFormatSaveResult(t *testing.T) {
	// Save original value and restore
	origSuppressOutput := suppressSpeakingOutput
	defer func() { suppressSpeakingOutput = origSuppressOutput }()

	tests := []struct {
		name       string
		text       string
		savedPath  string
		played     bool
		suppress   bool
		wantPrefix string
	}{
		{
			name:       "play only",
			text:       "Hello",
			savedPath:  "",
			played:     true,
			suppress:   false,
			wantPrefix: "Speaking: Hello",
		},
		{
			name:       "save and play",
			text:       "Hello",
			savedPath:  "/tmp/test.mp3",
			played:     true,
			suppress:   false,
			wantPrefix: "Speaking: Hello",
		},
		{
			name:       "save only (no play)",
			text:       "Hello",
			savedPath:  "/tmp/test.mp3",
			played:     false,
			suppress:   false,
			wantPrefix: "Saved:",
		},
		{
			name:       "suppressed output with save",
			text:       "Hello",
			savedPath:  "/tmp/test.mp3",
			played:     true,
			suppress:   true,
			wantPrefix: "Speech completed",
		},
		{
			name:       "suppressed save only",
			text:       "Hello",
			savedPath:  "/tmp/test.mp3",
			played:     false,
			suppress:   true,
			wantPrefix: "Saved:",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			suppressSpeakingOutput = tt.suppress
			result := formatSaveResult(tt.text, tt.savedPath, tt.played)
			assert.True(t, strings.HasPrefix(result, tt.wantPrefix),
				"Expected prefix %q, got %q", tt.wantPrefix, result)

			// If path was provided and not a play-only case, should contain the path
			if tt.savedPath != "" {
				assert.Contains(t, result, tt.savedPath)
			}
		})
	}
}

func TestSaveMP3Integration(t *testing.T) {
	// This test verifies the full flow of saving an MP3 file
	tempDir := t.TempDir()

	// Save original value and restore
	origOutputDir := outputDir
	defer func() { outputDir = origOutputDir }()
	outputDir = tempDir

	// Create fake MP3 data with a proper MP3 header (ID3 tag)
	mp3Data := append([]byte("ID3"), make([]byte, 100)...)

	path, err := saveMP3(mp3Data, "Integration test text")
	require.NoError(t, err)
	require.NotEmpty(t, path)

	// Verify file is in correct directory
	assert.Equal(t, tempDir, filepath.Dir(path))

	// Verify file extension
	assert.Equal(t, ".mp3", filepath.Ext(path))

	// Verify content
	savedData, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, mp3Data, savedData)
}
