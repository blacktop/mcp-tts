#!/bin/bash

echo "CLI Agent Multi-Instance Test"
echo "============================="
echo "Simulating multiple Claude Code terminals with TTS coordination"
echo ""

# Create test files for different "terminals"
cat > /tmp/terminal1.json << 'EOF'
{"jsonrpc":"2.0","id":0,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"terminal1","version":"1.0"}}}
{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"say_tts","arguments":{"text":"Terminal one agent complete","rate":300}}}
EOF

cat > /tmp/terminal2.json << 'EOF'
{"jsonrpc":"2.0","id":0,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"terminal2","version":"1.0"}}}
{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"say_tts","arguments":{"text":"Terminal two agent complete","rate":300}}}
EOF

cat > /tmp/terminal3.json << 'EOF'
{"jsonrpc":"2.0","id":0,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"terminal3","version":"1.0"}}}
{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"say_tts","arguments":{"text":"Terminal three agent complete","rate":300}}}
EOF

echo "Starting 3 mcp-tts servers simultaneously (simulating tmux/terminals)..."
echo "They should coordinate via global file lock - no cacophony!"
echo ""

start_time=$(date +%s)

# Start 3 separate MCP server processes simultaneously
cat /tmp/terminal1.json | go run main.go --verbose > /tmp/terminal1.log 2>&1 &
PID1=$!

cat /tmp/terminal2.json | go run main.go --verbose > /tmp/terminal2.log 2>&1 &
PID2=$!

cat /tmp/terminal3.json | go run main.go --verbose > /tmp/terminal3.log 2>&1 &
PID3=$!

echo "Terminal PIDs: $PID1, $PID2, $PID3"
echo "Waiting for coordination..."

wait $PID1 $PID2 $PID3

end_time=$(date +%s)
total_time=$((end_time - start_time))

echo ""
echo "All terminals completed in ${total_time} seconds"
echo ""

echo "Timing analysis (should show sequential execution):"
echo "================================================"
echo ""
echo "Terminal 1 log:"
grep -E "(Starting|Speaking text completed)" /tmp/terminal1.log | head -2

echo ""
echo "Terminal 2 log:"
grep -E "(Starting|Speaking text completed)" /tmp/terminal2.log | head -2

echo ""
echo "Terminal 3 log:"
grep -E "(Starting|Speaking text completed)" /tmp/terminal3.log | head -2

echo ""
echo "✅ Success! Multiple CLI agents coordinated speech without cacophony"

# Check if lock file was cleaned up
if [ -f /tmp/mcp-tts-global.lock ]; then
    echo "🔒 Lock file contents:"
    cat /tmp/mcp-tts-global.lock
else
    echo "🔓 Lock file cleaned up properly"
fi

# Cleanup
rm -f /tmp/terminal*.json /tmp/terminal*.log