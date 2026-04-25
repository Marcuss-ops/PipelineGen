#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
SMOKE_SCRIPT="$ROOT_DIR/scripts/test_clipsearch_smoke.sh"

# Load shared yt-dlp defaults for YouTube.
source "$ROOT_DIR/scripts/env_ytdlp_defaults.sh"

if [[ ! -x "$SMOKE_SCRIPT" ]]; then
  echo "ERROR: missing executable smoke script: $SMOKE_SCRIPT"
  exit 1
fi

echo "== Clipsearch Validation =="
echo "time_utc=$(date -u +%Y-%m-%dT%H:%M:%SZ)"
echo "mode=smoke"

if "$SMOKE_SCRIPT"; then
  echo "\nPASS: clipsearch smoke validation completed"
  exit 0
fi

echo "\nFAIL: clipsearch smoke validation failed"
exit 1
