#!/usr/bin/env bash
set -euo pipefail

ROOT=$(cd "$(dirname "$0")/.." && pwd)
source "$ROOT/scripts/lib/dotenv.sh"
load_dotenv_missing "$ROOT/.env"

BASE_URL="${VELOX_MASTER_URL:-http://127.0.0.1:8000}"
TOKEN="${VELOX_MASTER_ADMIN_TOKEN:-${VELOX_ADMIN_TOKEN:-}}"
PAYLOAD="$ROOT/ops/jobs/matt_damon_5_clips_docs_true.generate.json"
POLL_SECONDS="${MATT_DAMON_POLL_SECONDS:-5}"
TIMEOUT_SECONDS="${MATT_DAMON_TIMEOUT_SECONDS:-1800}"

[[ -n "$TOKEN" ]] || { echo "VELOX_MASTER_ADMIN_TOKEN or VELOX_ADMIN_TOKEN is required" >&2; exit 2; }
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
    -X POST "$BASE_URL/api/script/generate" \
    -H "Authorization: Bearer $TOKEN" \
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
      -H "Authorization: Bearer $TOKEN" \
      "$BASE_URL/api/jobs/$JOB_ID/full"
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
  and (($item.output.render.assemble_final // false) == false)
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
echo "response=$OUTPUT_RESPONSE"
