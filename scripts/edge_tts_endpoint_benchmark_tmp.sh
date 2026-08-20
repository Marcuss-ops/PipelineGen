#!/usr/bin/env bash
set -Eeuo pipefail
umask 077

API="${VELOX_BASE_URL:-http://127.0.0.1:8000}"
TOKEN="${VELOX_ADMIN_TOKEN:?VELOX_ADMIN_TOKEN is required}"
OUT="${EDGE_TTS_ENDPOINT_BENCHMARK_OUT:-/tmp/edge-tts-endpoint-benchmark-$(date +%Y%m%d-%H%M%S)}"
FOLDER="${EDGE_TTS_ENDPOINT_DRIVE_FOLDER:-1wFhLmyyIH5rKSbtQuCuua9a2LKQymA8A}"
PROJECT="edge-tts-endpoint-benchmark-$(date +%Y%m%d%H%M%S)"
WORDS_LIST="${EDGE_TTS_ENDPOINT_WORDS:-399 401 801 1201}"
mkdir -p "$OUT"

declare -A JOB_IDS STARTED_MS
pending=()

for words in $WORDS_LIST; do
  for run in 1 2 3 4 5; do
    label="w${words}-r${run}"
    text=$(python3 - "$words" "$run" <<'PY'
import sys
n = int(sys.argv[1])
run = sys.argv[2]
print(" ".join(["benchmark"] * (n - 1) + [f"run{run}"]))
PY
)
    if [[ -f "$OUT/$label.json" ]] && jq -e '.status == "SUCCEEDED"' "$OUT/$label.json" >/dev/null 2>&1; then
      echo "skip $label already succeeded"
      continue
    fi
    request_id="${PROJECT}-${label}"
    filename="${PROJECT}-${label}.mp3"
    payload=$(jq -nc \
      --arg rid "$request_id" --arg text "$text" --arg filename "$filename" \
      --arg folder "$FOLDER" --arg project "$PROJECT" \
      '{
        request_id: $rid,
        project: $project,
        items: [{text: $text, language: "en-US", voice: "en-US-RogerNeural", filename: $filename, required: true}],
        destination: {kind: "explicit", folder_id: $folder},
        options: {
          remove_silence: false,
          strategy: "verify",
          parallelism: 1,
          voiceover_timing: {mode: "best_effort", boundary: "word", formats: ["json", "srt", "vtt"]}
        }
      }')
    started_ms=$(date +%s%3N)
    response=$(curl -sS --retry 10 --retry-connrefused --retry-delay 2 --max-time 30 -w '\n%{http_code}' \
      -X POST "$API/api/media/voiceover/generate" \
      -H "Authorization: Bearer $TOKEN" -H "X-Request-ID: $request_id" \
      -H "Idempotency-Key: $request_id" -H 'Content-Type: application/json' --data "$payload")
    code="${response##*$'\n'}"
    body="${response%$'\n'*}"
    if [[ "$code" != 202 ]]; then
      jq -nc --arg lbl "$label" --argjson words "$words" --arg code "$code" --arg body "${body:0:500}" \
        '{"label":$lbl,"words":$words,"http_code":($code|tonumber),"error":$body}' > "$OUT/$label.json"
      echo "FAIL dispatch $label http=$code" >&2
      continue
    fi
    job_id=$(jq -r '.job_id // empty' <<<"$body")
    if [[ -z "$job_id" ]]; then
      jq -nc --arg lbl "$label" --argjson words "$words" --arg body "${body:0:500}" \
        '{"label":$lbl,"words":$words,"error":"missing job_id","response":$body}' > "$OUT/$label.json"
      echo "FAIL dispatch $label missing job_id" >&2
      continue
    fi
    JOB_IDS["$label"]="$job_id"
    STARTED_MS["$label"]="$started_ms"
    pending+=("$label")
    echo "dispatched $label job=$job_id"
  done
done

for label in "${pending[@]}"; do
  parent_id="${JOB_IDS[$label]}"
  parent_status=""
  parent_full='{}'
  child_id=""
  # The POST endpoint returns a parent immediately. Discover the child
  # job before measuring completion; parent SUCCEEDED can mean only that
  # fan-out was enqueued, not that TTS/Drive finished.
  for _ in $(seq 1 90); do
    parent_poll=$(curl -sS --retry 10 --retry-connrefused --retry-delay 2 --max-time 15 \
      -H "Authorization: Bearer $TOKEN" "$API/api/jobs/$parent_id/full")
    parent_status=$(jq -r '.status // .state // empty' <<<"$parent_poll")
    parent_full="$parent_poll"
    child_id=$(jq -r --arg parent "$parent_id" '
      [(.result.child_job_ids[]?), (.events[]?.data.stage_progress.voiceover.languages[]?.job_id)]
      | .[]? | select(type == "string" and . != $parent)
    ' <<<"$parent_poll" | head -n 1)
    [[ -n "$child_id" ]] && break
    sleep 1
  done

  status="$parent_status"
  full="$parent_full"
  measured_job_id="$parent_id"
  if [[ -n "$child_id" ]]; then
    measured_job_id="$child_id"
    for _ in $(seq 1 360); do
      child_poll=$(curl -sS --retry 10 --retry-connrefused --retry-delay 2 --max-time 15 \
        -H "Authorization: Bearer $TOKEN" "$API/api/jobs/$child_id/full")
      status=$(jq -r '.status // .state // empty' <<<"$child_poll")
      full="$child_poll"
      case "$status" in
        SUCCEEDED|COMPLETED|FAILED|CANCELLED|DEAD_LETTER|PARTIAL_SUCCESS|succeeded|completed|failed|cancelled|dead_letter|partial_success) break ;;
      esac
      sleep 1
    done
  fi
  finished_ms=$(date +%s%3N)
  words="${label#w}"; words="${words%%-*}"
  run="${label##*-r}"
  jq -n --arg lbl "$label" --argjson words "$words" --argjson run "$run" \
    --arg job_id "$measured_job_id" --arg parent_id "$parent_id" \
    --arg child_id "$child_id" --arg parent_status "$parent_status" --arg status "$status" \
    --argjson started_ms "${STARTED_MS[$label]}" --argjson finished_ms "$finished_ms" \
    --argjson full "$full" \
    '{"label":$lbl,"words":$words,"run":$run,"job_id":$job_id,"parent_job_id":$parent_id,"child_job_id":$child_id,"parent_status":$parent_status,"status":$status,"wall_ms":($finished_ms-$started_ms),"full":$full}' \
    > "$OUT/$label.json"
  echo "completed $label status=$status wall_ms=$((finished_ms-${STARTED_MS[$label]}))"
done

echo "$OUT"
