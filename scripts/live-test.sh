#!/bin/bash
set -eo pipefail
root=$(cd "$(dirname "$0")/.." && pwd)
model=haiku
run=.
for a in "$@"; do
	case "$a" in
	MODEL=*) model=${a#MODEL=} ;;
	RUN=*) run=${a#RUN=} ;;
	*) echo "unknown argument: $a" >&2; exit 2 ;;
	esac
done
case "$model" in
ollama:*)
	host=${QUILL_OLLAMA_URL:-http://localhost:11434}
	if ! curl -fsS -m 2 "$host/api/version" >/dev/null 2>&1; then
		echo "Ollama is not running at $host; not testing a local model." >&2
		exit 3
	fi
	if command -v pzlock >/dev/null 2>&1; then
		st=$(pzlock status 2>/dev/null || true)
		if ! echo "$st" | grep -q '^holder : (free)' || ! echo "$st" | grep -q '^game   : not running' || echo "$st" | sed -n '/^queue/,/^game/p' | grep -q '^  '; then
			echo "A Zomboid test holds or waits for the GPU (pzlock status); not loading a local model now." >&2
			exit 3
		fi
	fi
	;;
esac
export PATH="$HOME/.local/go/bin:$PATH"
"$root/scripts/build-whisper.sh"
cd "$root"
QUILL_LIVE_MODEL=$model go test -tags live -count=1 -timeout 60m -run "^TestLive(${run})" -v ./internal/session/ 2>&1 |
	{ grep -E '^(--- |ok |FAIL|PASS$|panic)|LIVE |stderr: |live_test.go:[0-9]+: [^L]' || true; }
