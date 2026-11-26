#!/usr/bin/env bash
# Plays test/json/volume.jsonl at several volume levels so you can hear the
# difference. The clip announces its level as a percentage, filled into the
# fixture's {{percent}} placeholder. Each level plays say_tts, which renders the
# clip and plays it with afplay -v like the cloud voices, then voice_tts when
# voice-say is installed, which passes the level to voice-say --volume. Each
# clip names its tool. macOS only; speaks aloud.
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

tools=(say_tts)
if command -v voice-say >/dev/null; then
	tools+=(voice_tts)
	if ! voice-say --help 2>&1 | grep -q -- --volume; then
		echo "note: this voice-say has no --volume option, so voice_tts plays at full volume"
	fi
else
	echo "note: voice-say is not installed, so only say_tts plays"
fi

# play runs the fixture through one server at the given volume with the given
# tool and checks that the server used that volume and the call succeeded. The
# server drops requests it has not read when stdin closes, so stdin stays open
# until the response arrives (up to 120 seconds, to outlast another session's
# speech and voice-say's model loading). Closing it then lets the server finish
# the clip and exit.
play() {
	local level=$1 tool=$2 speaker=$3
	local log="$work/server.log" out="$work/server.out"
	local percent
	percent=$(awk -v v="$level" 'BEGIN { printf "%g", v * 100 }')
	env MCP_TTS_VOLUME="$level" "$bin" --verbose <"$fifo" >"$out" 2>"$log" &
	local pid=$!
	exec 3>"$fifo"
	sed -e "s/{{percent}}/$percent/" -e "s/{{tool}}/$tool/" -e "s/{{speaker}}/$speaker/" "$fixture" >&3
	local tries=0
	until grep -q '"id":2' "$out" || ((tries++ >= 1200)); do
		sleep 0.1
	done
	exec 3>&-
	wait "$pid" || true
	local response
	response=$(grep '"id":2' "$out" || true)
	if [[ -z $response || $response == *'"isError":true'* ]]; then
		echo "FAIL: $tool did not succeed at volume $level. Response: ${response:-none}" >&2
		grep -E 'ERRO|WARN' "$log" >&2 || true
		exit 1
	fi
	grep -o 'Playback volume volume=[^ ]*' "$log" | sed "s/^/  $tool server: /"
}

for level in "${levels[@]}"; do
	echo
	echo "Volume $level"
	for tool in "${tools[@]}"; do
		speaker=Say
		[[ $tool == voice_tts ]] && speaker=Voice
		play "$level" "$tool" "$speaker"
		sleep 1
	done
done

echo
echo "Done."
