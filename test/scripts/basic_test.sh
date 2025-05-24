#!/bin/bash

echo "🔧 Basic MCP Server Test"
echo "========================"

# Check environment
echo "Checking environment..."
if [[ -z "$OPENAI_API_KEY" ]]; then
    echo "❌ OPENAI_API_KEY not set"
    exit 1
fi
echo "✅ OPENAI_API_KEY: ${OPENAI_API_KEY:0:8}..."

# Test server startup and basic functionality
echo ""
echo "Testing server startup and tool listing..."
echo '{"jsonrpc":"2.0","id":1,"method":"tools/list"}' | timeout 5s go run main.go

echo ""
echo "Testing simple OpenAI TTS (short text)..."
echo '{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"openai_tts","arguments":{"text":"Hello world test","voice":"coral","model":"gpt-4o-mini-tts"}}}' | timeout 10s go run main.go 