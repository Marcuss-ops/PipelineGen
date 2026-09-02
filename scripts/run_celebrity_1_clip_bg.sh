#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
source "$ROOT/scripts/lib/dotenv.sh"
load_dotenv_missing "$ROOT/.env"

LOCAL_BASE_URL="${PIPELINEGEN_URL:-http://127.0.0.1:8000}"
LOCAL_TOKEN="${VELOX_ADMIN_TOKEN:-${VELOX_PIPELINEGEN_TOKEN:-}}"
PAYLOAD="$ROOT/ops/jobs/celebrity_1_clip_bg_test.generate.json"
POLL_SECONDS=2
TIMEOUT_SECONDS=300

[[ -n "$LOCAL_TOKEN" ]] || { echo "VELOX_ADMIN_TOKEN is required" >&2; exit 2; }
[[ -f "$PAYLOAD" ]] || { echo "missing payload: $PAYLOAD" >&2; exit 2; }

RUN_ID="celebrity-1-clip-bg-$(date -u +%Y%m%d-%H%M%S)-$$"
BODY=$(mktemp)
trap 'rm -f "$BODY" "$BODY.poll"' EXIT INT TERM

echo "Submitting 1-clip background test job to $LOCAL_BASE_URL/api/script/generate..."
START_TIME=$(date +%s%N)

HTTP=$(curl -sS --max-time 30 -o "$BODY" -w '%{http_code}' \
  -X POST "$LOCAL_BASE_URL/api/script/generate" \
  -H "Authorization: Bearer $LOCAL_TOKEN" \
  -H 'Content-Type: application/json' \
  -H "X-Request-ID: ${RUN_ID}-request" \
  -H "Idempotency-Key: ${RUN_ID}-request" \
  --data-binary "@$PAYLOAD")

echo "HTTP Code: $HTTP"
if [[ "$HTTP" != "200" && "$HTTP" != "202" ]]; then
  echo "Submission failed:"
  cat "$BODY"
  exit 1
fi

JOB_ID=$(jq -r '.job_id // .data.job_id // .id // empty' "$BODY")
echo "Job ID: $JOB_ID"

while true; do
  POLL_HTTP=$(curl -sS --max-time 15 -o "$BODY.poll" -w '%{http_code}' \
    -X GET "$LOCAL_BASE_URL/api/jobs/$JOB_ID/full" \
    -H "Authorization: Bearer $LOCAL_TOKEN")

  if [[ "$POLL_HTTP" == "200" ]]; then
    STATUS=$(jq -r '.current_step // .status // "UNKNOWN"' "$BODY.poll")
    NOW=$(date +%H:%M:%S)
    echo "[$NOW] Status: $STATUS"

    if [[ "$STATUS" == "SUCCEEDED" || "$STATUS" == "COMPLETED" ]]; then
      END_TIME=$(date +%s%N)
      DURATION_SEC=$(awk -v start="$START_TIME" -v end="$END_TIME" 'BEGIN { printf "%.2f", (end-start)/1000000000 }')
      echo "=== JOB SUCCEEDED in ${DURATION_SEC}s ==="
      echo "--- Summary ---"
      jq '{
        job_id: .id,
        status: .current_step,
        localized_renders: .job.result.localized_renders,
        render_metrics: .job.result.render_metrics
      }' "$BODY.poll"
      exit 0
    elif [[ "$STATUS" == "FAILED" || "$STATUS" == "ERROR" ]]; then
      echo "=== JOB FAILED ==="
      jq '{error: .error, stages: .job.result.stages}' "$BODY.poll"
      exit 1
    fi
  fi
  sleep "$POLL_SECONDS"
done
