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

It registers four TTS tools: 
 - `say_tts` 
 - `elevenlabs_tts`
 - `google_tts`
 - `openai_tts`

### `say_tts`

Uses the macOS `say` binary to speak the text with built-in system voices

### `elevenlabs_tts`

Uses the [ElevenLabs](https://elevenlabs.io/app/speech-synthesis/text-to-speech) text-to-speech API to speak the text with premium AI voices

### `google_tts`

Uses Google's [Gemini TTS models](https://ai.google.dev/gemini-api/docs/speech-generation) to speak the text with 30 high-quality voices. Available voices include:

**Achernar, Achird, Algenib, Algieba, Alnilam, Aoede, Autonoe, Callirrhoe, Charon, Despina, Enceladus, Erinome, Fenrir, Gacrux, Iapetus, Kore, Laomedeia, Leda, Orus, Puck, Pulcherrima, Rasalgethi, Sadachbia, Sadaltager, Schedar, Sulafat, Umbriel, Vindemiatrix, Zephyr, Zubenelgenubi**

### `openai_tts`

Uses OpenAI's [Text-to-Speech API](https://platform.openai.com/docs/guides/text-to-speech) to speak the text with 10 natural-sounding voices:

- **alloy** (Warm, conversational, modern)
- **ash** (Confident, assertive, slightly textured)
- **ballad** (Gentle, melodious, slightly lyrical)
- **coral** (Cheerful, fresh, upbeat)
- **echo** (Neutral, calm, balanced)
- **fable** (Storyteller-like, expressive)
- **nova** (Clear, precise, slightly formal)
- **onyx** (Deep, authoritative, resonant)
- **sage** (Soothing, empathetic, reassuring)
- **shimmer** (Bright, animated, playful)
- **verse** (Versatile, expressive)

Supports three quality models:
- **gpt-4o-mini-tts** - Default, optimized quality and speed
- **tts-1** - Standard quality, faster generation  
- **tts-1-hd** - High definition audio, premium quality

Additional features:
- Speed control from 0.25x to 4.0x (default: 1.0x)
- Custom voice instructions (e.g., "Speak in a cheerful and positive tone") via parameter or `OPENAI_TTS_INSTRUCTIONS` environment variable

## Configuration

### Sequential vs Concurrent TTS

By default, the TTS server enforces sequential speech operations - only one TTS request can play audio at a time. This prevents multiple agents from speaking simultaneously and creating an unintelligible cacophony. Subsequent requests will wait in a queue until the current speech completes.

**Multi-Instance Protection**: The mutex works both within a single MCP server process and across multiple Claude Desktop instances. When running multiple Claude Desktop terminals, they coordinate via a system-wide file lock to prevent overlapping speech.

To allow concurrent TTS operations (multiple speeches playing simultaneously):

**Environment Variable:**
```bash
export MCP_TTS_ALLOW_CONCURRENT=true
```

**Command Line Flag:**
```bash
mcp-tts --sequential-tts=false
```

> **Note:** Concurrent TTS may result in overlapping audio that's difficult to understand. Use this option only when you explicitly want multiple TTS operations to run simultaneously.

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

```bash
go install github.com/blacktop/mcp-tts@latest
```

```bash
❱ mcp-tts --help

TTS (text-to-speech) MCP Server.

Provides multiple text-to-speech services via MCP protocol:

• say_tts - Uses macOS built-in 'say' command (macOS only)
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
      --sequential-tts             Enforce sequential TTS (prevent concurrent speech) (default true)
      --suppress-speaking-output   Suppress 'Speaking:' text output
  -v, --verbose                    Enable verbose debug logging
```

### Configuration

#### [Claude Desktop](https://claude.ai/download)

Add to `~/Library/Application Support/Claude/claude_desktop_config.json`:

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
        "MCP_TTS_SUPPRESS_SPEAKING_OUTPUT": "true",
        "MCP_TTS_ALLOW_CONCURRENT": "false"
      }
    }
  }
}
```

#### [Claude Code](https://docs.anthropic.com/en/docs/agents-and-tools/claude-code/overview)

```bash
claude mcp add say \
  -e GOOGLE_AI_API_KEY=your_key \
  -e ELEVENLABS_API_KEY=your_key \
  -e OPENAI_API_KEY=your_key \
  -- mcp-tts
```

#### [Codex CLI](https://platform.openai.com/docs/guides/mcp)

```bash
codex mcp add say \
  --env GOOGLE_AI_API_KEY=your_key \
  --env ELEVENLABS_API_KEY=your_key \
  --env OPENAI_API_KEY=your_key \
  -- mcp-tts
```

#### [Gemini CLI](https://github.com/google/gemini-cli)

```bash
gemini mcp add say mcp-tts \
  -e GOOGLE_AI_API_KEY=your_key \
  -e ELEVENLABS_API_KEY=your_key \
  -e OPENAI_API_KEY=your_key
```

Or manually add to `~/.gemini/settings.json` (or `.gemini/settings.json` in project root):

```json
{
  "mcpServers": {
    "say": {
      "command": ["mcp-tts"],
      "env": {
        "GOOGLE_AI_API_KEY": "..."
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
- `MCP_TTS_SUPPRESS_SPEAKING_OUTPUT`: Set to "true" to suppress "Speaking:" output (optional)
- `MCP_TTS_ALLOW_CONCURRENT`: Set to "true" to allow concurrent TTS operations (optional, defaults to sequential)

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

#### Test the mutex behavior with multiple TTS requests

```bash
# Sequential mode (default) - speeches play one after another
cat test/sequential.json | go run main.go --verbose

# Concurrent mode - allows overlapping speech  
cat test/sequential.json | go run main.go --verbose --sequential-tts=false
```

## Skill: `speak`

This repo includes a **speak** skill that automatically announces plans, issues, and summaries aloud using TTS. Each project gets a unique voice so you can identify which project is speaking from another room.

Skills follow the [Agent Skills](https://agentskills.io) open standard and work across Claude Code, Codex CLI, and Gemini CLI.

### Install Skill

#### Claude Code

**Via Plugin Marketplace** (recommended):
```bash
claude plugin marketplace add blacktop/mcp-tts
claude plugin install speak@mcp-tts
```

**Or manually:**
```bash
mkdir -p ~/.claude/skills
git clone https://github.com/blacktop/mcp-tts.git /tmp/mcp-tts
cp -r /tmp/mcp-tts/skill ~/.claude/skills/speak
```

The skill is now available. Claude will use it automatically when relevant, or invoke directly with `/speak`.

#### Codex CLI

**Using the skill-installer** (within a Codex session):
```
$skill-installer install the speak skill from https://github.com/blacktop/mcp-tts --path skill
```

**Or manually:**
```bash
mkdir -p ~/.codex/skills
git clone https://github.com/blacktop/mcp-tts.git /tmp/mcp-tts
cp -r /tmp/mcp-tts/skill ~/.codex/skills/speak
```

Restart Codex after installing.

#### Gemini CLI

Gemini CLI uses **extensions** to bundle skills. Install this repo as an extension:

```bash
gemini extensions install https://github.com/blacktop/mcp-tts.git
```

This installs the `speak` skill.

**Or manually** (skill only):
```bash
mkdir -p ~/.gemini/skills
git clone https://github.com/blacktop/mcp-tts.git /tmp/mcp-tts
cp -r /tmp/mcp-tts/skill ~/.gemini/skills/speak
```

> **Note:** Gemini CLI skills are experimental. Enable via `/settings` → search "Skills" → toggle on.

### Shared Skills Directory (Optional)

To maintain one copy across all agents, use symlinks:

```bash
# Create shared skills directory
mkdir -p ~/.agents/skills

# Clone and copy
git clone https://github.com/blacktop/mcp-tts.git /tmp/mcp-tts
cp -r /tmp/mcp-tts/skill ~/.agents/skills/speak

# Symlink to each agent
ln -sf ~/.agents/skills/speak ~/.claude/skills/speak
ln -sf ~/.agents/skills/speak ~/.codex/skills/speak
ln -sf ~/.agents/skills/speak ~/.gemini/skills/speak
```

### Verify Installation

| Agent | Command |
|-------|---------|
| Claude Code | Ask "What skills are available?" or type `/speak` |
| Codex CLI | Skills load automatically on restart |
| Gemini CLI | `gemini extensions list` or check `/settings` for skills |

### How It Works

The skill triggers automatically after:
- **Planning complete** - When a plan/todo list is finalized
- **Issue resolved** - When a bug fix or error is resolved
- **Summary generated** - When completing a major task

Providers fallback in order: `google` → `openai` → `elevenlabs` → `say` (macOS). If a provider fails due to missing API keys, it's marked unavailable and skipped in future attempts.

## License

MIT Copyright (c) 2025 **blacktop**