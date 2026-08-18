#!/usr/bin/env bash
# Generate a grounded script from ten indexed clips in one actor/source folder,
# then render each clip independently with the canonical watermark + subtitles
# contract. Narrative generation and media rendering are intentionally separate
# API contracts: generate owns the document text; clips/render owns pixels.
set -Eeuo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
DB="${SMOKE_DB:-$ROOT/data/media/media.db.sqlite}"
API_BASE="${VELOX_BASE_URL:-http://127.0.0.1:8000}"
SOURCE_VIDEO_ID="${SOURCE_VIDEO_ID:-pyw_bXCaj-s}"
WATERMARK_ASSET_ID="${WATERMARK_ASSET_ID:-9cc03bef0eeefa96d5ec39a63d1d1199}"
DRIVE_FOLDER_ID="${DRIVE_FOLDER_ID:-1ST6FxPuRaxwBOIz39MAN8Jj4gDv509-K}"
WORK_DIR="${WORK_DIR:-$(mktemp -d /tmp/actor-10-render.XXXXXX)}"
POLL_LIMIT="${POLL_LIMIT:-300}"
RENDER_CONCURRENCY="${RENDER_CONCURRENCY:-2}"
KEEP_WORK="${KEEP_WORK:-0}"

cleanup() { [[ "$KEEP_WORK" == "1" ]] || rm -rf "$WORK_DIR"; }
trap cleanup EXIT INT TERM

for command_name in jq sqlite3 curl; do
  command -v "$command_name" >/dev/null || { echo "$command_name is required" >&2; exit 2; }
done
mkdir -p "$WORK_DIR/renders"

CLIP_IDS_JSON="$(sqlite3 -json "$DB" "
  SELECT id FROM media_assets
  WHERE source='youtube'
    AND source_video_id='$SOURCE_VIDEO_ID'
    AND lifecycle_state='ACTIVE'
    AND index_state='INDEXED'
    AND drive_link<>''
  ORDER BY start_ms, id
  LIMIT 10;" | jq '[.[].id]')"
CLIP_COUNT="$(jq 'length' <<<"$CLIP_IDS_JSON")"
[[ "$CLIP_COUNT" == "10" ]] || {
  echo "expected 10 indexed clips for source_video_id=$SOURCE_VIDEO_ID, got $CLIP_COUNT" >&2
  exit 2
}
mapfile -t CLIP_IDS < <(jq -r '.[]' <<<"$CLIP_IDS_JSON")

auth_curl() {
  "$ROOT/scripts/with-velox-auth" bash -c \
    'curl -fsS --max-time 60 -H "Authorization: Bearer ${VELOX_ADMIN_TOKEN}" "$@"' \
    bash "$@"
}

poll_job() {
  local job_id="$1" output_file="$2" attempt status
  for attempt in $(seq 1 "$POLL_LIMIT"); do
    auth_curl "$API_BASE/api/jobs/$job_id/full" >"$output_file"
    status="$(jq -r '.status // .job.status // empty' "$output_file" | tr '[:lower:]' '[:upper:]')"
    echo "  [$attempt] $job_id $status"
    case "$status" in
      SUCCEEDED|SUCCEEDED_WITH_WARNINGS|COMPLETED) return 0 ;;
      FAILED|CANCELLED|DEAD_LETTER) return 1 ;;
    esac
    sleep 2
  done
  echo "timeout polling $job_id" >&2
  return 124
}

# ── 1. Generate grounded script/document ───────────────────────────────
GENERATE_PAYLOAD="$WORK_DIR/generate-payload.json"
jq -n \
  --arg source_video "$SOURCE_VIDEO_ID" \
  --arg folder "$DRIVE_FOLDER_ID" \
  --argjson ids "$CLIP_IDS_JSON" \
  '{
    version: 2,
    preset: "custom",
    correlation_id: ("actor-10-" + $source_video),
    force_refresh: true,
    items: [{
      id: ("actor-10-" + $source_video),
      title: "Dieci incontri celebri: un filo narrativo",
      language: "it",
      tone: "documentario leggero, osservativo e conciso",
      style: "Scrivi un testo narrativo breve e fluido che colleghi i dieci momenti nell ordine fornito. Descrivi solo ciò che emerge dai clip, senza inventare biografia o citazioni. Dedica una breve transizione a ogni clip e mantieni il testo adatto a voiceover e sottotitoli.",
      source: {
        type: "clips",
        clip_ids: $ids,
        num_clips: 10,
        ordering_strategy: "input_order",
        grounding_policy: "clips_primary",
        fallback_policy: "strict",
        force_refresh: true
      },
      script_params: {
        target_words: 650,
        min_words: 500,
        segment_words: 65,
        use_memory: false,
        force_refresh: true,
        skip_quality_gate: true
      },
      output: {
        save_to_db: true,
        generate_timeline: true,
        generate_metadata: true,
        extract_entities: false,
        generate_scene_images: false,
        drive_folder_id: $folder
      }
    }]
  }' >"$GENERATE_PAYLOAD"

echo "Selected clips: ${CLIP_IDS[*]}"
echo "Generate payload: $GENERATE_PAYLOAD"
jq . "$GENERATE_PAYLOAD"

GENERATE_RESPONSE="$WORK_DIR/generate-dispatch.json"
auth_curl -X POST "$API_BASE/api/script/generate" \
  -H 'Content-Type: application/json' \
  -H "Idempotency-Key: actor-10-${SOURCE_VIDEO_ID}-$(date +%s)" \
  --data-binary @"$GENERATE_PAYLOAD" >"$GENERATE_RESPONSE"
GENERATE_JOB_ID="$(jq -r '.job_id // empty' "$GENERATE_RESPONSE")"
[[ -n "$GENERATE_JOB_ID" ]] || { jq . "$GENERATE_RESPONSE" >&2; exit 1; }
echo "Generate job: $GENERATE_JOB_ID"

GENERATE_RESULT="$WORK_DIR/generate-full.json"
poll_job "$GENERATE_JOB_ID" "$GENERATE_RESULT" || { jq . "$GENERATE_RESULT" >&2; exit 1; }

# Preserve the generated text/document envelope as the source for the render
# audit. The exact path handles current and legacy response envelopes.
jq '(.result.data.result // .result.data.items[0].result // .result.items[0].result // .result.output // .result) | {text: (.output.text // .text // .script // .content), specscene: (.output.specscene // .specscene // {scenes: (.scenes // [])}), status: (.status // "")}' \
  "$GENERATE_RESULT" >"$WORK_DIR/generated-document.json"
echo "Generated document: $WORK_DIR/generated-document.json"
jq '{text_words: ((.text // "") | split(" ") | length), scenes: ((.specscene.scenes // []) | length)}' "$WORK_DIR/generated-document.json"

render_one() {
  local index="$1" clip_id="$2"
  render_payload="$WORK_DIR/renders/$index-payload.json"
  dispatch="$WORK_DIR/renders/$index-dispatch.json"
  full="$WORK_DIR/renders/$index-full.json"
  jq -n \
    --arg source "$clip_id" \
    --arg watermark "$WATERMARK_ASSET_ID" \
    --arg folder "$DRIVE_FOLDER_ID" \
    '{
      source_asset_id: $source,
      background: {mode: "blur_source"},
      watermark: {enabled: true, asset_id: $watermark, position: "top_right", opacity: 0.85, margin_px: 40},
      transcript: {mode: "reuse_or_generate", language: "it", persist: true},
      subtitles: {enabled: true, mode: "burn", style_id: "shorts-v1"},
      output: {contract: "velox-editing-clip-v1", width: 1080, height: 1920, fps: 30},
      audio: {mode: "copy_if_compatible"},
      destination: {drive_folder_id: $folder}
    }' >"$render_payload"
  echo "Render $((index + 1))/10: $clip_id (watermark + subtitles, 30 FPS)"
  auth_curl -X POST "$API_BASE/api/clips/render" \
    -H 'Content-Type: application/json' \
    --data-binary @"$render_payload" >"$dispatch"
  render_job_id="$(jq -r '.job_id // empty' "$dispatch")"
  [[ -n "$render_job_id" ]] || { jq . "$dispatch" >&2; exit 1; }
  poll_job "$render_job_id" "$full" || { jq . "$full" >&2; exit 1; }
  jq -e --arg clip "$clip_id" '
    ((.result.watermark.asset_id // .job.result.watermark.asset_id // "") | length) > 0
    and ((.result.render.subtitle_raster_cpu // .job.result.render.subtitle_raster_cpu) == true)
    and ((.result.contract.fps // .job.result.contract.fps) == 30)
  ' "$full" >/dev/null || { echo "render contract failed for $clip_id" >&2; exit 1; }
}

# ── 2. Render every clip with watermark + subtitles ────────────────────
# Keep a small bounded pool: NVENC is underutilized by a single filtered
# stream, while unbounded fan-out would make libass saturate the host CPU.
for ((batch_start = 0; batch_start < ${#CLIP_IDS[@]}; batch_start += RENDER_CONCURRENCY)); do
  pids=()
  for ((offset = 0; offset < RENDER_CONCURRENCY; offset++)); do
    index=$((batch_start + offset))
    (( index < ${#CLIP_IDS[@]} )) || break
    render_one "$index" "${CLIP_IDS[$index]}" &
    pids+=("$!")
  done
  for pid in "${pids[@]}"; do
    wait "$pid" || exit 1
  done
done

echo "PASS: generated document + 10 clip renders with watermark and burned subtitles"
echo "Artifacts kept in: $WORK_DIR"
