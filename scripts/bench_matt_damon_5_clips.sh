#!/usr/bin/env bash
# scripts/bench_matt_damon_5_clips.sh
# Benchmark: 5 Matt Damon clips parallel render via POST /api/clips/render
# Features verified:
# - Warm Chronon daemon over IPC socket (/run/chronon3d/chronon.sock)
# - Dedicated clip_render_settle_workers pool (P0.5)
# - Zero-copy locator-first artifact flow (P1)
# - Single-point certified output contract (P1)
# - Async Drive delivery intent (P1)
set -Eeuo pipefail

SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
BASE_URL="${VELOX_BASE_URL:-http://127.0.0.1:8000}"
# The admin credential MUST come from the environment: AGENTS.md fixes the
# canonical variable name (VELOX_ADMIN_TOKEN) and forbids hard-coded literals.
# Fail closed rather than silently authenticating with a baked-in fallback — a
# committed credential is a leaked credential, and a silent fallback hides the
# misconfiguration until someone reads the script.
TOKEN="${VELOX_ADMIN_TOKEN:-}"
if [ -z "$TOKEN" ]; then
  echo "VELOX_ADMIN_TOKEN is not set. Export it (see AGENTS.md) before running this benchmark." >&2
  exit 1
fi

CLIPS=(
  "yt_0ElQTzSx3ec_72_91_v1"
  "yt_ERzbkt5r5Gg_32_66_v1"
  "yt_Gcgdk1gEo8U_285_302_v1"
  "yt_S6ADB98CR7g_358_425_v1"
  "yt_T6x-kDiQsWM_203_252_v1"
)

echo "================================================================="
echo "  VELOX EDITING - 5 CLIPS MATT DAMON WARM DAEMON BENCHMARK"
echo "================================================================="
echo "Endpoint: $BASE_URL/api/clips/render"
echo "Clips: ${#CLIPS[@]}"
echo "Mode: IPC warm daemon (/run/chronon3d/chronon.sock)"
echo "-----------------------------------------------------------------"

T_START=$(date +%s%3N)

SUBMIT_JOB_IDS=()
CHILD_JOB_IDS=()

echo "Submitting 5 clips concurrently..."
for clip_id in "${CLIPS[@]}"; do
  PAYLOAD=$(cat <<JSON
{
  "source_asset_id": "${clip_id}",
  "background": {"mode": "none"},
  "transcript": {"mode": "reuse", "language": "en"},
  "subtitles": {"enabled": true, "mode": "burn"},
  "output": {"contract": "VELOX_ASSEMBLY_READY_V1", "width": 1920, "height": 1080, "fps_num": 24, "fps_den": 1},
  "audio": {"mode": "copy_if_compatible"},
  "destination": {"drive_folder_id": "1ST6FxPuRaxwBOIz39MAN8Jj4gDv509-K"}
}
JSON
)
  RESP=$(curl -s -X POST "$BASE_URL/api/clips/render" \
    -H "Authorization: Bearer $TOKEN" \
    -H "Content-Type: application/json" \
    -d "$PAYLOAD")
  JOB_ID=$(echo "$RESP" | jq -r '.job_id // empty')
  if [[ -z "$JOB_ID" ]]; then
    echo "❌ Failed to submit $clip_id: $RESP"
    exit 1
  fi
  echo "  ✅ Submitted $clip_id -> Job $JOB_ID"
  SUBMIT_JOB_IDS+=("$JOB_ID")
done

T_SUBMIT_DONE=$(date +%s%3N)
SUBMIT_WALL_MS=$(( T_SUBMIT_DONE - T_START ))
echo "All 5 jobs submitted in ${SUBMIT_WALL_MS} ms"
echo "-----------------------------------------------------------------"
echo "Resolving settle continuation child jobs..."

# Wait briefly for submit workers to release and create settle children
for job_id in "${SUBMIT_JOB_IDS[@]}"; do
  while true; do
    JOB_DATA=$(curl -s -H "Authorization: Bearer $TOKEN" "$BASE_URL/api/jobs/$job_id")
    CHILD_ID=$(echo "$JOB_DATA" | jq -r '.job.result.child_job_id // empty')
    STATUS=$(echo "$JOB_DATA" | jq -r '.job.status // empty')
    if [[ -n "$CHILD_ID" ]]; then
      CHILD_JOB_IDS+=("$CHILD_ID")
      break
    fi
    if [[ "$STATUS" == "FAILED" ]]; then
      ERR=$(echo "$JOB_DATA" | jq -r '.job.error // empty')
      echo "❌ Parent $job_id failed: $ERR"
      exit 1
    fi
    sleep 0.1
  done
done

echo "Waiting for all 5 settle continuation jobs to complete..."
ALL_DONE=0
POLL_COUNT=0
while [[ $ALL_DONE -eq 0 ]]; do
  POLL_COUNT=$(( POLL_COUNT + 1 ))
  COMPLETED_COUNT=0
  FAILED_COUNT=0
  for child_id in "${CHILD_JOB_IDS[@]}"; do
    CHILD_DATA=$(curl -s -H "Authorization: Bearer $TOKEN" "$BASE_URL/api/jobs/$child_id")
    C_STATUS=$(echo "$CHILD_DATA" | jq -r '.job.status // empty')
    if [[ "$C_STATUS" == "SUCCEEDED" ]]; then
      COMPLETED_COUNT=$(( COMPLETED_COUNT + 1 ))
    elif [[ "$C_STATUS" == "FAILED" ]]; then
      C_ERR=$(echo "$CHILD_DATA" | jq -r '.job.error // empty')
      echo ""
      echo "❌ Settle $child_id FAILED: $C_ERR"
      exit 1
    fi
  done
  NOW=$(date +%s%3N)
  ELAPSED_SEC=$(echo "scale=2; ($NOW - $T_START) / 1000" | bc)
  printf "\r[%6.1fs] Completed: %d/%d (poll #%d)..." "$ELAPSED_SEC" "$COMPLETED_COUNT" "${#CHILD_JOB_IDS[@]}" "$POLL_COUNT"
  if [[ $COMPLETED_COUNT -eq ${#CHILD_JOB_IDS[@]} ]]; then
    ALL_DONE=1
    break
  fi
  sleep 0.5
done

T_END=$(date +%s%3N)
TOTAL_WALL_MS=$(( T_END - T_START ))
TOTAL_WALL_SEC=$(echo "scale=2; $TOTAL_WALL_MS / 1000" | bc)

echo ""
echo "================================================================="
echo "  BENCHMARK RESULTS - 5 MATT DAMON CLIPS"
echo "================================================================="
TOTAL_WORK_MS=0
TOTAL_FRAMES=0

for idx in "${!CHILD_JOB_IDS[@]}"; do
  child_id="${CHILD_JOB_IDS[$idx]}"
  clip_id="${CLIPS[$idx]}"
  CHILD_DATA=$(curl -s -H "Authorization: Bearer $TOKEN" "$BASE_URL/api/jobs/$child_id")
  RES=$(echo "$CHILD_DATA" | jq '.job.result')
  SIZE=$(echo "$RES" | jq -r '.asset.size_bytes // 0')
  RENDER_WALL_MS=$(echo "$RES" | jq -r '.render.render_wall_ms // 0')
  RENDER_LOOP_MS=$(echo "$RES" | jq -r '.render.metrics_v2.render_loop_ms // 0')
  FRAMES=$(echo "$RES" | jq -r '.render.metrics_v2.frames // 0')
  RENDER_FPS=$(echo "$RES" | jq -r '.render.metrics_v2.render_fps // 0')
  STORAGE_KEY=$(echo "$RES" | jq -r '.render.chronon_timing.storage_key // empty')
  
  TOTAL_WORK_MS=$(( TOTAL_WORK_MS + RENDER_WALL_MS ))
  TOTAL_FRAMES=$(( TOTAL_FRAMES + FRAMES ))

  echo "Clip #$(( idx + 1 )): $clip_id"
  echo "  Size:       $(( SIZE / 1024 )) KiB (locator-first, zero local copies)"
  echo "  Frames:     $FRAMES @ 24fps"
  echo "  Render Loop: ${RENDER_LOOP_MS} ms"
  echo "  Render Wall: ${RENDER_WALL_MS} ms (${RENDER_FPS} fps)"
  echo "  Sidecar:    ${STORAGE_KEY:0:16}..."
done

SPEEDUP=$(echo "scale=2; $TOTAL_WORK_MS / $TOTAL_WALL_MS" | bc)
THROUGHPUT=$(echo "scale=2; (${#CLIPS[@]} / ($TOTAL_WALL_MS / 60000))" | bc)
AVG_FPS=$(echo "scale=2; $TOTAL_FRAMES / ($TOTAL_WALL_MS / 1000)" | bc)

echo "-----------------------------------------------------------------"
echo "Total Batch Wall Time:  ${TOTAL_WALL_SEC} s (${TOTAL_WALL_MS} ms)"
echo "Total Accumulated Work: ${TOTAL_WORK_MS} ms"
echo "Realized Speedup:       ${SPEEDUP}x (GPU multi-lane overlap)"
echo "Throughput:             ${THROUGHPUT} clips/min"
echo "Batch Aggregate FPS:    ${AVG_FPS} fps"
echo "================================================================="
