#!/usr/bin/env bash
set -euo pipefail

ROOT=$(cd "$(dirname "$0")/.." && pwd)
source "$ROOT/scripts/lib/dotenv.sh"
load_dotenv_missing "$ROOT/.env"

LOCAL_BASE_URL="${PIPELINEGEN_URL:-http://127.0.0.1:8000}"
LOCAL_TOKEN="${VELOX_ADMIN_TOKEN:-${VELOX_PIPELINEGEN_TOKEN:-}}"
PAYLOAD="$ROOT/ops/jobs/matt_damon_5_clips.generate.json"
POLL_SECONDS=3
TIMEOUT_SECONDS=600

[[ -n "$LOCAL_TOKEN" ]] || { echo "VELOX_ADMIN_TOKEN is required" >&2; exit 2; }
[[ -f "$PAYLOAD" ]] || { echo "missing payload: $PAYLOAD" >&2; exit 2; }

RUN_ID="matt-damon-5-clips-$(date -u +%Y%m%d-%H%M%S)-$$"
BODY=$(mktemp)
trap 'rm -f "$BODY" "$BODY.poll"' EXIT INT TERM

echo "Submitting 5 Matt Damon clips verification job to $LOCAL_BASE_URL/api/script/generate..."
START_TIME=$(date +%s%N)

HTTP=$(curl -sS --max-time 30 -o "$BODY" -w '%{http_code}' \
  -X POST "$LOCAL_BASE_URL/api/script/generate" \
  -H "Authorization: Bearer $LOCAL_TOKEN" \
  -H 'Content-Type: application/json' \
  -H "X-Request-ID: ${RUN_ID}-request" \
  -H "Idempotency-Key: ${RUN_ID}-request" \
  --data-binary "@$PAYLOAD")

echo "HTTP Code: $HTTP"
[[ "$HTTP" == "200" || "$HTTP" == "202" ]] || { cat "$BODY" >&2; exit 1; }

JOB_ID=$(jq -r '.job_id // .id // empty' "$BODY")
echo "Job ID: $JOB_ID"

deadline=$(( $(date +%s) + TIMEOUT_SECONDS ))
while :; do
  curl -sS --max-time 30 -o "$BODY.poll" \
    -H "Authorization: Bearer $LOCAL_TOKEN" \
    "$LOCAL_BASE_URL/api/jobs/$JOB_ID/full"
  
  status=$(jq -r '.status // .job.status // .result.status // empty' "$BODY.poll")
  echo "[$(date '+%H:%M:%S')] Status: $status"
  case "${status^^}" in
    SUCCEEDED|COMPLETED|SUCCEEDED_WITH_WARNINGS)
      END_TIME=$(date +%s%N)
      DURATION_SEC=$(echo "scale=2; ($END_TIME - $START_TIME)/1000000000" | bc)
      echo "=== JOB SUCCEEDED in ${DURATION_SEC}s ==="
      echo "--- Summary ---"
      jq '{
        job_id: (.id // .job.id),
        status: (.status // .job.status),
        total_wall_ms: .timing.wall_ms,
        stages: .timing.stages,
        documents: (.result.data.result.documents // .result.documents),
        render_metrics: (.result.data.result.render_metrics // .result.render_metrics),
        localized_renders: (.result.data.result.localized_renders // .result.localized_renders)
      }' "$BODY.poll"
      exit 0
      ;;
    FAILED|ERROR|CANCELLED)
      echo "=== JOB FAILED ===" >&2
      jq '{
        error: (.error // .job.error),
        timeline: (.timeline // .job.timeline)
      }' "$BODY.poll" >&2
      exit 1
      ;;
  esac
  (( $(date +%s) < deadline )) || { echo "Poll timeout" >&2; exit 1; }
  sleep "$POLL_SECONDS"
done
