#!/bin/bash

echo "🎯 Automated MCP TTS Cancellation Test"
echo "====================================="

# Function to test cancellation
test_engine() {
    local engine_name=$1
    local request_file=$2  
    local cancel_file=$3
    
    echo ""
    echo "🧪 Testing $engine_name cancellation..."
    
    # Create a named pipe for communication
    PIPE="/tmp/mcp_test_$$"
    mkfifo "$PIPE"
    
    # Start the MCP server with the pipe as input
    echo "🚀 Starting MCP server..."
    go run main.go < "$PIPE" > /tmp/mcp_output_$$ 2>&1 &
    SERVER_PID=$!
    
    # Give server time to start
    sleep 2
    
    # Check if server started successfully
    if ! kill -0 $SERVER_PID 2>/dev/null; then
        echo "❌ Failed to start MCP server"
        rm -f "$PIPE"
        return 1
    fi
    
    echo "✅ Server started (PID: $SERVER_PID)"
    
    # Send the long text request
    echo "📤 Sending long text request for $engine_name..."
    cat "$request_file" > "$PIPE" &
    REQUEST_PID=$!
    
    # Wait for audio to start (give it time to begin processing)
    echo "⏱️  Waiting 5 seconds for audio to start..."
    sleep 5
    
    # Send cancellation
    echo "🛑 Sending cancellation request..."
    cat "$cancel_file" > "$PIPE" &
    CANCEL_PID=$!
    
    # Wait a bit more
    sleep 3
    
    # Clean up
    echo "🧹 Cleaning up..."
    kill $SERVER_PID 2>/dev/null
    wait $SERVER_PID 2>/dev/null
    kill $REQUEST_PID 2>/dev/null 
    kill $CANCEL_PID 2>/dev/null
    rm -f "$PIPE"
    
    # Show server output
    echo "📋 Server output:"
    echo "----------------------------------------"
    tail -20 /tmp/mcp_output_$$ 2>/dev/null || echo "No output captured"
    echo "----------------------------------------"
    rm -f /tmp/mcp_output_$$
    
    echo "✅ $engine_name test completed"
}

# Check if we're in the right directory
if [[ ! -f "main.go" ]]; then
    echo "❌ Please run this script from the mcp-say directory"
    exit 1
fi

# Test selection
echo ""
echo "Select test to run:"
echo "1) ElevenLabs TTS"
echo "2) Google TTS" 
echo "3) OpenAI TTS"
echo "4) All engines (sequential)"
echo "5) Quick test (ElevenLabs only)"

read -p "Enter choice (1-5): " choice

case $choice in
    1)
        test_engine "ElevenLabs" "test/long_text_tts.jsonl" "test/cancel_request.jsonl"
        ;;
    2)
        test_engine "Google TTS" "test/long_google_tts.jsonl" "test/cancel_google.jsonl"
        ;;
    3)
        test_engine "OpenAI TTS" "test/long_openai_tts.jsonl" "test/cancel_openai.jsonl"
        ;;
    4)
        test_engine "ElevenLabs" "test/long_text_tts.jsonl" "test/cancel_request.jsonl"
        sleep 2
        test_engine "Google TTS" "test/long_google_tts.jsonl" "test/cancel_google.jsonl"
        sleep 2  
        test_engine "OpenAI TTS" "test/long_openai_tts.jsonl" "test/cancel_openai.jsonl"
        ;;
    5)
        # Quick test with shorter text
        echo '{"jsonrpc":"2.0","id":999,"method":"tools/call","params":{"name":"elevenlabs_tts","arguments":{"text":"This is a shorter test message for quick testing of the cancellation functionality. This should still provide enough time to test cancellation while the text-to-speech system is playing audio."}}}' > /tmp/quick_test.jsonl
        test_engine "ElevenLabs (Quick)" "/tmp/quick_test.jsonl" "test/cancel_request.jsonl"
        rm -f /tmp/quick_test.jsonl
        ;;
    *)
        echo "❌ Invalid choice"
        exit 1
        ;;
esac

echo ""
echo "🎉 Testing completed!"
echo ""
echo "What to look for in the output:"
echo "✅ GOOD: 'Context cancelled, stopping audio playback'"
echo "✅ GOOD: 'Audio playback cancelled'"  
echo "❌ BAD:  Audio continues without stopping"
echo "❌ BAD:  No response to cancellation" 