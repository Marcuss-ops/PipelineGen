#!/usr/bin/env bash
set -Eeuo pipefail

ROOT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd)"
SCRIPT="$ROOT_DIR/scripts/preflight-e2e.sh"

[[ -x "$SCRIPT" ]] || { echo "preflight script must be executable" >&2; exit 1; }

output="$(PREFLIGHT_REQUIRE_MAIN=0 PREFLIGHT_OLLAMA_URL=http://127.0.0.1:1 \
    PREFLIGHT_CHRONON_URL= PREFLIGHT_REQUIRE_DRIVE=0 "$SCRIPT" 2>&1 || true)"
grep -q '^Primary SQLite integrity' <<<"$output"
grep -q 'ENVIRONMENT NOT READY' <<<"$output"

if PREFLIGHT_REQUIRE_MAIN=1 PREFLIGHT_OLLAMA_URL=http://127.0.0.1:1 \
    "$SCRIPT" >/dev/null 2>&1; then
    echo "expected a non-main checkout or unavailable environment to fail" >&2
    exit 1
fi

echo "preflight-e2e tests passed"
