# Google TTS Tests Documentation

This directory contains comprehensive unit tests for the Google TTS (Text-to-Speech) integration in the mcp-tts TTS service.

## Test Files

### `root_test.go`
Contains all test functions for the Google TTS functionality:

#### Core Tests

**`TestGoogleTTSTool`** - Main test suite for the Google TTS tool functionality:
- ✅ Successful TTS requests with default model
- ✅ Successful TTS requests with custom voice and model  
- ✅ Missing API key validation
- ✅ Empty text validation
- ✅ Invalid text type validation
- ✅ Default parameter handling

**`TestGoogleTTSParameterValidation`** - Validates all supported parameters:
- Voice options: 30 Google TTS voices (Zephyr, Puck, Charon, Kore, Fenrir, Aoede, Leda, Orus, Autonoe, Enceladus, etc.)
- Model options: gemini-2.5-flash-preview-tts, gemini-2.5-pro-preview-tts
- Default value handling

#### Audio Tests

**`TestGoogleTTSAudioPlayback`** - Basic PCM audio playback simulation:
- Mock audio player functionality
- 24kHz PCM audio data generation (Google TTS sample rate)
- Playback verification

**`TestGoogleTTSAudioIntegration`** - Comprehensive audio integration tests:
- 🎵 Basic PCM audio generation and playback at 24kHz (A note - 440Hz)
- 🎭 Multiple Google TTS voice configurations (10 different voices with unique frequencies)
- 🎛️ Google TTS specific audio formats (24kHz PCM in various durations)
- 🎼 PCM Stream functionality testing

#### Benchmarks

**`BenchmarkGoogleTTSTool`** - Performance benchmarking for tool processing
- Measures parameter validation speed
- Average: ~53ns per operation

**`BenchmarkPCMAudioGeneration`** - Performance benchmarking for PCM audio generation
- Measures 1-second audio generation speed at 24kHz
- Average: ~67μs per operation (generating 48,000 bytes)

## Audio Generation

The tests include a sophisticated audio generation system:

### `generateTestAudio(sampleRate, duration, frequency)`
- Generates PCM audio data (16-bit samples)
- Creates sine wave audio at specified frequency
- Supports various sample rates (8kHz to 48kHz)
- Used for testing different voice characteristics

### `MockAudioPlayer`
- Simulates real audio playback
- Tracks played audio data
- Configurable playback duration
- Validates audio integrity

## Running Tests

```bash
# Run all tests
go test ./cmd -v

# Run specific test suites
go test ./cmd -v -run TestGoogleTTSTool
go test ./cmd -v -run TestGoogleTTSAudioIntegration

# Run benchmarks
go test ./cmd -bench=. -v

# Run audio-specific tests
go test ./cmd -v -run "Audio"
```

## Test Coverage

The tests cover:
- ✅ Parameter validation and sanitization
- ✅ API key management
- ✅ Error handling
- ✅ Audio generation and playback
- ✅ Multiple voice support
- ✅ Different audio formats
- ✅ Performance benchmarking

## Mock Components

### MockAudioPlayer
Simulates audio playback for testing without requiring actual audio hardware:
- Captures audio data that would be played
- Simulates realistic playback timing
- Provides verification methods

### Test Audio Generation
Creates realistic test audio data:
- Various frequencies (200Hz - 1000Hz)
- Different sample rates (8kHz - 48kHz) 
- Configurable durations (0.2s - 1.0s)
- 16-bit PCM format

## Integration with Google TTS API

While the tests use mocked components, they validate the complete Google TTS tool workflow:

1. **Parameter Processing** - Text, voice, model validation
2. **API Configuration** - Google AI client setup, TTS models, speech config
3. **Audio Handling** - 24kHz PCM data processing, playback simulation
4. **Error Management** - Comprehensive error scenarios and validation

## Example Test Output

```
🧪 Running Google TTS Audio Integration Test...
🎵 Testing basic PCM audio playback at 24kHz...
📊 Generated 48000 bytes of PCM audio data
✅ PCM audio playback completed in 501ms
🎭 Testing multiple Google TTS voice configurations...
   ✅ Google TTS Voice Zephyr tested successfully (300Hz)
   ✅ Google TTS Voice Puck tested successfully (340Hz)
   ✅ Google TTS Voice Charon tested successfully (380Hz)
   ✅ Google TTS Voice Kore tested successfully (420Hz)
   ✅ Google TTS Voice Fenrir tested successfully (460Hz)
   ✅ Google TTS Voice Aoede tested successfully (500Hz)
   ✅ Google TTS Voice Leda tested successfully (540Hz)
   ✅ Google TTS Voice Orus tested successfully (580Hz)
   ✅ Google TTS Voice Autonoe tested successfully (620Hz)
   ✅ Google TTS Voice Enceladus tested successfully (660Hz)
🎛️ Testing Google TTS specific audio formats...
   ✅ google_tts_standard: 12000 samples, 24000 bytes (24kHz PCM)
   ✅ google_tts_short: 4800 samples, 9600 bytes (24kHz PCM)
   ✅ google_tts_long: 24000 samples, 48000 bytes (24kHz PCM)
🎼 Testing PCM Stream functionality...
   ✅ PCM Stream functionality validated
🏆 Google TTS Audio Integration Test completed successfully!
```

This comprehensive test suite ensures the Google TTS integration is robust, performant, and handles all supported voice configurations and 24kHz PCM audio formats correctly. 