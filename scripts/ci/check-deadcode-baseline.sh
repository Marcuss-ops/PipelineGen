#!/usr/bin/env bash
# Fail-closed dead-code ratchet.
#
# The guard is the measured production unreachable count. It may only go DOWN:
# after a successful sweep, lower MAX and update the frozen baseline in the
# same change. Never raise it to make a red tree green.
#
# 2026-11-26 (P2-08): test-support packages are test oracles, not production
# code. They are inventoried by the ticket and excluded from the production
# guard; counting them made test helpers look like dead production functions.
# The re-baseline is downward: 229 -> 219.
#
# 2026-11-27 (audit residue): sweep of the orphaned verbatimcheck package
# (8 unreachable functions, zero importers) and two definition-only helpers.
# The re-baseline is downward: 219 -> 214.
set -euo pipefail

MAX=214
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

# Production roots only. The excluded packages are explicitly test support:
# PostgreSQL/SQLite test databases, shared test doubles and the mutation probe.
TEST_SUPPORT_EXCLUDE='/(pgtest|sqltest|testsupport|mutationprobe)$'
mapfile -t PACKAGES < <(go list ./... | grep -vE "$TEST_SUPPORT_EXCLUDE")
if (( ${#PACKAGES[@]} == 0 )); then
	echo "dead-code guard: no production packages resolved" >&2
	exit 2
fi

if ! "$BIN" -test=false "${PACKAGES[@]}" >"$OUT" 2>"$ERR"; then
	echo "dead-code guard: deadcode failed" >&2
	cat "$ERR" >&2
	exit 1
fi

# The ratchet covers TRACKED production code. An untracked file can be
# mid-write, or simply not part of the change yet; it is reported below but
# does not set the baseline until it is staged. This keeps a shared worktree
# from turning the guard red on code the sweep does not own.
TOTAL="$(grep -c 'unreachable func' "$OUT" || true)"
COUNT="$(awk 'NR==FNR { tracked[$0]=1; next }
	/unreachable func/ { p=$0; sub(/:.*/, "", p); if (p in tracked) print }' \
	<(git ls-files -- '*.go') "$OUT" | wc -l)"
COUNT="$((COUNT + 0))"
EXCLUDED=$((TOTAL - COUNT))
if (( EXCLUDED > 0 )); then
	echo "dead-code guard: $EXCLUDED untracked entries not counted (pending work)" >&2
fi
echo "dead-code guard: $COUNT unreachable production functions (max $MAX)"

if (( COUNT > MAX )); then
	echo "❌ dead-code regression: $COUNT > $MAX" >&2
	echo "The first 20 new offenders:" >&2
	grep 'unreachable func' "$OUT" | head -20 >&2
	exit 1
fi

echo "✅ dead-code guard passed"
