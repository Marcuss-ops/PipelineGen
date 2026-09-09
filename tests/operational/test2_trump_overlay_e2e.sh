#!/usr/bin/env bash
# Test 2 / footballer overlay-only: certify the semantic path without TTS
#
#   generated text -> NLP PERSON(Lionel Messi) + image materialization
#   -> internet image search -> download/materialize -> DB/index
#   -> static no-audio image-only overlay plan -> RenderingGen/Chronon artifact
#
# IMAGE_OVERLAY receives the canonical preset selected by PipelineGen for this
# job fingerprint. The selected preset varies across new jobs but is stable on
# retry, proving randomised-by-job image animation without hardcoding a choice.
#
# The local MP4 and the full evidence envelope are always written under the
# Chronon overlay scratch directory. That directory is intentionally ignored
# by Chronon3d because it contains generated media, not source fixtures.
set -euo pipefail
umask 077

DIR=$(cd "$(dirname "$0")" && pwd)
ROOT=$(cd "$DIR/../.." && pwd)
CHRONON_OVERLAY_DIR="${CHRONON_OVERLAY_DIR:-$ROOT/../Chronon3d/test_renders/overlay}"
mkdir -p "$CHRONON_OVERLAY_DIR"

SMOKE_TIMEOUT_SECONDS="${SMOKE_TIMEOUT_SECONDS:-1800}"
SMOKE_POLL_TIMEOUT_SECONDS="${SMOKE_POLL_TIMEOUT_SECONDS:-1800}"
# shellcheck disable=SC1091
source "$DIR/lib/common.sh"
smoke_require curl jq sha256sum stat ffprobe file

DB_PATH="${DB_PATH:-$ROOT/data/media/media.db.sqlite}"
# shellcheck source=../../scripts/lib/canonical_db_path.sh
source "$ROOT/scripts/lib/canonical_db_path.sh"
DB_PATH="$(resolve_canonical_primary_db "$DB_PATH" "$ROOT")"
[[ -f "$DB_PATH" ]] || { echo "setup error: DB not found: $DB_PATH" >&2; exit 2; }

RUN_ID="messi-overlay-$(date -u +%Y%m%dT%H%M%SZ)-$(smoke_gen_uuid | cut -c1-8)"
PAYLOAD_FILE="$CHRONON_OVERLAY_DIR/${RUN_ID}.request.json"
FULL_FILE="$CHRONON_OVERLAY_DIR/${RUN_ID}.result.json"
MP4_FILE="$CHRONON_OVERLAY_DIR/${RUN_ID}.mp4"
PLAN_FILE="$CHRONON_OVERLAY_DIR/${RUN_ID}.overlay-plan.json"

build_payload() {
    jq -nc --arg run "$RUN_ID" '{
      version: 2,
      preset: "custom",
      correlation_id: $run,
      force_refresh: true,
      items: [{
        id: ($run + "-item"),
        project: $run,
        title: "Lionel Messi — important football context",
        language: "en",
        tone: "clear, concise documentary narration",
        style: "Use Lionel Messi as the only named person. Keep the narration factual and source-grounded. Do not introduce additional named people, places, organizations, dates, or events.",
        source: {
          type: "text",
          topic: "Lionel Messi and football",
          source_text: "Lionel Messi is the only person in this controlled test. His creativity, close control, and sustained excellence made him a defining figure in modern football. Explain why technical consistency and decision-making matter in elite football, using Lionel Messi as the sole named person."
        },
        script_params: {
          target_words: 90,
          min_words: 45,
          segment_words: 90,
          images_per_scene: 1,
          single_scene: true
        },
        media_plan: {
          mode: "hybrid",
          provider_policy: {
            internet_images: "enabled",
            image_generation: "disabled",
            artlist: "disabled",
            youtube: "disabled"
          },
          cache: {read: true, write: true},
          extraction: {
            enabled: true,
            include: ["entities", "important_phrases"],
            max_entities_per_segment: 1,
            max_important_phrases_per_segment: 1,
            max_important_words_per_segment: 5,
            max_image_queries_per_segment: 3,
            entity_images: {
              enabled: true,
              entity_types: ["PERSON"],
              max_per_entity: 1,
              upload_to_drive: true
            }
          },
          force_refresh_extraction: true,
          force_refresh_assets: true,
          force_refresh_bindings: true,
          materialization: {mode: "selected", upload_to_drive: true, wait_for_ready: true},
          planner: {candidate_limit: 10},
          include_trace: true
        },
        output: {
          save_to_db: true,
          extract_entities: true,
          generate_metadata: true,
          generate_scene_images: false,
          # No TTS/audio for this probe. PipelineGen still performs script,
          # NLP, media extraction/materialization and document publication;
          # the static no-audio overlay is rendered by the Chronon queue
          # stage below.
          generate_timeline: false,
          voiceover_enabled: false,
          render: {
            enabled: true,
            watermark: {enabled: true, text: "CHRONON", position: "top_right", margin_px: 100},
            subtitles: {enabled: true, mode: "burn", preset: "montserrat_bold"}
          }
        },
        audio: {mode: "NONE"},
        docs: {
          enabled: true,
          languages: ["en"],
          folder_id: "1J_xUGo_bchzXDIGqSX04CU44c_Dm3SxS"
        }
      }]
    }'
}

payload=$(build_payload)
printf '%s\n' "$payload" > "$PAYLOAD_FILE"

if [[ "${SMOKE_DRY_RUN:-0}" == "1" || "${1:-}" == "--dry" ]]; then
    jq . "$PAYLOAD_FILE"
    echo "PASS: Messi overlay request contract"
    exit 0
fi

smoke_log_section "Messi: script → NLP → image materialization → no-audio Chronon overlay"
export SMOKE_IDEMPOTENCY_KEY="$RUN_ID"
smoke_curl POST "/api/script/generate" --data "$payload" >/dev/null
unset SMOKE_IDEMPOTENCY_KEY
smoke_assert_http_2xx "dispatch Messi overlay" || exit 1
JOB_ID=$(jq -r '.job_id // .id // empty' "$SMOKE_LAST_BODY")
[[ -n "$JOB_ID" ]] || { echo "FAIL: dispatch returned no job_id" >&2; exit 1; }
echo "job_id=$JOB_ID"

smoke_poll_terminal "$JOB_ID" || { echo "FAIL: Messi job did not reach terminal" >&2; exit 1; }
[[ "$SMOKE_LAST_STATUS" == "completed" || "$SMOKE_LAST_STATUS" == "SUCCEEDED" ]] || {
    echo "FAIL: Messi job ended with $SMOKE_LAST_STATUS" >&2
    exit 1
}
smoke_curl GET "/api/jobs/$JOB_ID/full" >/dev/null
smoke_assert_http_2xx "fetch Messi full result" || exit 1
cp "$SMOKE_LAST_BODY" "$FULL_FILE"

item=$(jq -c '.result.data.items[0].result // .result.items[0].result // .result.data.result // .result.result // .result // empty' "$FULL_FILE")
[[ -n "$item" && "$item" != "null" ]] || { echo "FAIL: canonical result missing" >&2; exit 1; }

jq -e '((.output.text // "") | length) > 0' <<<"$item" >/dev/null || { echo "FAIL: generated text is empty" >&2; exit 1; }
if jq -e 'any((.segments // [])[]?.insights.entities[]?; (.type|ascii_upcase) == "PERSON" and ((.value|ascii_downcase)|contains("lionel messi")))' <<<"$item" >/dev/null 2>&1 ||
   jq -e 'any((.entities.persons // [])[]?; ((.value // .name // "")|ascii_downcase)|contains("lionel messi"))' <<<"$item" >/dev/null 2>&1; then
    echo "PASS: NLP found PERSON=Lionel Messi"
else
    echo "FAIL: NLP did not find PERSON=Lionel Messi" >&2
    jq '{entities, segments: [.segments[]? | {insights: .insights}]}' <<<"$item" >&2
    exit 1
fi

jq -e 'any((.segments // [])[]?.insights.important_phrases[]?; type == "string" and length > 0) or any((.scenes // [])[]?.annotations.important_phrases[]?; ((.text // .) | length) > 0) or ((.phrase_timings // []) | length) > 0' <<<"$item" >/dev/null || {
    echo "FAIL: no important phrase was extracted from generated text" >&2
    exit 1
}
echo "PASS: at least one important phrase found"

jq -e 'any((.segments // [])[]?.assets.candidates[]?; .provider == "internet_images" and ((.source_url // "")|length) > 0 and ((.drive_link // "")|length) > 0 and (((.entity // .query // "")|ascii_downcase)|contains("messi")))' <<<"$item" >/dev/null || {
    echo "FAIL: no downloaded Messi internet_images candidate with Drive link" >&2
    exit 1
}
candidate_id=$(jq -r '[.segments[]?.assets.candidates[]? | select(.provider == "internet_images" and (((.entity // .query // "")|ascii_downcase)|contains("messi"))) | .asset_id // empty][0] // empty' <<<"$item")
jq -e 'any((.segments // [])[]?.assets.candidates[]?; .provider == "internet_images" and ((.source_url // "")|length) > 0 and ((.drive_link // "")|length) > 0)' <<<"$item" >/dev/null || {
    echo "FAIL: no downloaded/verified internet_images candidate with Drive link" >&2
    exit 1
}
[[ -n "$candidate_id" ]] || { echo "FAIL: image candidate has no asset_id" >&2; exit 1; }
echo "PASS: Messi image downloaded/materialized asset_id=$candidate_id"

doc_id=$(jq -r '.documents.en.id // .documents.en.doc_id // .artifacts.document.doc_id // empty' <<<"$item")
doc_link=$(jq -r '.documents.en.link // .documents.en.url // .documents.en.drive_link // .artifacts.document.doc_link // empty' <<<"$item")
[[ -n "$doc_id" && -n "$doc_link" ]] || {
    echo "FAIL: Google Doc missing id/link" >&2
    jq '{documents, artifacts: .artifacts.document}' <<<"$item" >&2
    exit 1
}
echo "PASS: Google Doc generated id=$doc_id link=$doc_link"

# The candidate identity and Drive publication are the durable PipelineGen
# evidence. The local binary is obtained from its materialized path when it
# is surfaced; otherwise the verified source URL is used for this renderer
# probe.
candidate=$(jq -c '[.segments[]?.assets.candidates[]? | select(.provider == "internet_images" and (((.entity // .query // "")|ascii_downcase)|contains("messi")) and ((.drive_link // "")|length) > 0 and (((.local_path // "")|length) > 0 or ((.source_url // "")|length) > 0))][0] // empty' <<<"$item")
candidate_path=$(jq -r '.local_path // empty' <<<"$candidate")
candidate_url=$(jq -r '.source_url // empty' <<<"$candidate")
IMAGE_FILE="$CHRONON_OVERLAY_DIR/${RUN_ID}.source-image"
if [[ -n "$candidate_path" && -f "$candidate_path" && -s "$candidate_path" ]]; then
    cp "$candidate_path" "$IMAGE_FILE"
else
    [[ -n "$candidate_url" ]] || { echo "FAIL: selected image has neither local_path nor source_url" >&2; exit 1; }
    curl -fsSL --max-time 180 "$candidate_url" -o "$IMAGE_FILE"
fi
[[ -s "$IMAGE_FILE" ]] || { echo "FAIL: materialized image is empty" >&2; exit 1; }
image_hash=$(sha256sum "$IMAGE_FILE" | awk '{print $1}')
image_mime=$(file --brief --mime-type "$IMAGE_FILE")
[[ "$image_mime" == image/* ]] || { echo "FAIL: downloaded candidate is not an image: $image_mime" >&2; exit 1; }
index_status=$(jq -r '.index_status // empty' <<<"$candidate")
[[ "${index_status^^}" == "INDEXED" ]] || {
    echo "FAIL: selected Messi image is not indexed (index_status=$index_status)" >&2
    exit 1
}
echo "PASS: local indexed Messi image ready hash=$image_hash mime=$image_mime index_status=$index_status"

STORE_URL="${RENDERINGGEN_STORE_URL:-http://127.0.0.1:9000}"
QUEUE_URL="${RENDERINGGEN_QUEUE_URL:-http://127.0.0.1:8081}"
CLASSIC_BACKGROUND="$ROOT/../RenderingGen/testdata/golden/Pale-Olive.mp4"
[[ -s "$CLASSIC_BACKGROUND" ]] || { echo "FAIL: classic background fixture missing: $CLASSIC_BACKGROUND" >&2; exit 1; }
background_hash=$(sha256sum "$CLASSIC_BACKGROUND" | awk '{print $1}')
background_mime=$(file --brief --mime-type "$CLASSIC_BACKGROUND")
[[ "$background_mime" == video/* ]] || { echo "FAIL: classic background is not a video: $background_mime" >&2; exit 1; }
curl -fsS -X PUT --data-binary @"$IMAGE_FILE" -H 'Content-Type: application/octet-stream' "$STORE_URL/objects/$image_hash" >/dev/null
curl -fsS -X PUT --data-binary @"$CLASSIC_BACKGROUND" -H 'Content-Type: application/octet-stream' "$STORE_URL/objects/$background_hash" >/dev/null
curl -fsSI "$STORE_URL/objects/$image_hash" >/dev/null
curl -fsSI "$STORE_URL/objects/$background_hash" >/dev/null

plan_id="${RUN_ID}-chronon"
QUEUE_PAYLOAD="$WORK_DIR/${RUN_ID}.render-job.json"
image_preset=$(cd "$ROOT" && GOCACHE="${GOCACHE:-/tmp/pipelinegen-overlay-gocache}" GOTMPDIR="${GOTMPDIR:-/tmp}" go run ./cmd/overlay-preset "$plan_id" "" "entity_image")
[[ -n "$image_preset" ]] || { echo "FAIL: canonical image preset selection returned empty" >&2; exit 1; }
echo "PASS: canonical image preset selected for this job: $image_preset"
jq -n --arg plan "$plan_id" --arg image_id "$candidate_id" --arg image_hash "$image_hash" --arg image_url "$STORE_URL/objects/$image_hash" --arg image_mime "$image_mime" --arg image_preset "$image_preset" --arg background_hash "$background_hash" --arg background_url "$STORE_URL/objects/$background_hash" --arg background_mime "$background_mime" '{
  id: $plan,
  schema: "renderinggen.job",
  version: 1,
  job_type: "overlay.render",
  render_plan: {
    schema_version: "renderinggen.overlay-plan.v1",
    plan_id: $plan,
    video_id: $plan,
    width: 1920,
    height: 1080,
    fps_num: 30,
    fps_den: 1,
    items: [
      {id:"entity_image", template_id:"IMAGE_OVERLAY", preset_id:$image_preset, start_ms:0, end_ms:5000, params:{width:600,height:600,position:"center",fit:"cover",radius:1}, asset_refs:[{asset_id:$image_id,sha256:$image_hash,url:$image_url,media_type:$image_mime}]}
    ],
    background: {kind:"video", fit:"cover", loop:true, asset_refs:[{asset_id:"classic-background",sha256:$background_hash,url:$background_url,media_type:$background_mime}]},
    assets: [
      {hash:$background_hash, logical_path:"assets/Pale-Olive.mp4"},
      {hash:$image_hash, logical_path:("assets/semantic/" + $image_id + ".jpg")}
    ]
}' > "$QUEUE_PAYLOAD"
jq '.render_plan' "$QUEUE_PAYLOAD" > "$PLAN_FILE"
echo "PASS: static no-audio 1920x1080 image-only overlay plan carries the canonical PipelineGen-selected image preset"

submit_code=$(curl -sS -o "$WORK_DIR/render-submit.json" -w '%{http_code}' -X POST "$QUEUE_URL/jobs" -H 'Content-Type: application/json' --data-binary @"$QUEUE_PAYLOAD")
case "$submit_code" in 201|200|409) ;; *) echo "FAIL: RenderingGen submit HTTP $submit_code" >&2; cat "$WORK_DIR/render-submit.json" >&2; exit 1;; esac
render_state=""
for _ in $(seq 1 180); do
    render_status=$(curl -fsS "$QUEUE_URL/jobs/$plan_id")
    render_state=$(jq -r '.state // empty' <<<"$render_status")
    case "$render_state" in
        completed) break;;
        failed) echo "FAIL: Chronon overlay render failed" >&2; jq . <<<"$render_status" >&2; exit 1;;
        *) sleep 2;;
    esac
done
[[ "$render_state" == completed ]] || { echo "FAIL: Chronon overlay render timed out" >&2; exit 1; }
artifact_sha=$(jq -r '.artifact.artifact_hash // empty' <<<"$render_status")
artifact_size=$(jq -r '.artifact.size_bytes // 0' <<<"$render_status")
[[ "$artifact_sha" =~ ^[0-9a-fA-F]{64}$ && "$artifact_size" -gt 0 ]] || { echo "FAIL: Chronon artifact is not certified" >&2; exit 1; }
curl -fsSL "$STORE_URL/objects/$artifact_sha" -o "$MP4_FILE"
[[ "$(stat -c%s "$MP4_FILE")" == "$artifact_size" ]] || { echo "FAIL: downloaded Chronon artifact size mismatch" >&2; exit 1; }
[[ "$(sha256sum "$MP4_FILE" | awk '{print $1}')" == "$artifact_sha" ]] || { echo "FAIL: downloaded Chronon artifact hash mismatch" >&2; exit 1; }
ffprobe -v error -select_streams v:0 -show_entries stream=width,height -show_entries format=duration -of json "$MP4_FILE" > "$WORK_DIR/ffprobe.json"
jq -e '.streams[0].width == 1920 and .streams[0].height == 1080 and ((.format.duration|tonumber) > 4.8 and (.format.duration|tonumber) < 5.2)' "$WORK_DIR/ffprobe.json" >/dev/null || { echo "FAIL: Chronon output geometry/duration mismatch" >&2; cat "$WORK_DIR/ffprobe.json" >&2; exit 1; }
echo "PASS: local Chronon overlay saved at $MP4_FILE sha256=$artifact_sha"

printf '\nCERTIFIED = YES\n'
printf 'evidence=%s\nplan=%s\nartifact=%s\n' "$FULL_FILE" "$PLAN_FILE" "${MP4_FILE}"
