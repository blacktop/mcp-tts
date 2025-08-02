#!/bin/bash

echo "Demonstrating Concurrent vs Sequential Speech"
echo "============================================="
echo ""

echo "Test 1: True Concurrent Speech (using 'say' directly)"
echo "This will show you what concurrent speech sounds like"
echo "-----------------------------------------------------"

start_time=$(date +%s.%N)

# Launch multiple 'say' commands simultaneously - these WILL overlap
say --rate 250 "First speaker talking now" &
say --rate 250 "Second speaker also talking" &  
say --rate 250 "Third speaker speaking too" &

wait

end_time=$(date +%s.%N)
concurrent_time=$(echo "$end_time - $start_time" | bc)

echo "Concurrent speech completed in ${concurrent_time} seconds"
echo "You should have heard overlapping, garbled speech!"
echo ""

sleep 2

echo "Test 2: Sequential Speech (using mcp-tts with mutex)"
echo "This shows how the mutex prevents the cacophony"
echo "------------------------------------------------"

# Create test files
for i in 1 2 3; do
    cat > "/tmp/agent${i}.json" << EOF
{"jsonrpc":"2.0","id":0,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"agent${i}","version":"1.0"}}}
{"jsonrpc":"2.0","id":${i},"method":"tools/call","params":{"name":"say_tts","arguments":{"text":"Agent ${i} speaking clearly","rate":250}}}
EOF
done

start_time=$(date +%s.%N)

# Launch multiple MCP servers - these will coordinate via mutex
cat /tmp/agent1.json | go run main.go --verbose > /tmp/agent1.log 2>&1 &
cat /tmp/agent2.json | go run main.go --verbose > /tmp/agent2.log 2>&1 &
cat /tmp/agent3.json | go run main.go --verbose > /tmp/agent3.log 2>&1 &

wait

end_time=$(date +%s.%N)  
sequential_time=$(echo "$end_time - $start_time" | bc)

echo "Sequential speech completed in ${sequential_time} seconds"
echo "You should have heard clear, orderly speech!"
echo ""

echo "Summary:"
echo "========"
echo "Concurrent (no mutex): ${concurrent_time}s - Fast but garbled"
echo "Sequential (with mutex): ${sequential_time}s - Slower but clear"
echo ""
echo "The mutex solves the multi-agent cacophony problem!"

# Cleanup
rm -f /tmp/agent*.json /tmp/agent*.log