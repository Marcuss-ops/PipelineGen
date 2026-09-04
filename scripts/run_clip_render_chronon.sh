#!/usr/bin/env bash
# Submit one canonical clip recreation through PipelineGen's clip endpoint.
#
# Pipeline:
#   POST /api/clips/render -> PipelineGen Master -> RenderingGen queue
#   -> RenderingGen worker -> Chronon3d -> certified artifact -> PipelineGen
#
# This surface intentionally does not invoke the local Rust executor. Rust
# remains available for unrelated capabilities; clip recreation is accepted
# only when the completed result reports the Chronon Vulkan backend.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
source "$ROOT/scripts/lib/dotenv.sh"
load_dotenv_missing "$ROOT/.env"

SOURCE_ASSET_ID="${1:-${CLIP_SOURCE_ASSET_ID:-}}"
PAYLOAD_FILE="${2:-${CLIP_RENDER_PAYLOAD:-}}"
BASE_URL="${PIPELINEGEN_URL:-http://127.0.0.1:8000}"
TOKEN="${VELOX_ADMIN_TOKEN:-${VELOX_PIPELINEGEN_TOKEN:-}}"
DRIVE_FOLDER_ID="${CLIP_RENDER_DRIVE_FOLDER_ID:-1ST6FxPuRaxwBOIz39MAN8Jj4gDv509-K}"
# Optional script/batch subfolder under DRIVE_FOLDER_ID (e.g. the script
# title). When set, the worker resolves ROOT/<name>/ create-or-reuse ONCE per
# job; all clips of the same batch carry the same value and land together in
# one Drive folder. Empty = publish directly into DRIVE_FOLDER_ID (legacy).
SUBFOLDER_NAME="${CLIP_RENDER_DRIVE_SUBFOLDER:-}"
POLL_SECONDS="${CLIP_RENDER_POLL_SECONDS:-2}"
TIMEOUT_SECONDS="${CLIP_RENDER_TIMEOUT_SECONDS:-900}"

command -v curl >/dev/null || { echo "ERROR: curl is required" >&2; exit 2; }
command -v jq >/dev/null || { echo "ERROR: jq is required" >&2; exit 2; }
[[ -n "$TOKEN" ]] || { echo "ERROR: VELOX_ADMIN_TOKEN is required" >&2; exit 2; }
[[ -n "$SOURCE_ASSET_ID" || -n "$PAYLOAD_FILE" ]] || {
  echo "usage: $0 SOURCE_ASSET_ID [PAYLOAD.json]" >&2
  exit 2
}
if [[ -n "$PAYLOAD_FILE" && ! -f "$PAYLOAD_FILE" ]]; then
  echo "ERROR: payload not found: $PAYLOAD_FILE" >&2
  exit 2
fi

WORK_DIR="$(mktemp -d "${TMPDIR:-/tmp}/pipelinegen-clip-render.XXXXXX")"
trap 'rm -rf "$WORK_DIR"' EXIT INT TERM
PAYLOAD="$WORK_DIR/request.json"
RESPONSE="$WORK_DIR/response.json"
FULL="$WORK_DIR/full.json"

if [[ -n "$PAYLOAD_FILE" ]]; then
  jq -e 'type == "object" and (.source_asset_id | type == "string" and length > 0)' \
    "$PAYLOAD_FILE" > "$PAYLOAD"
else
  jq -n --arg source "$SOURCE_ASSET_ID" '
    {
      source_asset_id: $source,
      transcript: {mode: "reuse_or_generate", language: "en", persist: true},
      subtitles: {enabled: true, mode: "burn", style_id: "shorts-v1"},
      watermark: {
        enabled: true,
        text: "VELOX • CHRONON",
        position: "top_right",
        opacity: 1,
        margin_px: 40
      },
      background: {mode: "none"},
      audio: {mode: "copy_if_compatible"},
      destination: ({drive_folder_id: $folder} + (if ($subfolder // "") == "" then {} else {subfolder_name: $subfolder} end)),
      output: {contract: "VELOX_ASSEMBLY_READY_V1"},
      execution: {require_gpu: true}
    }
  ' --arg folder "$DRIVE_FOLDER_ID" --arg subfolder "$SUBFOLDER_NAME" > "$PAYLOAD"
fi

RUN_ID="clip-chronon-$(date -u +%Y%m%d-%H%M%S)-$$"
HTTP="$(curl -sS --max-time 30 -o "$RESPONSE" -w '%{http_code}' \
  -X POST "$BASE_URL/api/clips/render" \
  -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' \
  -H "X-Request-ID: ${RUN_ID}-request" \
  -H "Idempotency-Key: ${RUN_ID}-request" \
  --data-binary @"$PAYLOAD")"

if [[ "$HTTP" != 202 ]]; then
  echo "ERROR: PipelineGen clip endpoint returned HTTP $HTTP" >&2
  jq . "$RESPONSE" 2>/dev/null || cat "$RESPONSE" >&2
  exit 1
fi

JOB_ID="$(jq -r '.job_id // empty' "$RESPONSE")"
[[ -n "$JOB_ID" ]] || { echo "ERROR: endpoint returned no job_id" >&2; exit 1; }
echo "PipelineGen queued clip job: $JOB_ID"

DEADLINE=$(( $(date +%s) + TIMEOUT_SECONDS ))
while :; do
  POLL_HTTP="$(curl -sS --max-time 30 -o "$FULL" -w '%{http_code}' \
    "$BASE_URL/api/jobs/$JOB_ID/full" \
    -H "Authorization: Bearer $TOKEN")"
  [[ "$POLL_HTTP" == 200 ]] || {
    echo "ERROR: polling job $JOB_ID returned HTTP $POLL_HTTP" >&2
    cat "$FULL" >&2
    exit 1
  }
  STATUS="$(jq -r '(.status // .job.status // .current_step // "UNKNOWN") | ascii_upcase' "$FULL")"
  echo "[$(date '+%H:%M:%S')] $STATUS"
  case "$STATUS" in
    SUCCEEDED|COMPLETED|SUCCEEDED_WITH_WARNINGS) break ;;
    FAILED|ERROR|CANCELLED)
      echo "ERROR: PipelineGen clip job failed" >&2
      jq '{status, error, result: (.result // .job.result)}' "$FULL" >&2
      exit 1
      ;;
  esac
  (( $(date +%s) < DEADLINE )) || { echo "ERROR: timeout waiting for $JOB_ID" >&2; exit 1; }
  sleep "$POLL_SECONDS"
done

# The result is the evidence boundary: a successful response is not enough.
# Require the backend identity emitted by the RenderingGen/Chronon artifact.
BACKEND="$(jq -r '(.result.render.backend // .job.result.render.backend // .result.data.result.render.backend // "")' "$FULL")"
if [[ "$BACKEND" != "chronon_vulkan" ]]; then
  echo "ERROR: clip did not complete through Chronon Vulkan (backend=$BACKEND)" >&2
  exit 1
fi

echo "PASS: PipelineGen -> RenderingGen -> Chronon3d"
echo "job_id=$JOB_ID backend=$BACKEND"
jq '{job_id: (.id // .job.id), status: (.status // .job.status), render: (.result.render // .job.result.render), asset: (.result.asset // .job.result.asset)}' "$FULL"
