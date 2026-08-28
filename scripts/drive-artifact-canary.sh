#!/usr/bin/env bash
# scripts/drive-artifact-canary.sh — authenticated Drive artifact E2E check.
#
# The API canary owns the canonical Publisher write path. This script verifies
# credentials, publishes a unique artifact, reads it back through the canonical
# resolve-by-id endpoint, and trashes the temporary artifact. IDs and tokens
# are never printed.
set -Eeuo pipefail

BASE_URL="${DRIVE_CANARY_BASE_URL:-${PREFLIGHT_BASE_URL:-http://127.0.0.1:${VELOX_PORT:-8000}}}"
FOLDER_ID="${DRIVE_CANARY_FOLDER_ID:-${PREFLIGHT_DRIVE_FOLDER_ID:-}}"
TOKEN="${VELOX_ADMIN_TOKEN:-}"
TOKEN_FILE="${TOKEN_FILE:-/etc/pipelinegen/pipelinegen.env}"
TIMEOUT="${DRIVE_CANARY_TIMEOUT_SECONDS:-15}"

fail() { printf 'Drive artifact canary: FAIL: %s\n' "$1" >&2; exit 1; }
require() { command -v "$1" >/dev/null 2>&1 || fail "required command unavailable: $1"; }
require curl
require jq

if [[ -z "$TOKEN" && -r "$TOKEN_FILE" ]]; then
    TOKEN="$(sed -n 's/^VELOX_ADMIN_TOKEN=//p' "$TOKEN_FILE" | tail -n 1)"
fi
[[ "$TOKEN" =~ ^[a-fA-F0-9]{64}$ ]] || fail 'credentials missing or invalid'
[[ -n "$FOLDER_ID" ]] || fail 'DRIVE_CANARY_FOLDER_ID is required'
[[ "$BASE_URL" =~ ^https?://[^[:space:]]+$ ]] || fail 'invalid base URL'

work_dir="$(mktemp -d "${TMPDIR:-/tmp}/pipelinegen-drive-canary.XXXXXX")"
chmod 700 "$work_dir"
cleanup() {
    if [[ -n "${file_id:-}" ]]; then
        curl -fsS --max-time "$TIMEOUT" -X POST \
            -H "Authorization: Bearer $TOKEN" \
            -H 'Content-Type: application/json' \
            --data "$(jq -nc --arg id "$file_id" '{file_id:$id}')" \
            "$BASE_URL/api/drive/trash" >/dev/null 2>&1 || true
    fi
    rm -rf "$work_dir"
}
trap cleanup EXIT INT TERM

payload="$work_dir/payload.json"
name="pipelinegen-e2e-canary-$(date +%s)-$$.txt"
printf 'PipelineGen Drive artifact canary\n' > "$payload"

# Credential verification: an authenticated request must reach the canonical
# canary handler. HTTP 401/403 is distinct from a publish failure.
health_code="$(curl -sS --max-time "$TIMEOUT" -o /dev/null -w '%{http_code}' \
    -H "Authorization: Bearer $TOKEN" "$BASE_URL/ready" || true)"
[[ "$health_code" != 401 && "$health_code" != 403 ]] || fail 'credentials rejected by PipelineGen'

body="$work_dir/upload.json"
code="$(curl -sS --max-time "$TIMEOUT" -o "$body" -w '%{http_code}' -X POST \
    -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
    --data "$(jq -nc --arg folder "$FOLDER_ID" --arg path "$payload" --arg name "$name" \
        '{folder_id:$folder, local_path:$path, filename:$name}')" \
    "$BASE_URL/api/drive/canary-upload" || true)"
[[ "$code" =~ ^2[0-9][0-9]$ ]] || fail "artifact publication failed (HTTP $code)"

file_id="$(jq -r '.file_id // empty' "$body" 2>/dev/null || true)"
drive_link="$(jq -r '.drive_link // empty' "$body" 2>/dev/null || true)"
[[ -n "$file_id" && -n "$drive_link" ]] || fail 'publish response missing artifact identity'

read_body="$work_dir/read.json"
read_code="$(curl -sS --max-time "$TIMEOUT" -o "$read_body" -w '%{http_code}' -X POST \
    -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
    --data "$(jq -nc --arg id "$file_id" '{ids:[$id]}')" \
    "$BASE_URL/api/drive/resolve-by-id" || true)"
[[ "$read_code" =~ ^2[0-9][0-9]$ ]] || fail "artifact read-back failed (HTTP $read_code)"
jq -e --arg id "$file_id" '
    (.resolved | length) >= 1 and
    .resolved[0].id == $id and
    (.resolved[0].trashed // false) == false and
    ((.resolved[0].mime_type // .resolved[0].mimeType // .resolved[0].MimeType // "") | length) > 0 and
    ((.resolved[0].size // 0) | tonumber) > 0
' "$read_body" >/dev/null 2>&1 || fail 'artifact read-back contract failed'

printf 'Drive artifact canary: PASS (credentials, write, publish, read-back, cleanup scheduled)\n'
