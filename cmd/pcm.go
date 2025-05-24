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
	"github.com/gopxl/beep/v2"
)

// PCMStream implements beep.StreamSeeker for playing raw PCM audio data
type PCMStream struct {
	data       []byte
	sampleRate beep.SampleRate
	position   int
}

func (s *PCMStream) Stream(samples [][2]float64) (n int, ok bool) {
	if s.position >= len(s.data) {
		return 0, false
	}

	for i := range samples {
		if s.position+1 >= len(s.data) {
			return i, true
		}

		// Convert 16-bit little-endian PCM to float64
		sample16 := int16(s.data[s.position]) | int16(s.data[s.position+1])<<8
		sampleFloat := float64(sample16) / 32768.0

		// Mono to stereo
		samples[i][0] = sampleFloat
		samples[i][1] = sampleFloat

		s.position += 2
	}

	return len(samples), true
}

func (s *PCMStream) Err() error {
	return nil
}

func (s *PCMStream) Len() int {
	return len(s.data) / 2 // 16-bit samples
}

func (s *PCMStream) Position() int {
	return s.position / 2
}

func (s *PCMStream) Seek(p int) error {
	s.position = p * 2
	if s.position < 0 {
		s.position = 0
	}
	if s.position >= len(s.data) {
		s.position = len(s.data)
	}
	return nil
}
