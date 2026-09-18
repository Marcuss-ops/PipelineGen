#!/usr/bin/env bash
# Runs the 1 clip / 1 scene / 10 languages acceptance job: ONE source clip is
# translated at runtime into every requested language and each language is
# rendered with ITS OWN burned subtitles into ITS OWN Drive folder under the
# run folder (<docs root>/<job>/<language>).
#
# The destination is per language by contract; the payload's clips fields
# (drive_folder_id / drive_subfolder_name) are only the fallback of a run with
# no documents root at all, so a docs-enabled run publishes beside the script.
#
# Usage:  ./scripts/run_verify_1clip_10lang.sh
# Requires: VELOX_ADMIN_TOKEN (or TOKEN_FILE), a running API on PIPELINEGEN_URL
# and a GPU render lane (output.render.require_gpu=true fails closed without it).
set -euo pipefail

ROOT=$(cd "$(dirname "$0")/.." && pwd)
source "$ROOT/scripts/lib/dotenv.sh"
load_dotenv_missing "$ROOT/.env"

LOCAL_BASE_URL="${PIPELINEGEN_URL:-http://127.0.0.1:8000}"
LOCAL_TOKEN="${VELOX_ADMIN_TOKEN:-${VELOX_PIPELINEGEN_TOKEN:-}}"
PAYLOAD="$ROOT/ops/jobs/verify_1clip_10lang.generate.json"
POLL_SECONDS=5
# Ten GPU renders of one clip: the default budget is generous enough for the
# queue plus the fan-out without waiting on a stalled lane forever.
TIMEOUT_SECONDS="${VERIFY_1CLIP_10LANG_TIMEOUT:-1800}"

[[ -n "$LOCAL_TOKEN" ]] || { echo "VELOX_ADMIN_TOKEN is required" >&2; exit 2; }
[[ -f "$PAYLOAD" ]] || { echo "missing payload: $PAYLOAD" >&2; exit 2; }

RUN_ID="verify-1clip-10lang-$(date -u +%Y%m%d-%H%M%S)-$$"
BODY=$(mktemp)
trap 'rm -f "$BODY" "$BODY.poll"' EXIT INT TERM

echo "Submitting 1 clip / 1 scene / 10 languages verification job to $LOCAL_BASE_URL/api/script/generate..."
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
[[ -n "$JOB_ID" ]] || { cat "$BODY" >&2; exit 1; }

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
      # The acceptance facts: ten languages, one render per language, each with
      # the language it was asked for, a real content-addressed asset id, and a
      # destination folder that is DIFFERENT per language.
      jq '{
        job_id: (.id // .job.id),
        status: (.status // .job.status),
        total_wall_ms: .timing.wall_ms,
        documents: (.result.data.result.documents // .result.documents),
        render_metrics: (.result.data.result.render_metrics // .result.render_metrics),
        localized_render_failures: (.result.data.result.localized_render_failures // .result.localized_render_failures),
        renders_by_language: ((.result.data.result.localized_renders // .result.localized_renders // [])
          | group_by(.language)
          | map({
              language: .[0].language,
              renders: length,
              folders: ([.[].drive_folder_id] | unique),
              clips: [.[].clip_id],
              asset_ids: [.[].asset_id],
              drive_links: [.[].drive_link]
            }))
      }' "$BODY.poll"
      echo
      echo "Check: every language must report exactly 1 render, exactly 1 folder,"
      echo "and no two languages may share a folder."
      exit 0
      ;;
    FAILED|ERROR|CANCELLED)
      echo "=== JOB FAILED ===" >&2
      jq '{error: (.error // .job.error), timeline: (.timeline // .job.timeline)}' "$BODY.poll" >&2
      exit 1
      ;;
  esac
  (( $(date +%s) < deadline )) || { echo "Poll timeout after ${TIMEOUT_SECONDS}s" >&2; exit 1; }
  sleep "$POLL_SECONDS"
done
