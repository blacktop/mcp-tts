<p align="center">
  <a href="https://github.com/blacktop/mcp-tts"><img alt="mcp-tts Logo" src="https://raw.githubusercontent.com/blacktop/mcp-tts/main/docs/logo.webp" height="200" /></a>
  <h1 align="center">mcp-tts</h1>
  <h4><p align="center">MCP Server for TTS (Text-to-Speech)</p></h4>
  <p align="center">
    <a href="https://github.com/blacktop/mcp-tts/actions" alt="Actions">
          <img src="https://github.com/blacktop/mcp-tts/actions/workflows/go.yml/badge.svg" /></a>
    <a href="https://github.com/blacktop/mcp-tts/releases/latest" alt="Downloads">
          <img src="https://img.shields.io/github/downloads/blacktop/mcp-tts/total.svg" /></a>
    <a href="https://github.com/blacktop/mcp-tts/releases" alt="GitHub Release">
          <img src="https://img.shields.io/github/release/blacktop/mcp-tts.svg" /></a>
    <a href="http://doge.mit-license.org" alt="LICENSE">
          <img src="https://img.shields.io/:license-mit-blue.svg" /></a>
</p>
<br>

## What? 🤔

Adds Text-to-Speech to things like Claude Desktop and Cursor IDE.  

It registers six TTS tools: 
 - `say_tts` (macOS only)
 - `windows_speech_tts` (Windows/WSL)
 - `windows_speech_voices` (Windows/WSL)
 - `elevenlabs_tts`
 - `google_tts`
 - `openai_tts`

### `say_tts`

Uses the macOS `say` binary to speak the text with built-in system voices

### `windows_speech_tts` & `windows_speech_voices`

**Cross-platform Windows Speech API TTS for offline speech synthesis**

These tools provide Windows text-to-speech functionality that works seamlessly on both native Windows and WSL (Windows Subsystem for Linux) environments. By using the same PowerShell-based approach for both platforms, we ensure consistent behavior regardless of where the application runs.

**Key Features:**
- **Unified Implementation**: Single codebase works on both Windows and WSL
- **Offline Operation**: No API keys or internet connection required
- **Voice Selection**: Choose from all installed Windows TTS voices via `windows_speech_voices`
- **Voice Control**: Specify exact voice in `windows_speech_tts` (e.g., "Microsoft Zira Desktop")
- **Rate Control**: Speech rate adjustment from -10 (slowest) to 10 (fastest)
- **Security**: Proper text escaping prevents command injection attacks

**Why This Approach Works for Both Platforms:**
- **Windows**: Direct access to `powershell.exe` and Windows Speech API
- **WSL**: Windows interop allows executing `powershell.exe` from Linux, bridging the gap where WSL cannot access Linux audio devices (ALSA/PulseAudio)

**Technical Implementation**: Uses `powershell.exe -Command` to execute `System.Speech.Synthesis.SpeechSynthesizer` with proper text and voice name escaping, plus cancellation support.

### `elevenlabs_tts`

Uses the [ElevenLabs](https://elevenlabs.io/app/speech-synthesis/text-to-speech) text-to-speech API to speak the text with premium AI voices

### `google_tts`

Uses Google's [Gemini TTS models](https://ai.google.dev/gemini-api/docs/speech-generation) to speak the text with 30 high-quality voices. Available voices include:

- **Zephyr** (Bright), **Puck** (Upbeat), **Charon** (Informative)  
- **Kore** (Firm), **Fenrir** (Excitable), **Leda** (Youthful)
- **Orus** (Firm), **Aoede** (Breezy), **Callirhoe** (Easy-going)
- **Autonoe** (Bright), **Enceladus** (Breathy), **Iapetus** (Clear)
- And 18 more voices with various characteristics

### `openai_tts`

Uses OpenAI's [Text-to-Speech API](https://platform.openai.com/docs/guides/text-to-speech) to speak the text with 6 natural-sounding voices:

- **coral** (Default, warm and natural)
- **alloy** (Balanced tone)
- **echo** (Warm and engaging)  
- **fable** (Expressive and storytelling)
- **onyx** (Deep and resonant)
- **nova** (Bright and articulate)
- **shimmer** (Smooth and pleasant)

Supports three quality models:
- **gpt-4o-mini-tts** - Default, optimized quality and speed
- **tts-1** - Standard quality, faster generation  
- **tts-1-hd** - High definition audio, premium quality

Additional features:
- Speed control from 0.25x to 4.0x (default: 1.0x)
- Custom voice instructions (e.g., "Speak in a cheerful and positive tone") via parameter or `OPENAI_TTS_INSTRUCTIONS` environment variable

## Configuration

### Suppressing "Speaking:" Output

By default, TTS tools return a message like "Speaking: [text]" when speech completes. This can interfere with LLM responses. To suppress this output:

**Environment Variable:**
```bash
export MCP_TTS_SUPPRESS_SPEAKING_OUTPUT=true
```

**Command Line Flag:**
```bash
mcp-tts --suppress-speaking-output
```

When enabled, tools return "Speech completed" instead of echoing the spoken text.

## Getting Started

### Install

#### Linux/WSL Build Dependencies

On Linux and WSL, you need to install ALSA development libraries before building:

```bash
# Ubuntu/Debian
sudo apt-get install libasound2-dev

# Fedora/RHEL
sudo dnf install alsa-lib-devel

# Arch Linux
sudo pacman -S alsa-lib
```

#### Go Install

```bash
go install github.com/blacktop/mcp-tts@latest
```

```bash
❱ mcp-tts --help

TTS (text-to-speech) MCP Server.

Provides multiple text-to-speech services via MCP protocol:

• say_tts - Uses macOS built-in 'say' command (macOS only)
• windows_speech_tts - Uses Windows Speech API via PowerShell (Windows/WSL)
• windows_speech_voices - Lists available Windows Speech API voices (Windows/WSL)
• elevenlabs_tts - Uses ElevenLabs API for high-quality speech synthesis
• google_tts - Uses Google's Gemini TTS models for natural speech
• openai_tts - Uses OpenAI's TTS API with various voice options

Each tool supports different voices, rates, and configuration options.
Requires appropriate API keys for cloud-based services.

Designed to be used with the MCP (Model Context Protocol).

Usage:
  mcp-tts [flags]

Flags:
  -h, --help                       help for mcp-tts
      --suppress-speaking-output   Suppress 'Speaking:' text output
  -v, --verbose                    Enable verbose debug logging
```

#### Set Claude Desktop Config

```json
{
  "mcpServers": {
    "say": {
      "command": "mcp-tts",
      "env": {
        "ELEVENLABS_API_KEY": "********",
        "ELEVENLABS_VOICE_ID": "1SM7GgM6IMuvQlz2BwM3",
        "GOOGLE_AI_API_KEY": "********",
        "OPENAI_API_KEY": "********",
        "OPENAI_TTS_INSTRUCTIONS": "Speak in a cheerful and positive tone",
        "MCP_TTS_SUPPRESS_SPEAKING_OUTPUT": "true"
      }
    }
  }
}
```

#### Environment Variables

- `ELEVENLABS_API_KEY`: Your ElevenLabs API key (required for `elevenlabs_tts`)
- `ELEVENLABS_VOICE_ID`: ElevenLabs voice ID (optional, defaults to a built-in voice)
- `GOOGLE_AI_API_KEY` or `GEMINI_API_KEY`: Your Google AI API key (required for `google_tts`)
- `OPENAI_API_KEY`: Your OpenAI API key (required for `openai_tts`)
- `OPENAI_TTS_INSTRUCTIONS`: Custom voice instructions for OpenAI TTS (optional, e.g., "Speak in a cheerful and positive tone")

### Test

#### Test macOS TTS
```bash
❱ cat test/say.json | go run main.go --verbose

2025/03/23 22:41:49 INFO Starting MCP server name="Say TTS Service" version=1.0.0
2025/03/23 22:41:49 DEBU Say tool called request="{Request:{Method:tools/call Params:{Meta:<nil>}} Params:{Name:say_tts Arguments:map[text:Hello, world!] Meta:<nil>}}"
2025/03/23 22:41:49 DEBU Executing say command args="[--rate 200 Hello, world!]"
2025/03/23 22:41:49 INFO Speaking text text="Hello, world!"
```
```json
{"jsonrpc":"2.0","id":3,"result":{"content":[{"type":"text","text":"Speaking: Hello, world!"}]}}
```

#### Test Google TTS
```bash
❱ cat test/google_tts.json | go run main.go --verbose

2025/05/23 18:26:45 INFO Starting MCP server name="Say TTS Service" version=""
2025/05/23 18:26:45 DEBU Google TTS tool called request="{...}"
2025/05/23 18:26:45 DEBU Generating TTS audio model=gemini-2.5-flash-preview-tts voice=Kore text="Hello! This is a test of Google's TTS API. How does it sound?"
2025/05/23 18:26:49 INFO Playing TTS audio via beep speaker bytes=181006
2025/05/23 18:26:53 INFO Speaking via Google TTS text="Hello! This is a test of Google's TTS API. How does it sound?" voice=Kore
```
```json
{"jsonrpc":"2.0","id":4,"result":{"content":[{"type":"text","text":"Speaking: Hello! This is a test of Google's TTS API. How does it sound? (via Google TTS with voice Kore)"}]}}
```

#### Test OpenAI TTS
```bash
❱ cat test/openai_tts.json | go run main.go --verbose

2025/05/23 19:15:32 INFO Starting MCP server name="Say TTS Service" version=""
2025/05/23 19:15:32 DEBU OpenAI TTS tool called request="{...}"
2025/05/23 19:15:32 DEBU Generating OpenAI TTS audio model=tts-1 voice=nova speed=1.2 text="Hello! This is a test of OpenAI's text-to-speech API. I'm using the nova voice at 1.2x speed."
2025/05/23 19:15:34 DEBU Decoding MP3 stream from OpenAI
2025/05/23 19:15:34 DEBU Initializing speaker for OpenAI TTS sampleRate=22050
2025/05/23 19:15:36 INFO Speaking text via OpenAI TTS text="Hello! This is a test of OpenAI's text-to-speech API. I'm using the nova voice at 1.2x speed." voice=nova model=tts-1 speed=1.2
```
```json
{"jsonrpc":"2.0","id":5,"result":{"content":[{"type":"text","text":"Speaking: Hello! This is a test of OpenAI's text-to-speech API. I'm using the nova voice at 1.2x speed. (via OpenAI TTS with voice nova)"}]}}
```


## License

MIT Copyright (c) 2025 **blacktop**