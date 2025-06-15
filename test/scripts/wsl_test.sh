#!/bin/bash

echo "🔧 WSL TTS Test"
echo "================"

# Check if running in WSL
if ! grep -qEi "(microsoft|wsl)" /proc/version &> /dev/null; then
    echo "❌ Not running in WSL environment"
    exit 1
fi
echo "✅ WSL environment detected"

# Check if PowerShell is available
if ! command -v powershell.exe &> /dev/null; then
    echo "❌ PowerShell not found in PATH"
    exit 1
fi
echo "✅ PowerShell is available"

# Test server startup and WSL tool registration
echo ""
echo "Testing server startup and WSL tool availability..."
TOOLS_RESPONSE=$(echo '{"jsonrpc":"2.0","id":1,"method":"tools/list"}' | timeout 5s go run main.go)

if [[ $? -ne 0 ]]; then
    echo "❌ Server failed to start"
    exit 1
fi

if [[ "$TOOLS_RESPONSE" == *"wsl_tts"* ]]; then
    echo "✅ WSL TTS tool is registered"
else
    echo "❌ WSL TTS tool not found in tools list"
    exit 1
fi

# Test basic WSL TTS functionality
echo ""
echo "Testing basic WSL TTS..."
echo '{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"wsl_tts","arguments":{"text":"WSL text to speech test successful"}}}' | timeout 10s go run main.go

if [[ $? -eq 0 ]]; then
    echo "✅ Basic WSL TTS test passed"
else
    echo "❌ Basic WSL TTS test failed"
    exit 1
fi

# Test with custom rate
echo ""
echo "Testing WSL TTS with custom rate..."
echo '{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"wsl_tts","arguments":{"text":"Testing faster speech rate","rate":5}}}' | timeout 10s go run main.go

if [[ $? -eq 0 ]]; then
    echo "✅ Custom rate test passed"
else
    echo "❌ Custom rate test failed"
    exit 1
fi

# Test cancellation
echo ""
echo "Testing WSL TTS cancellation..."
FIFO=$(mktemp -u)
mkfifo $FIFO

# Start the server with the named pipe
go run main.go < $FIFO &
SERVER_PID=$!

# Give the server time to start
sleep 1

# Send the TTS request
echo '{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"wsl_tts","arguments":{"text":"This is a longer text that should be cancelled before it finishes speaking completely"}}}' > $FIFO &

# Wait a moment then send cancellation
sleep 1
echo '{"jsonrpc":"2.0","id":5,"method":"$/cancel","params":{"id":"wsl_tts-4"}}' > $FIFO

# Wait for completion
sleep 2

# Cleanup
kill $SERVER_PID 2>/dev/null
rm -f $FIFO

echo ""
echo "✅ All WSL TTS tests completed successfully!"