#!/usr/bin/env bash
# Render one canonical clip through POST /api/clips/render and publish it to
# a Drive folder. Subtitles are intentionally omitted from the payload.
set -Eeuo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
API_BASE="${VELOX_BASE_URL:-http://127.0.0.1:8000}"
SOURCE_ASSET_ID="${SOURCE_ASSET_ID:-yt_2JFBX65Tsnc_30_40_v1}"
WATERMARK_ASSET_ID="${WATERMARK_ASSET_ID:-9cc03bef0eeefa96d5ec39a63d1d1199}"
DRIVE_FOLDER_ID="${DRIVE_FOLDER_ID:-1ST6FxPuRaxwBOIz39MAN8Jj4gDv509-K}"
PAYLOAD_FILE="${PAYLOAD_FILE:-$(mktemp)}"
RESPONSE_FILE="${RESPONSE_FILE:-$(mktemp)}"
trap 'rm -f "$PAYLOAD_FILE" "$RESPONSE_FILE"' EXIT

command -v jq >/dev/null || { echo "jq is required" >&2; exit 2; }

jq -n \
  --arg source "$SOURCE_ASSET_ID" \
  --arg watermark "$WATERMARK_ASSET_ID" \
  --arg folder "$DRIVE_FOLDER_ID" \
  '{
    source_asset_id: $source,
    background: {mode: "blur_source"},
    watermark: {
      enabled: true,
      asset_id: $watermark,
      position: "top_right",
      opacity: 0.85,
      margin_px: 40
    },
    transcript: {mode: "reuse_or_generate", language: "en", persist: true},
    # Shorts stock preset is 1080x1920@30; keep the existing H.264/AAC
    # contract and avoid the destructive 60-FPS up-conversion.
    output: {contract: "velox-editing-clip-v1", width: 1080, height: 1920, fps: 30},
    audio: {mode: "copy_if_compatible"},
    destination: {drive_folder_id: $folder}
  }' >"$PAYLOAD_FILE"

echo "Payload clip render (watermark sì, subtitles omessi):"
jq . "$PAYLOAD_FILE"

"$ROOT/scripts/with-velox-auth" bash -c \
  'curl -fsS --max-time 30 -X POST "$1/api/clips/render" \
    -H "Authorization: Bearer ${VELOX_ADMIN_TOKEN}" \
    -H "Content-Type: application/json" \
    --data-binary @"$2"' bash "$API_BASE" "$PAYLOAD_FILE" >"$RESPONSE_FILE"

JOB_ID="$(jq -r '.job_id // empty' "$RESPONSE_FILE")"
[[ -n "$JOB_ID" ]] || { cat "$RESPONSE_FILE" >&2; exit 1; }
echo "Job: $JOB_ID"

for attempt in $(seq 1 "${POLL_LIMIT:-180}"); do
  FULL_FILE="$(mktemp)"
  "$ROOT/scripts/with-velox-auth" bash -c \
    'curl -fsS --max-time 30 -H "Authorization: Bearer ${VELOX_ADMIN_TOKEN}" "$1/api/jobs/$2/full"' \
    bash "$API_BASE" "$JOB_ID" >"$FULL_FILE"
  STATUS="$(jq -r '.status // .job.status // empty' "$FULL_FILE")"
  echo "[$attempt] $STATUS"
  case "$STATUS" in
    SUCCEEDED|SUCCEEDED_WITH_WARNINGS|COMPLETED)
      jq . "$FULL_FILE"
      rm -f "$FULL_FILE"
      exit 0
      ;;
    FAILED|CANCELLED|DEAD_LETTER|dead_letter)
      jq . "$FULL_FILE" >&2
      rm -f "$FULL_FILE"
      exit 1
      ;;
  esac
  rm -f "$FULL_FILE"
  sleep 2
done

echo "Timeout polling job $JOB_ID" >&2
exit 124
