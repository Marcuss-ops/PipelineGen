#!/usr/bin/env bash
# Fail-closed dead-code ratchet.
#
# The guard is the measured production unreachable count at the 2026-11-21
# sweep. It may only go DOWN: after a successful sweep, lower MAX and update
# docs/operations/deadcode-2026-11-21.txt in the same change. Never raise it to
# make a red tree green.
set -euo pipefail

MAX=229
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "$ROOT"

BIN="${DEADCODE_BIN:-}"
if [[ -z "$BIN" ]]; then
	if command -v deadcode >/dev/null 2>&1; then
		BIN="$(command -v deadcode)"
	elif [[ -x "$(go env GOPATH)/bin/deadcode" ]]; then
		BIN="$(go env GOPATH)/bin/deadcode"
	else
		echo "dead-code guard: deadcode not found." >&2
		echo "install it with: go install golang.org/x/tools/cmd/deadcode@latest" >&2
		exit 2
	fi
fi

OUT="$(mktemp)"
ERR="$(mktemp)"
trap 'rm -f "$OUT" "$ERR"' EXIT

if ! "$BIN" -test=false ./... >"$OUT" 2>"$ERR"; then
	echo "dead-code guard: deadcode failed" >&2
	cat "$ERR" >&2
	exit 1
fi

COUNT="$(grep -c 'unreachable func' "$OUT" || true)"
echo "dead-code guard: $COUNT unreachable functions (max $MAX)"

if (( COUNT > MAX )); then
	echo "❌ dead-code regression: $COUNT > $MAX" >&2
	echo "The first 20 new offenders:" >&2
	grep 'unreachable func' "$OUT" | head -20 >&2
	exit 1
fi

echo "✅ dead-code guard passed"
