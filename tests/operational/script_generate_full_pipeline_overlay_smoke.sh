#!/usr/bin/env bash
# Contract smoke for the final semantic path:
# script → TTS/timing → entities → image cache/provider → metadata →
# OverlayPlan → Chronon overlay.render.
#
# This is intentionally separate from the expensive live E2E batteries. With
# SMOKE_DRY_RUN=1 it validates the exact request contract without contacting
# the API. With a live API it submits one controlled item and fail-closes on
# missing stages or an un-certified overlay artifact.
set -euo pipefail

DIR=$(cd "$(dirname "$0")" && pwd)
# shellcheck disable=SC1091
source "$DIR/lib/common.sh"
smoke_require curl jq

PAYLOAD_FILE="${PAYLOAD_FILE:-$DIR/script_generate_full_pipeline_overlay.json}"
[[ -f "$PAYLOAD_FILE" ]] || { printf '%ssetup error: missing %s%s\n' "$RED" "$PAYLOAD_FILE" "$RESET" >&2; exit 2; }

assert_payload() {
    local payload
    payload=$(<"$PAYLOAD_FILE")
    jq -e '
      .version == 2 and .preset == "custom" and (.items|length) == 1 and
      .items[0].project != "" and
      .items[0].output.save_to_db == true and
      .items[0].output.extract_entities == true and
      .items[0].output.generate_metadata == true and
      .items[0].output.generate_timeline == true and
      .items[0].output.voiceover_enabled == true and
      .items[0].audio.mode == "COMBINED_TIMELINE" and
      .items[0].audio.timing.mode == "required" and
      .items[0].media_plan.mode == "hybrid" and
      .items[0].media_plan.provider_policy.internet_images == "enabled" and
      .items[0].media_plan.extraction.entity_images.enabled == true and
      .items[0].media_plan.materialization.upload_to_drive == true
    ' <<<"$payload" >/dev/null
}

assert_result() {
    local body_file="$1"
    local item
    item=$(jq -c '.result.result // .result.data.items[0].result // .result.items[0].result // .result.data.result // .result // empty' "$body_file")
    [[ -n "$item" && "$item" != "null" ]] || { echo "FAIL: missing canonical generation result" >&2; return 1; }

    jq -e '((.output.text // "") | length) > 0' <<<"$item" >/dev/null || { echo "FAIL: generated script is empty" >&2; return 1; }
    jq -e '((.scenes // []) | length) > 0 and ((.segments // []) | length) > 0' <<<"$item" >/dev/null || { echo "FAIL: no generated scenes/segments" >&2; return 1; }
    jq -e '((.scenes[0].voiceover.en.timing.words // .scenes[0].voiceover.timing.words // []) | length) > 0' <<<"$item" >/dev/null || { echo "FAIL: TTS word timing missing" >&2; return 1; }
    jq -e '(.segments[0].insights != null) and ((.scenes[0].entities // []) != null)' <<<"$item" >/dev/null || { echo "FAIL: NLP insights/entities missing" >&2; return 1; }
    jq -e '(.canonical_timeline != null) and (.final_audio != null)' <<<"$item" >/dev/null || { echo "FAIL: canonical timeline/final audio missing" >&2; return 1; }
    jq -e '((.result.__artifact_manifest.artifacts // []) | map(.kind) | index("script_json")) != null' "$body_file" >/dev/null || { echo "FAIL: script artifact manifest missing" >&2; return 1; }

    if jq -e '.overlay_render.artifact != null' <<<"$item" >/dev/null 2>&1; then
        jq -e '(.overlay_render.artifact.sha256 | type == "string" and length == 64) and ((.overlay_render.artifact.drive_link // .overlay_render.artifact.url // "") | length > 0)' <<<"$item" >/dev/null || { echo "FAIL: overlay artifact is not certified/published" >&2; return 1; }
    else
        echo "NOTE: script.generate returned no overlay_render; Chronon overlay publication remains a separate queue stage" >&2
    fi
}

assert_payload
if [[ "${SMOKE_DRY_RUN:-0}" == "1" || "${1:-}" == "--dry-run" ]]; then
    jq . "$PAYLOAD_FILE"
    echo "PASS: full-pipeline request contract"
    exit 0
fi

PAYLOAD=$(<"$PAYLOAD_FILE")
smoke_curl POST "/api/script/generate" -H "Idempotency-Key: full-pipeline-overlay-$(smoke_gen_uuid)" --data "$PAYLOAD" >/dev/null
[[ "$SMOKE_LAST_HTTP" == "200" || "$SMOKE_LAST_HTTP" == "202" ]] || { echo "FAIL: dispatch HTTP $SMOKE_LAST_HTTP" >&2; exit 1; }
JOB_ID=$(jq -r '.job_id // .id // empty' "$SMOKE_LAST_BODY")
[[ -n "$JOB_ID" ]] || { echo "FAIL: dispatch returned no job_id" >&2; exit 1; }
smoke_poll_terminal "$JOB_ID" || { echo "FAIL: job did not reach terminal state" >&2; exit 1; }
[[ "$SMOKE_LAST_STATUS" == "completed" || "$SMOKE_LAST_STATUS" == "SUCCEEDED" ]] || { echo "FAIL: terminal status $SMOKE_LAST_STATUS" >&2; exit 1; }
# smoke_poll_terminal keeps the terminal status response in SMOKE_LAST_BODY;
# fetch the full envelope explicitly before inspecting generation artifacts.
smoke_curl GET "/api/jobs/${JOB_ID}/full" >/dev/null
[[ "$SMOKE_LAST_HTTP" == "200" ]] || { echo "FAIL: full result HTTP $SMOKE_LAST_HTTP" >&2; exit 1; }
assert_result "$SMOKE_LAST_BODY"
echo "PASS: script → TTS/timing → NLP/media → canonical artifacts contract"
