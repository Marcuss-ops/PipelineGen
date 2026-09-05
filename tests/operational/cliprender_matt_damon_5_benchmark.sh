#!/usr/bin/env bash
# Submit five Matt Damon clip.render jobs concurrently and collect timing data.
# clip.render accepts one source_asset_id per job, so this is a five-job batch.

set -euo pipefail
umask 077

ROOT=$(cd "$(dirname "$0")/../.." && pwd)
if [[ -f "$ROOT/.env" && -f "$ROOT/scripts/lib/dotenv.sh" ]]; then
  # Reuse the repository's standard local environment loading convention.
  # Existing exported variables still take precedence in dotenv.sh.
  source "$ROOT/scripts/lib/dotenv.sh"
  load_dotenv_missing "$ROOT/.env"
fi

BASE_URL="${VELOX_MASTER_URL:-http://127.0.0.1:8000}"
TOKEN="${VELOX_MASTER_ADMIN_TOKEN:-${VELOX_ADMIN_TOKEN:-}}"
POLL_SECONDS="${CLIPRENDER_POLL_SECONDS:-2}"
TIMEOUT_SECONDS="${CLIPRENDER_TIMEOUT_SECONDS:-1800}"
OUTPUT_DIR="${CLIPRENDER_OUTPUT_DIR:-$(cd "$(dirname "$0")/../.." && pwd)/ops/benchmarks/cliprender}"
RUN_ID="matt-damon-5-cliprender-$(date -u +%Y%m%d-%H%M%S)-$$"
RUN_DIR="$OUTPUT_DIR/$RUN_ID"

DEFAULT_IDS=(
  "yt_0ElQTzSx3ec_72_91_v1"
  "yt_ERzbkt5r5Gg_32_66_v1"
  "yt_Gcgdk1gEo8U_285_302_v1"
  "yt_S6ADB98CR7g_358_425_v1"
  "yt_T6x-kDiQsWM_203_252_v1"
)
if [[ -n "${CLIPRENDER_SOURCE_ASSET_IDS:-}" ]]; then
  IFS=',' read -r -a SOURCE_IDS <<< "$CLIPRENDER_SOURCE_ASSET_IDS"
else
  SOURCE_IDS=("${DEFAULT_IDS[@]}")
fi

[[ -n "$TOKEN" ]] || { echo "VELOX_MASTER_ADMIN_TOKEN or VELOX_ADMIN_TOKEN is required" >&2; exit 2; }
EXPECTED_COUNT="${CLIPRENDER_EXPECTED_COUNT:-5}"
(( ${#SOURCE_IDS[@]} == EXPECTED_COUNT )) || { echo "expected $EXPECTED_COUNT source asset IDs" >&2; exit 2; }
command -v jq >/dev/null 2>&1 || { echo "jq is required" >&2; exit 2; }
command -v curl >/dev/null 2>&1 || { echo "curl is required" >&2; exit 2; }
mkdir -p "$RUN_DIR"
trap 'rm -rf "$RUN_DIR/tmp"' EXIT INT TERM
mkdir -p "$RUN_DIR/tmp"

WATERMARK_TEXT="${CLIPRENDER_WATERMARK_TEXT:-MATT DAMON}"
WATERMARK_ASSET_ID="${CLIPRENDER_WATERMARK_ASSET_ID:-}"
# The benchmark inputs already have editable ASS sidecars. Keep subtitles
# editable without burning them a second time into source videos that may
# already contain captions. Set CLIPRENDER_SUBTITLES_MODE=burn explicitly
# when testing a clean, unsubtitled source.
SUBTITLES_MODE="${CLIPRENDER_SUBTITLES_MODE:-sidecar}"
# Never silently fall back to clip.render's certification root: benchmark
# artifacts must be isolated in the caller-selected Drive folder.
DESTINATION_ID="${CLIPRENDER_DESTINATION_FOLDER_ID:-19UYPb2dfK_Y_-rDBXrsY_1VOge4iiWmk}"
[[ -n "$DESTINATION_ID" ]] || { echo "CLIPRENDER_DESTINATION_FOLDER_ID is required; refusing root upload" >&2; exit 2; }

build_payload() {
  local source_id="$1" payload_path="$2"
  jq -n --arg source "$source_id" --arg wm_text "$WATERMARK_TEXT" \
    --arg wm_asset "$WATERMARK_ASSET_ID" --arg destination "$DESTINATION_ID" \
    --arg subtitles_mode "$SUBTITLES_MODE" '
    {
      source_asset_id: $source,
      watermark: ({
        enabled:true, position:"top_right", opacity:0.85, margin_px:24,
        style:{font:"DejaVu Sans",size:42,color:"#FFFFFF",
          shadow:{color:"#000000",opacity:0.75,blur_px:8,offset_x:2,offset_y:2}}
      } | if $wm_asset != "" then . + {asset_id:$wm_asset} | del(.text)
        else . + {text:$wm_text} end),
      transcript:{mode:"reuse_or_generate",language:"en",persist:true},
      subtitles:{
        enabled:true, mode:$subtitles_mode, style_id:"matt-damon-benchmark-v1",
        style:{font:"DejaVu Sans",size:54,color:"#FFFFFF",position:"bottom_center",
          shadow:{color:"#000000",opacity:0.80,blur_px:10,offset_x:0,offset_y:5}}
      },
      output:{contract:"VELOX_ASSEMBLY_READY_V1",width:1920,height:1080,fps_num:24,fps_den:1},
      audio:{mode:"copy_if_compatible"},
      destination:(if $destination != "" then {drive_folder_id:$destination} else null end),
      execution:{require_gpu:false}
    }' > "$payload_path"
}

submit_and_poll() {
  local index="$1" source_id="$2"
  local job_dir="$RUN_DIR/clip-$index" request submit latest http job_id status deadline
  mkdir -p "$job_dir"
  request="$job_dir/request.json"; submit="$job_dir/submit.json"; latest="$job_dir/latest.json"
  build_payload "$source_id" "$request"
  http=$(curl -sS --max-time 30 -o "$submit" -w '%{http_code}' -X POST "$BASE_URL/api/clips/render" \
    -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
    -H "X-Request-ID: $RUN_ID-clip-$index" -H "Idempotency-Key: $RUN_ID-clip-$index" \
    --data-binary "@$request")
  if [[ "$http" != "200" && "$http" != "202" ]]; then
    jq -n --arg status "submit_http_$http" --arg source "$source_id" \
      '{status:$status,source_asset_id:$source}' > "$latest"
    return 1
  fi
  job_id=$(jq -r '.job_id // .id // empty' "$submit")
  [[ -n "$job_id" ]] || return 1
  echo "clip=$index source=$source_id job_id=$job_id"
  deadline=$(( $(date +%s) + TIMEOUT_SECONDS ))
  while :; do
    curl -sS --max-time 30 -o "$latest" -H "Authorization: Bearer $TOKEN" \
      "$BASE_URL/api/jobs/$job_id/full"
    status=$(jq -r '.status // .job.status // .result.status // empty' "$latest")
    case "${status^^}" in
      SUCCEEDED|COMPLETED|SUCCEEDED_WITH_WARNINGS)
        echo "clip=$index job_id=$job_id status=$status"; return 0 ;;
      FAILED|ERROR|CANCELLED)
        echo "clip=$index job_id=$job_id status=$status" >&2; return 1 ;;
    esac
    if (( $(date +%s) >= deadline )); then
      jq --arg status TIMEOUT '. + {benchmark_status:$status}' "$latest" > "$latest.tmp"
      mv "$latest.tmp" "$latest"
      return 1
    fi
    sleep "$POLL_SECONDS"
  done
}

batch_start_ms=$(date +%s%3N)
failed=0
# One job at a time: Chronon/Vulkan memory is intentionally bounded and the
# benchmark must never create five simultaneous render surfaces.
for i in "${!SOURCE_IDS[@]}"; do
  submit_and_poll "$((i + 1))" "${SOURCE_IDS[$i]}" || failed=1
done
batch_end_ms=$(date +%s%3N)

REPORT="$RUN_DIR/report.json"
jq -n --arg run_id "$RUN_ID" --arg endpoint "$BASE_URL/api/clips/render" \
  --argjson batch_wall_ms "$((batch_end_ms - batch_start_ms))" \
  --argjson failed_jobs "$failed" --argjson source_asset_ids "$(printf '%s\n' "${SOURCE_IDS[@]}" | jq -R . | jq -s .)" \
  '{run_id:$run_id,endpoint:$endpoint,batch_wall_ms:$batch_wall_ms,failed_jobs:$failed_jobs,
    source_asset_ids:$source_asset_ids,jobs:[]}' > "$REPORT"
for ((i=1; i<=EXPECTED_COUNT; i++)); do
  latest="$RUN_DIR/clip-$i/latest.json"
  if [[ -f "$latest" ]]; then
    jq --argjson index "$i" '. + {batch_index:$index}' "$latest" > "$latest.tmp"
    mv "$latest.tmp" "$latest"
    jq --slurpfile item "$latest" '.jobs += $item' "$REPORT" > "$REPORT.tmp"
    mv "$REPORT.tmp" "$REPORT"
  fi
done

echo "report=$REPORT"
echo "batch_wall_ms=$(jq -r '.batch_wall_ms' "$REPORT")"
echo "timings=$(jq -c '[.jobs[] | {batch_index,job_id:(.job.id // .id // .job_id),status:(.status // .job.status),timing:(.timing // {})}]' "$REPORT")"
echo "bottlenecks=$(jq -c '[.jobs[] | {batch_index,job_id:(.job.id // .id // .job_id),stage:(.timing.bottleneck_stage // null),operation:(.timing.bottleneck_operation // null),stages:(.timing.stages // []),operations:(.timing.operations // [])}]' "$REPORT")"
exit "$failed"
