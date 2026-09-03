#!/usr/bin/env bash
set -euo pipefail

ROOT=$(cd "$(dirname "$0")/.." && pwd)
source "$ROOT/scripts/lib/dotenv.sh"
load_dotenv_missing "$ROOT/.env"

LOCAL_BASE_URL="${PIPELINEGEN_URL:-http://127.0.0.1:8000}"
LOCAL_TOKEN="${VELOX_PIPELINEGEN_TOKEN:-${VELOX_ADMIN_TOKEN:-}}"
PAYLOAD="$ROOT/ops/jobs/matt_damon_5_clips_docs_true.generate.json"
DRIVE_FOLDER_ID="${MATT_DAMON_DRIVE_FOLDER_ID:-1UPya0b647sLs-7NPLYjIBnZwqDSsFZNK}"
DRIVE_SUBFOLDER_NAME="${MATT_DAMON_DRIVE_SUBFOLDER_NAME:-matt-damon-5-clips-$(date -u +%Y%m%d-%H%M%S)-$$}"
POLL_SECONDS="${MATT_DAMON_POLL_SECONDS:-5}"
TIMEOUT_SECONDS="${MATT_DAMON_TIMEOUT_SECONDS:-1800}"

[[ -n "$LOCAL_TOKEN" ]] || { echo "VELOX_PIPELINEGEN_TOKEN or VELOX_ADMIN_TOKEN is required" >&2; exit 2; }
[[ -f "$PAYLOAD" ]] || { echo "missing payload: $PAYLOAD" >&2; exit 2; }

RUN_ID="matt-damon-5-clips-docs-true-$(date -u +%Y%m%d-%H%M%S)-$$"
BODY=$(mktemp)
REQUEST_PAYLOAD=$(mktemp)
trap 'rm -f "$BODY" "$BODY.poll" "$REQUEST_PAYLOAD"' EXIT INT TERM
OUTPUT_RESPONSE="${MATT_DAMON_RESPONSE_OUTPUT:-/tmp/${RUN_ID}.json}"

# Drive subfolder and Docs are created by the real PipelineGen endpoint.
# This only specializes the request, keeping every output link run-specific.
jq --arg folder "$DRIVE_FOLDER_ID" --arg subfolder "$DRIVE_SUBFOLDER_NAME" \
  '.items[0].docs.folder_id = $folder
   | .items[0].output.render.drive_folder_id = $folder
   | .items[0].output.render.drive_subfolder_name = $subfolder' \
  "$PAYLOAD" > "$REQUEST_PAYLOAD"

if [[ -n "${MATT_DAMON_RESPONSE_FILE:-}" ]]; then
  cp "$MATT_DAMON_RESPONSE_FILE" "$BODY.poll"
  JOB_ID=$(jq -r '.id // .job.id // "replayed-response"' "$BODY.poll")
  echo "replaying_response=$MATT_DAMON_RESPONSE_FILE"
else
  HTTP=$(curl -sS --max-time 30 -o "$BODY" -w '%{http_code}' \
    -X POST "$LOCAL_BASE_URL/api/script/generate" \
    -H "Authorization: Bearer $LOCAL_TOKEN" \
    -H 'Content-Type: application/json' \
    -H "X-Request-ID: ${RUN_ID}-request" \
    -H "Idempotency-Key: ${RUN_ID}-request" \
    --data-binary "@$REQUEST_PAYLOAD")
  [[ "$HTTP" == "200" || "$HTTP" == "202" ]] || { cat "$BODY" >&2; exit 1; }
  JOB_ID=$(jq -r '.job_id // .id // empty' "$BODY")
  [[ -n "$JOB_ID" ]] || { cat "$BODY" >&2; echo "response did not contain job_id" >&2; exit 1; }
  echo "job_id=$JOB_ID"

  deadline=$(( $(date +%s) + TIMEOUT_SECONDS ))
  while :; do
    curl -sS --max-time 30 -o "$BODY.poll" \
      -H "Authorization: Bearer $LOCAL_TOKEN" \
      "$LOCAL_BASE_URL/api/jobs/$JOB_ID/full"
    status=$(jq -r '.status // .job.status // .result.status // empty' "$BODY.poll")
    echo "status=$status"
    case "${status^^}" in
      SUCCEEDED|COMPLETED|SUCCEEDED_WITH_WARNINGS)
        break
        ;;
      FAILED|ERROR|CANCELLED)
        cat "$BODY.poll" >&2
        exit 1
        ;;
    esac
    (( $(date +%s) < deadline )) || { cat "$BODY.poll" >&2; echo "poll timeout" >&2; exit 1; }
    sleep "$POLL_SECONDS"
  done
fi

cp "$BODY.poll" "$OUTPUT_RESPONSE"

jq -e '(.status // .job.status // "") | ascii_upcase | test("SUCCEEDED|COMPLETED")' "$BODY.poll" >/dev/null || {
  echo "response is not terminal-success" >&2; jq '.status // .job.status' "$BODY.poll" >&2; exit 1;
}

jq -e '
  (.job.payload.items[0]) as $item
  | ($item.docs.enabled == true)
  and (($item.source.clip_ids // []) | length == 5)
  and ($item.output.render.enabled == true)
  and (($item.output.render.drive_folder_id // "") | length > 0)
  and ($item.output.render.require_gpu == true)
  and (($item.output.render.render_concurrency // 0) >= 8)
  and ($item.output.render.watermark.enabled == true)
  and (($item.output.render.watermark.text // "") | length > 0)
  and ($item.output.render.watermark.position == "top_right")
  and ($item.output.render.watermark.style.transition_in.preset == "fade_in")
  and ($item.output.render.subtitles.enabled == true)
  and ($item.output.render.subtitles.mode == "burn")
  and ($item.output.render.subtitles.style_id == "shorts-v1-40-shadow")
  and ($item.output.render.subtitles.style.position == "bottom_center")
  and ($item.output.render.subtitles.style.transition_in.preset == "fade_in")
' "$BODY.poll" >/dev/null || {
  echo "job payload does not prove docs/render/GPU/subtitle/watermark contract" >&2
  jq '.job.payload.items[0] | {docs,source,render:.output.render}' "$BODY.poll" >&2
  exit 1
}

jq -e '
  (.result.data.result // .result.data.result.result // .result.data.items[0].result // .result.items[0].result // .result.output // .result) as $r
  | (($r.documents.it.link // $r.documents.it.url // "") | length > 0)
  and (($r.render_metrics.expected // 0) == 5)
  and (($r.render_metrics.successful // 0) == 5)
  and (($r.render_metrics.failed // -1) == 0)
  and (($r.localized_renders // []) | length == 5)
  and (($r.localized_renders // []) | all(.backend == "chronon_vulkan"))
' "$BODY.poll" >/dev/null || {
  echo "job completed but docs/render/Chronon Vulkan completeness verification failed" >&2
  jq '.result.data.result | {documents,render_metrics,localized_renders}' "$BODY.poll" >&2
  exit 1
}

echo "docs=true verification passed"
echo "document=$(jq -r '(.result.data.result // .result.data.result.result // .result.data.items[0].result // .result.items[0].result // .result.output // .result).documents.it.link // (.result.data.result // .result.data.result.result // .result.data.items[0].result // .result.items[0].result // .result.output // .result).documents.it.url' "$BODY.poll")"
echo "render_metrics=$(jq -c '(.result.data.result // .result.data.result.result // .result.data.items[0].result // .result.items[0].result // .result.output // .result).render_metrics // {}' "$BODY.poll")"
echo "localized_renders=$(jq -c '(.result.data.result // .result.data.result.result // .result.data.items[0].result // .result.items[0].result // .result.output // .result).localized_renders // [] | map({scene_id,clip_id,status,duration_ms,wall_ms,drive_link})' "$BODY.poll")"
echo "chronon_backends=$(jq -c '(.result.data.result // .result.data.result.result // .result.data.items[0].result // .result.items[0].result // .result.output // .result).localized_renders // [] | map(.backend)' "$BODY.poll")"
echo "timing=$(jq -c '.timing // {} | {wall_ms,execution_wall_ms,queue_wait_ms,attributed_ms,unattributed_ms,unattributed_percent,overlapped_ms,bottleneck_stage,bottleneck_operation,bottleneck_percent}' "$BODY.poll")"
echo "stages=$(jq -c '.timing.stages // []' "$BODY.poll")"
echo "critical_path=$(jq -c '.timing.critical_path // []' "$BODY.poll")"
echo "operations=$(jq -c '.timing.operations // []' "$BODY.poll")"
echo "fanout=$(jq -c '.timing.fanout // []' "$BODY.poll")"

echo "drive_folder_id=$DRIVE_FOLDER_ID"
echo "drive_subfolder_name=$DRIVE_SUBFOLDER_NAME"
echo "response=$OUTPUT_RESPONSE"
