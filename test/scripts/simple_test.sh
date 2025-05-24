#!/bin/bash

echo "🎯 Simple MCP TTS Cancellation Test"
echo "==================================="

# Check if we're in the right directory
if [[ ! -f "main.go" ]]; then
    echo "❌ Please run this script from the mcp-say directory"
    exit 1
fi

echo ""
echo "This script will:"
echo "1. Start the MCP server"
echo "2. Send an OpenAI TTS request"
echo "3. Wait for audio to start"
echo "4. Send a cancellation request"
echo "5. Show you the results"
echo ""

# Check for required environment variables
if [[ -z "$OPENAI_API_KEY" ]]; then
    echo "❌ OPENAI_API_KEY environment variable not set"
    echo "Please set it with: export OPENAI_API_KEY='your-key-here'"
    exit 1
fi

echo "✅ OPENAI_API_KEY is set"

# Create a temporary script for automation
cat > /tmp/mcp_test_input.txt << 'EOF'
{"jsonrpc":"2.0","id":777,"method":"tools/call","params":{"name":"openai_tts","arguments":{"text":"This is a test message for OpenAI TTS cancellation functionality. We are generating enough text content to ensure that the audio generation and playback process takes sufficient time to allow for proper testing of the cancellation mechanism. This should provide adequate time to test the cancellation while the audio is actively playing.","voice":"coral","model":"gpt-4o-mini-tts","speed":1.0}}}
EOF

cat > /tmp/mcp_test_cancel.txt << 'EOF'
{"jsonrpc":"2.0","method":"notifications/cancelled","params":{"requestId":777,"reason":"Testing cancellation"}}
EOF

echo "🚀 Starting MCP server..."
echo "📋 You should see server logs below:"
echo "===================================="

# Use a much simpler approach with a timeout
{
    sleep 2
    echo "📤 Sending TTS request..."
    cat /tmp/mcp_test_input.txt
    sleep 8
    echo "🛑 Sending cancellation..."
    cat /tmp/mcp_test_cancel.txt
    sleep 2
} | timeout 20s go run main.go

echo ""
echo "===================================="
echo "🔍 Test completed!"
echo ""
echo "What to look for in the output above:"
echo "✅ GOOD: 'Speaking text via OpenAI TTS'"
echo "✅ GOOD: 'Context cancelled, stopping OpenAI TTS audio playback'"
echo "✅ GOOD: 'OpenAI TTS audio playback cancelled by user'"
echo "❌ BAD:  Error messages about API keys or server issues"
echo "❌ BAD:  No cancellation messages"

# Cleanup
rm -f /tmp/mcp_test_input.txt /tmp/mcp_test_cancel.txt 