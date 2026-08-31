#!/usr/bin/env bash
set -euo pipefail

ROOT=$(cd "$(dirname "$0")/.." && pwd)
source "$ROOT/scripts/lib/dotenv.sh"
load_dotenv_missing "$ROOT/.env"

LOCAL_BASE_URL="${PIPELINEGEN_URL:-http://127.0.0.1:8000}"
MASTER_BASE_URL="${VELOX_MASTER_URL:-}"
LOCAL_TOKEN="${VELOX_PIPELINEGEN_TOKEN:-${VELOX_ADMIN_TOKEN:-}}"
MASTER_TOKEN="${VELOX_MASTER_ADMIN_TOKEN:-}"
PAYLOAD="$ROOT/ops/jobs/matt_damon_5_clips_docs_true.generate.json"
POLL_SECONDS="${MATT_DAMON_POLL_SECONDS:-5}"
TIMEOUT_SECONDS="${MATT_DAMON_TIMEOUT_SECONDS:-1800}"

[[ -n "$LOCAL_TOKEN" ]] || { echo "VELOX_PIPELINEGEN_TOKEN or VELOX_ADMIN_TOKEN is required" >&2; exit 2; }
[[ -n "$MASTER_BASE_URL" ]] || { echo "VELOX_MASTER_URL is required" >&2; exit 2; }
[[ -n "$MASTER_TOKEN" ]] || { echo "VELOX_MASTER_ADMIN_TOKEN is required for creator push" >&2; exit 2; }
[[ -f "$PAYLOAD" ]] || { echo "missing payload: $PAYLOAD" >&2; exit 2; }

RUN_ID="matt-damon-5-clips-docs-true-$(date -u +%Y%m%d-%H%M%S)-$$"
BODY=$(mktemp)
trap 'rm -f "$BODY" "$BODY.poll"' EXIT INT TERM
OUTPUT_RESPONSE="${MATT_DAMON_RESPONSE_OUTPUT:-/tmp/${RUN_ID}.json}"

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
    --data-binary "@$PAYLOAD")
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
' "$BODY.poll" >/dev/null || {
  echo "job payload does not prove docs=true / five-clip / localized-render contract" >&2
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
' "$BODY.poll" >/dev/null || {
  echo "job completed but docs/render completeness verification failed" >&2
  jq '.result.data.result | {documents,render_metrics,localized_renders}' "$BODY.poll" >&2
  exit 1
}

echo "docs=true verification passed"
echo "document=$(jq -r '(.result.data.result // .result.data.result.result // .result.data.items[0].result // .result.items[0].result // .result.output // .result).documents.it.link // (.result.data.result // .result.data.result.result // .result.data.items[0].result // .result.items[0].result // .result.output // .result).documents.it.url' "$BODY.poll")"
echo "render_metrics=$(jq -c '(.result.data.result // .result.data.result.result // .result.data.items[0].result // .result.items[0].result // .result.output // .result).render_metrics // {}' "$BODY.poll")"
echo "localized_renders=$(jq -c '(.result.data.result // .result.data.result.result // .result.data.items[0].result // .result.items[0].result // .result.output // .result).localized_renders // [] | map({scene_id,clip_id,status,duration_ms,wall_ms,drive_link})' "$BODY.poll")"
echo "timing=$(jq -c '.timing // {} | {wall_ms,execution_wall_ms,queue_wait_ms,attributed_ms,unattributed_ms,unattributed_percent,overlapped_ms,bottleneck_stage,bottleneck_operation,bottleneck_percent}' "$BODY.poll")"
echo "stages=$(jq -c '.timing.stages // []' "$BODY.poll")"
echo "critical_path=$(jq -c '.timing.critical_path // []' "$BODY.poll")"
echo "operations=$(jq -c '.timing.operations // []' "$BODY.poll")"
echo "fanout=$(jq -c '.timing.fanout // []' "$BODY.poll")"

# The 77 is a producer only. It hands the completed, link-based payload to
# the Master; final-video assembly is owned by the Master and never runs here.
MASTER_PAYLOAD=$(mktemp)
MASTER_RESPONSE=$(mktemp)
PREFETCH_ASSETS_FILE=$(mktemp)
trap 'rm -f "$BODY" "$BODY.poll" "$MASTER_PAYLOAD" "$MASTER_RESPONSE" "$PREFETCH_ASSETS_FILE"' EXIT INT TERM

# Build the eager-preparation manifest from the selected localized clips.
# Only the verified Drive locator, SHA-256 and byte size cross the machine
# boundary; local_path is deliberately never included in the Master payload.
jq -c '(.result.data.result // .result.data.result.result // .result.output // .result) | .localized_renders[]' "$BODY.poll" |
while IFS= read -r render; do
  local_path=$(jq -r '.local_path // empty' <<<"$render")
  size_bytes=0
  if [[ -n "$local_path" && -f "$local_path" ]]; then
    size_bytes=$(stat -c '%s' -- "$local_path" 2>/dev/null || echo 0)
  fi
  [[ "$size_bytes" =~ ^[1-9][0-9]*$ ]] || { echo "selected clip has no positive local size" >&2; exit 1; }
  jq -n \
    --arg asset_id "$(jq -r '.clip_id' <<<"$render")" \
    --arg url "$(jq -r '.drive_link' <<<"$render")" \
    --arg sha256 "$(jq -r '.sha256' <<<"$render")" \
    --argjson size_bytes "$size_bytes" \
    '{asset_id:$asset_id,kind:"source_clip",availability:"known",producer:"pipelinegen",url:$url,sha256:$sha256,size_bytes:$size_bytes,mime_type:"video/mp4",profile_id:"velox-h264-copy-v1",required:true,state:"ready"}' \
    >> "$PREFETCH_ASSETS_FILE"
done
PREFETCH_ASSETS_JSON=$(jq -s . "$PREFETCH_ASSETS_FILE")
jq -n \
  --arg source_provider "pipelinegen-77" \
  --arg source_job_id "${MASTER_SOURCE_JOB_ID:-$JOB_ID}" \
  --arg target_executor_id "scene.composite.v1" \
  --arg video_name "$(jq -r '(.result.data.result // .result.data.result.result // .result.output // .result).title // "PipelineGen video"' "$BODY.poll")" \
  --arg script_text "$(jq -r '(.result.data.result // .result.data.result.result // .result.output // .result).output.text // ""' "$BODY.poll")" \
  --arg voiceover "$(jq -r '(.result.data.result // .result.data.result.result // .result.output // .result).final_audio.drive_link // ""' "$BODY.poll")" \
  --arg timeline_hash "pipelinegen:${MASTER_SOURCE_JOB_ID:-$JOB_ID}:1" \
  --argjson prefetch_assets "$PREFETCH_ASSETS_JSON" \
  --slurpfile result "$BODY.poll" '
    ($result[0].result.data.result // $result[0].result.data.result.result // $result[0].result.output // $result[0].result) as $r
    | {
        source_provider: $source_provider,
        source_job_id: $source_job_id,
        target_executor_id: $target_executor_id,
        assembly: {
          send_to_velox: true,
          timeline_revision: 1,
          timeline_hash: $timeline_hash,
          output_profile: "velox-h264-copy-v1",
          assets: $prefetch_assets,
          timeline: ($prefetch_assets | map({scene_id: .asset_id, asset_id: .asset_id}))
        },
        payload: {
          status: "completed",
          pipeline: "scene.composite.v1",
          pipeline_id: "clips.v1",
          copy_only: true,
          job_id: $source_job_id,
          video_name: $video_name,
          script_text: $script_text,
          voiceover_paths: (if $voiceover == "" then [] else [$voiceover] end),
          delivery_plan: [{destination_id: "drive-production", priority: 1, retry_budget: 3}],
          scenes: ($r.scenes // [] | map({
            text: (.text.it // .text.en // ((.text // {}) | to_entries[0].value) // ""),
            scene_id: (.id // ""),
            index: (.index // 0),
            clip_link: (.clip.drive_link // .clips[0].drive_link // ""),
            duration_seconds: (((.duration_ms // 0) / 1000) | if . < 0.1 then 0.1 else . end)
          }))
        }
      }' > "$MASTER_PAYLOAD"

MASTER_HTTP=$(curl -sS --max-time 30 -o "$MASTER_RESPONSE" -w '%{http_code}' \
  -X POST "$MASTER_BASE_URL/api/v1/creator/jobs" \
  -H "Authorization: Bearer $MASTER_TOKEN" \
  -H 'Content-Type: application/json' \
  --data-binary "@$MASTER_PAYLOAD")
[[ "$MASTER_HTTP" == "202" ]] || { echo "master creator push failed: http=$MASTER_HTTP" >&2; cat "$MASTER_RESPONSE" >&2; exit 1; }
echo "master_push=$(jq -c '{job_id,status,accepted_from,dispatch_status}' "$MASTER_RESPONSE")"
echo "response=$OUTPUT_RESPONSE"
