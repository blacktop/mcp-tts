#!/usr/bin/env bash
# Plays test/json/volume.jsonl at several volume levels so you can hear the
# difference. The clip announces its level as a percentage, filled into the
# fixture's {{percent}} placeholder. Each level plays twice: say speaking directly (the [[volm]]
# command) and say writing a file that afplay plays (afplay -v, the same route
# the cloud voices use). macOS only; speaks aloud.
#
# Run from the repo root:
#   bash test/scripts/volume_test.sh            # levels 0.25 0.5 1
#   bash test/scripts/volume_test.sh 0.1 0.3    # your own levels
set -euo pipefail

fixture=test/json/volume.jsonl
if [[ ! -f $fixture ]]; then
	echo "Run this from the repo root; $fixture not found" >&2
	exit 1
fi
levels=("$@")
if [[ ${#levels[@]} -eq 0 ]]; then
	levels=(0.25 0.5 1)
fi

work=$(mktemp -d)
bin="$work/mcp-tts"
fifo="$work/stdin"
mkfifo "$fifo"
echo "Building mcp-tts into $work"
go build -o "$bin" .

echo
echo "Checking that an out-of-range volume is refused..."
if env MCP_TTS_VOLUME=2 "$bin" </dev/null >/dev/null 2>"$work/refused.log"; then
	echo "FAIL: MCP_TTS_VOLUME=2 was accepted" >&2
	exit 1
fi
if ! grep -q "out of range" "$work/refused.log"; then
	echo "FAIL: MCP_TTS_VOLUME=2 failed for another reason:" >&2
	cat "$work/refused.log" >&2
	exit 1
fi
echo "ok: MCP_TTS_VOLUME=2 was refused"

# play runs the fixture through one server at the given volume and checks that
# the server used that volume and the say_tts call succeeded. The server drops
# requests it has not read when stdin closes, so stdin stays open until the
# say_tts response arrives (up to 60 seconds, to outlast another session's
# speech). Closing it then makes the server finish the clip and exit.
play() {
	local level=$1
	shift
	local log="$work/server.log" out="$work/server.out"
	local percent
	percent=$(awk -v v="$level" 'BEGIN { printf "%g", v * 100 }')
	env MCP_TTS_VOLUME="$level" "$bin" --verbose "$@" <"$fifo" >"$out" 2>"$log" &
	local pid=$!
	exec 3>"$fifo"
	sed "s/{{percent}}/$percent/" "$fixture" >&3
	local tries=0
	until grep -q '"id":2' "$out" || ((tries++ >= 600)); do
		sleep 0.1
	done
	exec 3>&-
	wait "$pid" || true
	local response
	response=$(grep '"id":2' "$out" || true)
	if [[ -z $response || $response == *'"isError":true'* ]]; then
		echo "FAIL: say_tts did not succeed at volume $level. Response: ${response:-none}" >&2
		grep -E 'ERRO|WARN' "$log" >&2 || true
		exit 1
	fi
	grep -o 'Playback volume volume=[^ ]*' "$log" | sed 's/^/  server: /'
}

for level in "${levels[@]}"; do
	echo
	echo "Volume $level: say speaks directly"
	play "$level"
	sleep 1
	echo "Volume $level: say writes a file, afplay plays it"
	play "$level" --output-dir "$work"
	sleep 1
done

echo
echo "Done. Saved files in $work stay at full level by design."
