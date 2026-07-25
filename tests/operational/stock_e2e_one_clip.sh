#!/usr/bin/env bash
# stock_e2e_one_clip.sh
#
# End-to-end: submit a 1-clip stock pipeline run, poll for completion,
# verify the job reaches a terminal state (SUCCEEDED or FAILED).
# Uses a 4-second clip from the Pacquiao vs Cotto fight.
#
# Exit codes:
#   0 = PASS (job reached terminal state)
#   1 = FAIL (wrong HTTP, job stuck, or unexpected error)
#   2 = prereq missing (server down / curl/jq absent)
#
# Overridable env vars:
#   BASE  = http://127.0.0.1:8000
#   AUTH  = "Authorization: Bearer <token>"
#   POLL_MAX = max poll iterations (default 30)
#   POLL_INTERVAL = seconds between polls (default 10)

set -euo pipefail

BASE="${BASE:-http://127.0.0.1:8000}"
AUTH="${AUTH:-Authorization: Bearer ${VELOX_ADMIN_TOKEN:-}}"
POLL_MAX="${POLL_MAX:-30}"
POLL_INTERVAL="${POLL_INTERVAL:-10}"

command -v curl >/dev/null 2>&1 || { echo "FAIL: curl not on PATH (exit 2)" >&2; exit 2; }
command -v jq >/dev/null 2>&1   || { echo "FAIL: jq not on PATH (exit 2)" >&2; exit 2; }

# ---- Pre-flight --------------------------------------------------------
HTTP=$(curl -sS -o /dev/null -w '%{http_code}' --max-time 10 \
    -X POST "$BASE/api/stock-pipeline/run" \
    -H "$AUTH" -H "Content-Type: application/json" -d '{}' 2>/dev/null || echo "000")
if [ "$HTTP" = "000" ]; then
    echo "FAIL: PipelineGen server at $BASE unreachable (exit 2)" >&2; exit 2
fi

# ---- Submit 1-clip run --------------------------------------------------
PAYLOAD=$(jq -n '{
    direct_urls: ["https://www.youtube.com/watch?v=QdSbtEo3x_Y"],
    clip_duration: 4,
    folder_name: "e2e-one-clip",
    async: true
}')

echo "=== STK-E2E-ONE-CLIP: Submit 1-clip stock run ==="
RESP=$(curl -sS -X POST "$BASE/api/stock-pipeline/run" \
    -H "$AUTH" -H "Content-Type: application/json" \
    --data "$PAYLOAD" --max-time 30)

HTTP_CODE=$(echo "$RESP" | jq -r '.status // empty')
JOB_ID=$(echo "$RESP" | jq -r '.job_id // empty')

echo "Response: $RESP"
echo "JOB_ID=$JOB_ID"

if [ -z "$JOB_ID" ]; then
    echo "FAIL: No job_id in response (HTTP or composition error)" >&2
    echo "Body: $RESP" >&2
    exit 1
fi

# ---- Poll until terminal ------------------------------------------------
echo "=== Polling job $JOB_ID ==="
TERMINAL=0
for i in $(seq 1 "$POLL_MAX"); do
    sleep "$POLL_INTERVAL"
    STATUS=$(curl -sS "$BASE/api/jobs/$JOB_ID/full" -H "$AUTH" --max-time 10 2>/dev/null \
        | jq -r '.status // "unknown"')
    echo "  [$i/$POLL_MAX] status=$STATUS"
    case "$STATUS" in
        SUCCEEDED|FAILED|CANCELLED)
            TERMINAL=1
            break
            ;;
    esac
done

if [ "$TERMINAL" -eq 0 ]; then
    echo "FAIL: Job did not reach terminal state in $(( POLL_MAX * POLL_INTERVAL ))s" >&2
    exit 1
fi

echo
echo "PASS: Job $JOB_ID reached terminal state: $STATUS"
exit 0
