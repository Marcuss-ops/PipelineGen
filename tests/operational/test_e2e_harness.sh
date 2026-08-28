#!/usr/bin/env bash
set -Eeuo pipefail

ROOT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd)"
HARNESS="$ROOT_DIR/scripts/dev/e2e-up.sh"

[[ -x "$HARNESS" ]] || { echo "harness must be executable" >&2; exit 1; }
bash -n "$HARNESS"
grep -q 'checkout must be on branch main' "$HARNESS"
grep -q 'compose up -d qdrant artlist-scraper searxng' "$HARNESS"
grep -q 'run_preflight' "$HARNESS"
grep -q 'stop_process worker' "$HARNESS"
grep -q 'compose down' "$HARNESS"

if "$HARNESS" unknown >/dev/null 2>&1; then
    echo "unknown harness command unexpectedly succeeded" >&2
    exit 1
fi

echo "e2e harness tests passed"
