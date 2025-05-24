#!/bin/bash

set -e

# Always run from repo root
cd "$(dirname "$0")/../.."

echo "🧪 Running say_tts command injection test suite..."

TMPDIR=/tmp
FAILED=0

# Array of test cases: (description, payload)
TESTS=(
  "text argument injection (classic)|{\"jsonrpc\":\"2.0\",\"id\":1001,\"method\":\"tools/call\",\"params\":{\"name\":\"say_tts\",\"arguments\":{\"text\":\"Hello world\"; touch /tmp/hacked_say_tts_text\"}}}"
  "voice argument injection (classic)|{\"jsonrpc\":\"2.0\",\"id\":1002,\"method\":\"tools/call\",\"params\":{\"name\":\"say_tts\",\"arguments\":{\"text\":\"Hello world\",\"voice\":\"Alex; touch /tmp/hacked_say_tts_voice\"}}}"
  "rate argument injection (classic)|{\"jsonrpc\":\"2.0\",\"id\":1003,\"method\":\"tools/call\",\"params\":{\"name\":\"say_tts\",\"arguments\":{\"text\":\"Hello world\",\"rate\":\"200; touch /tmp/hacked_rate\"}}}"
  "unicode newline/tab injection|{\"jsonrpc\":\"2.0\",\"id\":2001,\"method\":\"tools/call\",\"params\":{\"name\":\"say_tts\",\"arguments\":{\"text\":\"Hello world\\n; touch /tmp/hacked_unicode\"}}}"
  "shell metacharacter injection|{\"jsonrpc\":\"2.0\",\"id\":2003,\"method\":\"tools/call\",\"params\":{\"name\":\"say_tts\",\"arguments\":{\"text\":\"Hello world \\$(touch /tmp/hacked_shellmeta)\"}}}"
  "array type injection|{\"jsonrpc\":\"2.0\",\"id\":2005,\"method\":\"tools/call\",\"params\":{\"name\":\"say_tts\",\"arguments\":{\"text\":[\"Hello\",\"world; touch /tmp/hacked_array\"]}}}"
  "object type injection|{\"jsonrpc\":\"2.0\",\"id\":2006,\"method\":\"tools/call\",\"params\":{\"name\":\"say_tts\",\"arguments\":{\"voice\":{\"en\":\"Alex; touch /tmp/hacked_obj\"}}}}"
  "scientific notation injection|{\"jsonrpc\":\"2.0\",\"id\":2007,\"method\":\"tools/call\",\"params\":{\"name\":\"say_tts\",\"arguments\":{\"rate\":\"2e2; touch /tmp/hacked_sci\"}}}"
  "float rate injection|{\"jsonrpc\":\"2.0\",\"id\":2008,\"method\":\"tools/call\",\"params\":{\"name\":\"say_tts\",\"arguments\":{\"rate\":200.0,\"text\":\"Hello; touch /tmp/hacked_float\"}}}"
  "null byte/control char injection|{\"jsonrpc\":\"2.0\",\"id\":2009,\"method\":\"tools/call\",\"params\":{\"name\":\"say_tts\",\"arguments\":{\"text\":\"Hello\\u0000; touch /tmp/hacked_null\"}}}"
  "long string overflow injection|{\"jsonrpc\":\"2.0\",\"id\":2010,\"method\":\"tools/call\",\"params\":{\"name\":\"say_tts\",\"arguments\":{\"voice\":\"$(printf 'A%.0s' {1..1000}); touch /tmp/hacked_long\"}}}"
  "escaped quote/backslash injection|{\"jsonrpc\":\"2.0\",\"id\":2011,\"method\":\"tools/call\",\"params\":{\"name\":\"say_tts\",\"arguments\":{\"text\":\"Hello world\\\\\"; touch /tmp/hacked_escape\"}}}"
  "multiple dangerous args at once|{\"jsonrpc\":\"2.0\",\"id\":2012,\"method\":\"tools/call\",\"params\":{\"name\":\"say_tts\",\"arguments\":{\"text\":\"Hello; touch /tmp/hacked_multi1\",\"voice\":\"Alex; touch /tmp/hacked_multi2\",\"rate\":\"200; touch /tmp/hacked_multi3\"}}}"
  "unicode homoglyph injection|{\"jsonrpc\":\"2.0\",\"id\":2013,\"method\":\"tools/call\",\"params\":{\"name\":\"say_tts\",\"arguments\":{\"voice\":\"A\u0439\u0435x; touch /tmp/hacked_homoglyph\"}}}"
  "env variable expansion injection|{\"jsonrpc\":\"2.0\",\"id\":2014,\"method\":\"tools/call\",\"params\":{\"name\":\"say_tts\",\"arguments\":{\"text\":\"Hello $HOME; touch /tmp/hacked_env\"}}}"
  "exec keyword injection|{\"jsonrpc\":\"2.0\",\"id\":2015,\"method\":\"tools/call\",\"params\":{\"name\":\"say_tts\",\"arguments\":{\"text\":\"exec $HOME; touch /tmp/hacked_exec\"}}}"
)

for entry in "${TESTS[@]}"; do
  DESC="${entry%%|*}"
  PAYLOAD="${entry#*|}"
  echo -e "\n🔎 Testing: $DESC"
  echo "Payload: $PAYLOAD"
  rm -f $TMPDIR/hacked*
  echo "$PAYLOAD" | ./mcp-tts --verbose || true
  if ls $TMPDIR/hacked* 1> /dev/null 2>&1; then
    echo "❌ [FAIL] Injection succeeded! $DESC"
    ls $TMPDIR/hacked*
    FAILED=1
    rm -f $TMPDIR/hacked*
  else
    echo "✅ [PASS] Injection prevented."
  fi
  sleep 1
  rm -f $TMPDIR/hacked*
done

if [ $FAILED -eq 0 ]; then
  echo -e "\n🎉 All say_tts injection tests PASSED!"
else
  echo -e "\n❌ Some say_tts injection tests FAILED!"
  exit 1
fi 