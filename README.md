<p align="center">
  <a href="https://github.com/blacktop/mcp-say"><img alt="mcp-say Logo" src="https://raw.githubusercontent.com/blacktop/mcp-say/main/docs/logo.webp" /></a>
  <h1 align="center">mcp-say</h1>
  <h4><p align="center">TTS (text-to-speech) MCP Server</p></h4>
  <p align="center">
    <a href="https://github.com/blacktop/mcp-say/actions" alt="Actions">
          <img src="https://github.com/blacktop/mcp-say/actions/workflows/go.yml/badge.svg" /></a>
    <a href="https://github.com/blacktop/mcp-say/releases/latest" alt="Downloads">
          <img src="https://img.shields.io/github/downloads/blacktop/mcp-say/total.svg" /></a>
    <a href="https://github.com/blacktop/mcp-say/releases" alt="GitHub Release">
          <img src="https://img.shields.io/github/release/blacktop/mcp-say.svg" /></a>
    <a href="http://doge.mit-license.org" alt="LICENSE">
          <img src="https://img.shields.io/:license-mit-blue.svg" /></a>
</p>
<br>

## Why? 🤔

Adds Text-to-Speech to things like Claude Desktop and Cursor IDE.

## Getting Started

### Install

```bash
go install github.com/blacktop/mcp-say@latest
```

```bash
❱ mcp-say --help

TTS (text-to-speech) MCP Server.

Provides a text-to-speech service using the MacOS 'say' command.

Designed to be used with the MCP protocol.

Usage:
  mcp-say [flags]

Flags:
  -h, --help      help for mcp-say
  -v, --verbose   Enable verbose debug logging
```

### Test

```bash
❱ cat test/say.json | go run main.go --verbose

2025/03/23 22:41:49 INFO Starting MCP server name="Say TTS Service" version=1.0.0
2025/03/23 22:41:49 DEBU Say tool called request="{Request:{Method:tools/call Params:{Meta:<nil>}} Params:{Name:say Arguments:map[text:Hello, world!] Meta:<nil>}}"
2025/03/23 22:41:49 DEBU Executing say command args="[--rate 200 Hello, world!]"
2025/03/23 22:41:49 INFO Speaking text text="Hello, world!"
```
```json
{"jsonrpc":"2.0","id":3,"result":{"content":[{"type":"text","text":"Speaking: Hello, world!"}]}}
```


## License

MIT Copyright (c) 2025 **blacktop**