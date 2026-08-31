#!/usr/bin/env bash
# Intro-hook stock binding practical suite (no render).
#
# Demonstrates that intro-hook can simultaneously carry timeline clips
# before its narration, a direct stock binding during the narration,
# timeline clips after the narration, its own source_text and its own
# voiceover — without generating or rendering any video. Only the
# script-generation contract is exercised.
#
# Runs against the live PipelineGen service; authentication is delegated
# to scripts/with-velox-auth. The voiceover cases (Test 4/9/final) need
# BOXERS_VOICEOVER_FOLDER_ID.
set -euo pipefail

REPO_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
TEST_DIR="$REPO_DIR/tests/operational/boxers-generate"
API_BASE="${VELOX_BASE_URL:-${SMOKE_API_BASE:-http://127.0.0.1:8000}}"
DB_PATH="${VELOX_DB:?VELOX_DB must be explicitly set to an isolated or approved database}"
FULL="${FULL:-/tmp/intro-hook-stock-full.json}"

die() {
  echo "FAIL: $*" >&2
  exit 1
}

require_command() {
  command -v "$1" >/dev/null 2>&1 || die "comando mancante: $1"
}

require_command curl
require_command jq
require_command python3
[[ -f "$DB_PATH" ]] || die "database SQLite assente: $DB_PATH"

echo "PipelineGen: $API_BASE"
scripts/with-velox-auth bash -c 'curl -fsS --max-time 15 -H "Authorization: Bearer ${VELOX_ADMIN_TOKEN}" "'"$API_BASE"'/health"' | jq -e '.ok == true' >/dev/null

scripts/with-velox-auth env VELOX_BASE_URL="$API_BASE" SMOKE_TIMEOUT_SECONDS="${SMOKE_TIMEOUT_SECONDS:-2700}" \
  FULL="$FULL" \
  python3 "$TEST_DIR/intro_hook_suite.py" "$@"
