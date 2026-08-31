#!/usr/bin/env bash
# yt_clip_extract_smoke_RRJvrDKunyA.sh — YouTube clips extraction smoke.
#
# Purpose:
#   Submit the RRJvrDKunyA Pacquiao/Broner clip batch to /api/clips/process,
#   force all 8 clips into one Drive subfolder, then verify SQLite + indexing
#   metadata for the resulting media_assets rows.
#
# This is a clips smoke, not a stock-pipeline smoke. Do not reuse the old
# stock fixtures here.
#
# Usage:
#   SMOKE_DRIVE_FOLDER_ID=<real-drive-folder-id> \
#   VELOX_ADMIN_TOKEN=<token> \
#   ./tests/operational/yt_clip_extract_smoke_RRJvrDKunyA.sh
#
# Optional:
#   API_BASE=http://127.0.0.1:8000
#   TOKEN_FILE=.env
#   SMOKE_TIMEOUT_SECONDS=300
#   SMOKE_POLL_TIMEOUT_SECONDS=180
#
set -euo pipefail

DIR=$(cd "$(dirname "$0")" && pwd)
# shellcheck disable=SC1091
source "$DIR/lib/common.sh"

smoke_require curl sqlite3 yt-dlp ffmpeg ffprobe

if [[ "${1:-}" == "-h" || "${1:-}" == "--help" ]]; then
    sed -n '1,60p' "$0"
    exit 0
fi

if [[ "$DRY_RUN" == "1" ]]; then
    smoke_log_section "DRY RUN — RRJvrDKunyA clip smoke"
    printf '  fixture: %s\n' "$DIR/../fixtures/clip_batch_rrjvrdkunyA/payload.json"
    printf '  endpoint: http://%s/api/clips/process\n' "$SMOKE_API_BASE"
    printf '  destination subfolder: Manny Pacquiao vs Adrien Broner\n'
    printf '  expected clip count: 8\n'
    exit 0
fi

if [[ -z "${SMOKE_DRIVE_FOLDER_ID:-}" ]]; then
    printf '%ssetup error: SMOKE_DRIVE_FOLDER_ID is required for destination.kind=explicit%s\n' \
        "$RED" "$RESET" >&2
    exit 2
fi

FIXTURE="$DIR/../fixtures/clip_batch_rrjvrdkunyA/payload.json"
if [[ ! -f "$FIXTURE" ]]; then
    printf '%ssetup error: fixture missing: %s%s\n' "$RED" "$FIXTURE" "$RESET" >&2
    exit 2
fi

SUBFOLDER_NAME="Manny Pacquiao vs Adrien Broner"
CLIP_PREFIX="yt_RRJvrDKunyA_"
DB_PATH="${DB_PATH:?DB_PATH must be explicitly set to an isolated or approved database}"

payload=$(jq --arg folder_id "$SMOKE_DRIVE_FOLDER_ID" '
    .destination.folder_id = $folder_id
    | {
        url: .url,
        segments: .segments,
        force_keyframes: .force_keyframes,
        normalize: .normalize,
        keep_audio: .keep_audio,
        strategy: .strategy,
        concurrency: .concurrency,
        destination: .destination
    }
' "$FIXTURE")

smoke_log_section "Preflight"
printf '  API base         = %s\n' "$SMOKE_API_BASE"
printf '  Drive folder ID  = %s\n' "$SMOKE_DRIVE_FOLDER_ID"
printf '  Subfolder name   = %s\n' "$SUBFOLDER_NAME"
printf '  Payload fixture  = %s\n' "$FIXTURE"
printf '  DB path          = %s\n' "$DB_PATH"

smoke_log_section "Submit clip batch"
smoke_curl POST "/api/clips/process" --data "$payload" >/dev/null
if ! smoke_assert_http_2xx "POST /api/clips/process"; then
    exit 1
fi

JOB_ID=$(jq -r '.job_id // .id // empty' "$SMOKE_LAST_BODY" 2>/dev/null || echo "")
if [[ -z "$JOB_ID" || "$JOB_ID" == "null" ]]; then
    JOB_ID=$(sqlite3 "$DB_PATH" \
        "SELECT id
         FROM jobs
         WHERE type='youtube_clip.extract'
           AND json_extract(payload_json, '$.url') = 'https://www.youtube.com/watch?v=RRJvrDKunyA'
           AND json_extract(payload_json, '$.destination.folder_id') = '${SMOKE_DRIVE_FOLDER_ID}'
           AND json_extract(payload_json, '$.segments[0].start') = '00:00:32'
           AND json_extract(payload_json, '$.segments[0].end') = '00:00:37'
         ORDER BY created_at DESC
         LIMIT 1;" 2>/dev/null || echo "")
fi
if [[ -z "$JOB_ID" ]]; then
    printf '%sFAIL: POST /api/clips/process returned no job_id%s\n' "$RED" "$RESET" >&2
    cat "$SMOKE_LAST_BODY" >&2 || true
    exit 1
fi
printf '  enqueued job_id = %s\n' "$JOB_ID"

smoke_log_section "Poll job to terminal"
if ! smoke_poll_terminal "$JOB_ID"; then
    printf '%sFAIL: job did not reach terminal state within timeout%s\n' "$RED" "$RESET" >&2
    exit 1
fi
printf '  terminal status = %s\n' "${SMOKE_LAST_STATUS:-?}"
case "${SMOKE_LAST_STATUS:-}" in
    completed|SUCCEEDED)
        ;;
    *)
        printf '%sFAIL: job terminal status = %s (expected completed or SUCCEEDED)%s\n' \
            "$RED" "${SMOKE_LAST_STATUS:-?}" "$RESET" >&2
        exit 1
        ;;
esac

smoke_log_section "Wait for indexing"
deadline=$(( $(date +%s) + ${SMOKE_POLL_TIMEOUT_SECONDS:-180} ))
indexed=""
while (( $(date +%s) < deadline )); do
    indexed=$(sqlite3 "$DB_PATH" \
        "SELECT COUNT(*) FROM media_assets WHERE id GLOB '${CLIP_PREFIX}*' AND folder_id = '${SMOKE_DRIVE_FOLDER_ID}' AND folder_path = '${SUBFOLDER_NAME}' AND index_state = 'INDEXED';" 2>/dev/null || echo "0")
    indexed="${indexed:-0}"
    if [[ "$indexed" -eq 8 ]]; then
        break
    fi
    sleep 2
done
if [[ "$indexed" -ne 8 ]]; then
    printf '%sFAIL: indexed clip count = %s (expected 8)%s\n' "$RED" "$indexed" "$RESET" >&2
    exit 1
fi
printf '  indexed clips = %s\n' "$indexed"

smoke_log_section "Verify SQLite metadata"
total_rows=$(sqlite3 "$DB_PATH" \
    "SELECT COUNT(*) FROM media_assets WHERE id GLOB '${CLIP_PREFIX}*' AND folder_id = '${SMOKE_DRIVE_FOLDER_ID}' AND folder_path = '${SUBFOLDER_NAME}';" 2>/dev/null || echo "0")
folder_count=$(sqlite3 "$DB_PATH" \
    "SELECT COUNT(DISTINCT folder_path) FROM media_assets WHERE id GLOB '${CLIP_PREFIX}*' AND folder_id = '${SMOKE_DRIVE_FOLDER_ID}' AND folder_path = '${SUBFOLDER_NAME}';" 2>/dev/null || echo "0")
same_folder=$(sqlite3 "$DB_PATH" \
    "SELECT COUNT(*) FROM media_assets WHERE id GLOB '${CLIP_PREFIX}*' AND folder_id = '${SMOKE_DRIVE_FOLDER_ID}' AND folder_path = '${SUBFOLDER_NAME}';" 2>/dev/null || echo "0")
meta_ok=$(sqlite3 "$DB_PATH" \
    "SELECT COUNT(*) FROM media_assets WHERE id GLOB '${CLIP_PREFIX}*' AND folder_id = '${SMOKE_DRIVE_FOLDER_ID}' AND folder_path = '${SUBFOLDER_NAME}' AND source = 'youtube' AND json_extract(metadata_json, '$.summary') != '' AND json_extract(metadata_json, '$.topics') IS NOT NULL AND json_extract(metadata_json, '$.speakers') IS NOT NULL AND json_extract(metadata_json, '$.hook') != '' AND json_extract(metadata_json, '$.search_visibility') != '';" 2>/dev/null || echo "0")
outbox_ok=$(sqlite3 "$DB_PATH" \
    "SELECT COUNT(*) FROM outbox_events WHERE event_type = 'asset.index.requested' AND aggregate_id IN (SELECT id FROM media_assets WHERE id GLOB '${CLIP_PREFIX}*' AND folder_id = '${SMOKE_DRIVE_FOLDER_ID}' AND folder_path = '${SUBFOLDER_NAME}');" 2>/dev/null || echo "0")

printf '  rows with prefix           = %s\n' "$total_rows"
printf '  distinct folder_path count  = %s\n' "$folder_count"
printf '  rows in target subfolder    = %s\n' "$same_folder"
printf '  rows with rich metadata     = %s\n' "$meta_ok"
printf '  outbox asset.index.requested = %s\n' "$outbox_ok"

if [[ "$total_rows" -ne 8 ]]; then
    printf '%sFAIL: expected 8 media_assets rows, got %s%s\n' "$RED" "$total_rows" "$RESET" >&2
    exit 1
fi
if [[ "$folder_count" -ne 1 ]]; then
    printf '%sFAIL: expected a single folder_path, got %s%s\n' "$RED" "$folder_count" "$RESET" >&2
    exit 1
fi
if [[ "$same_folder" -ne 8 ]]; then
    printf '%sFAIL: expected 8 rows in subfolder %s, got %s%s\n' \
        "$RED" "$SUBFOLDER_NAME" "$same_folder" "$RESET" >&2
    exit 1
fi
if [[ "$meta_ok" -ne 8 ]]; then
    printf '%sFAIL: expected rich metadata on all 8 rows, got %s%s\n' "$RED" "$meta_ok" "$RESET" >&2
    exit 1
fi
if [[ "$outbox_ok" -lt 8 ]]; then
    printf '%sFAIL: expected at least 8 index outbox events, got %s%s\n' "$RED" "$outbox_ok" "$RESET" >&2
    exit 1
fi

smoke_log_section "Verify API listing"
smoke_curl GET "/api/media/clips/youtube/folders/${SMOKE_DRIVE_FOLDER_ID}/children" >/dev/null
if ! smoke_assert_http_2xx "GET /api/media/clips/youtube/folders/${SMOKE_DRIVE_FOLDER_ID}/children"; then
    exit 1
fi
api_count=$(jq -r '.children | length' "$SMOKE_LAST_BODY")
api_wrong_prefix=$(jq -r '[.children[] | select((.id // "") | startswith("yt_RRJvrDKunyA_") | not)] | length' "$SMOKE_LAST_BODY")
printf '  api children count    = %s\n' "$api_count"
printf '  api non-boxing rows   = %s\n' "$api_wrong_prefix"
if [[ "$api_count" -ne 8 ]]; then
    printf '%sFAIL: expected 8 API children in the target folder, got %s%s\n' "$RED" "$api_count" "$RESET" >&2
    exit 1
fi
if [[ "$api_wrong_prefix" -ne 0 ]]; then
    printf '%sFAIL: API listing contains non-boxing children (%s)%s\n' "$RED" "$api_wrong_prefix" "$RESET" >&2
    exit 1
fi

smoke_log_section "Success"
printf '  clip batch       = RRJvrDKunyA\n'
printf '  job_id           = %s\n' "$JOB_ID"
printf '  target subfolder = %s\n' "$SUBFOLDER_NAME"
printf '  verified rows    = 8\n'
printf '  rich metadata    = ok\n'
printf '  indexing         = ok\n'
