#!/usr/bin/env bash
# Live E2E: testo → PERSON/important_phrases → entity image catalog/Drive →
# timed OverlayPlan → Chronon overlay.render → Drive.
#
# Usage:
#   bash tests/operational/person_overlay_drive_e2e.sh
#   bash tests/operational/person_overlay_drive_e2e.sh --dry
#
# The payload deliberately uses only media_plan.extraction.include for the
# semantic switches. There is no ad-hoc protagonist/entity-images toggle.
# The background is selected in this test as a deterministic color layer, so
# a missing background asset cannot hide a failure in NLP, timing or Chronon.

set -Eeuo pipefail

DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
# Chronon can render image/text-only overlays below realtime on this host.
# Preserve an explicit caller override, but give this E2E enough time to
# observe a valid GPU job instead of reporting a false polling failure.
PERSON_OVERLAY_POLL_TIMEOUT_SECONDS="${SMOKE_POLL_TIMEOUT_SECONDS:-300}"
# shellcheck disable=SC1091
source "$DIR/lib/common.sh"
smoke_require curl jq
SMOKE_POLL_TIMEOUT_SECONDS="$PERSON_OVERLAY_POLL_TIMEOUT_SECONDS"

if [[ "${HELP_REQUESTED:-0}" == "1" ]]; then
    sed -n '1,18p' "${BASH_SOURCE[0]}"
    exit 0
fi

RESULTS_DIR="${RESULTS_DIR:-$DIR/results/person-overlay-drive}"
mkdir -p "$RESULTS_DIR"
chmod 700 "$RESULTS_DIR"

RUN_ID="person-overlay-drive-$(date -u +%Y%m%dT%H%M%SZ)-${RANDOM}"
SOURCE_TEXT="Ada Lovelace pioneered analytical computing and remains the central person in this short documentary. Her work connected mathematical reasoning with an early analytical engine and helped define an important historical phrase: the first computer program. Keep the narration factual, concise, and focused on Ada Lovelace."
PAYLOAD="$RESULTS_DIR/payload-${RUN_ID}.json"
FULL="$RESULTS_DIR/full-${RUN_ID}.json"
STATUS="$RESULTS_DIR/status-${RUN_ID}.json"

jq -n \
    --arg run_id "$RUN_ID" \
    --arg source_text "$SOURCE_TEXT" \
    '{
      version: 2,
      preset: "custom",
      correlation_id: $run_id,
      force_refresh: true,
      items: [{
        id: $run_id,
        project: "person-overlay-drive-e2e",
        title: "Ada Lovelace person overlay E2E",
        language: "en",
        tone: "clear, concise documentary narration",
        style: "Use only the supplied facts. Keep Ada Lovelace as the protagonist and do not introduce additional named people, places, organizations, dates, or events.",
        source: {
          type: "text",
          topic: "Ada Lovelace and analytical computing",
          source_text: $source_text
        },
        script_params: {
          target_words: 90,
          min_words: 50,
          segment_words: 90,
          images_per_scene: 1,
          skip_quality_gate: true,
          use_memory: false
        },
        output: {
          save_to_db: true,
          extract_entities: true,
          generate_metadata: false,
          generate_scene_images: false,
          generate_timeline: true,
          voiceover_enabled: true
        },
        overlay_background: {
          kind: "color",
          color: [0.04, 0.06, 0.12, 1],
          fit: "cover",
          opacity: 1,
          loop: false
        },
        audio: {
          mode: "COMBINED_TIMELINE",
          timing: {
            mode: "required",
            boundary: "word",
            formats: ["json", "srt", "vtt"]
          }
        },
        media_plan: {
          mode: "hybrid",
          cache: { read: true, write: true },
          provider_policy: {
            internet_images: "enabled",
            artlist: "disabled",
            image_generation: "disabled",
            youtube: "disabled"
          },
          extraction: {
            enabled: true,
            include: ["entities", "special_names", "important_phrases"],
            max_entities_per_segment: 3,
            max_important_phrases_per_segment: 3,
            max_image_queries_per_segment: 1
          },
          materialization: {
            mode: "selected",
            upload_to_drive: true,
            wait_for_ready: true
          },
          planner: { candidate_limit: 3 },
          include_trace: true
        },
        docs: { enabled: true, languages: ["en"] }
      }]
    }' > "$PAYLOAD"

if [[ "$DRY_RUN" == "1" ]]; then
    printf '%sDRY RUN%s — payload written to %s\n' "$CYAN" "$RESET" "$PAYLOAD"
    jq . "$PAYLOAD"
    exit 0
fi

fail() {
    printf '%sFAIL%s: %s\n' "$RED" "$RESET" "$1" >&2
    printf 'Artifacts: %s\n' "$RESULTS_DIR" >&2
    exit 1
}

printf '%sPOST%s /api/script/generate — run=%s\n' "$CYAN" "$RESET" "$RUN_ID"
export SMOKE_IDEMPOTENCY_KEY="$RUN_ID"
smoke_curl POST "/api/script/generate" --data-binary "@$PAYLOAD" >/dev/null
unset SMOKE_IDEMPOTENCY_KEY
cp "$SMOKE_LAST_BODY" "$RESULTS_DIR/submit-${RUN_ID}.json"

if [[ "$SMOKE_LAST_HTTP" != "200" && "$SMOKE_LAST_HTTP" != "202" ]]; then
    smoke_echo_safe "$(cat "$SMOKE_LAST_BODY")" >&2
    fail "submit HTTP $SMOKE_LAST_HTTP"
fi

JOB_ID=$(jq -r '.job_id // .id // .job.id // empty' "$SMOKE_LAST_BODY")
[[ -n "$JOB_ID" && "$JOB_ID" != "null" ]] || fail "submit response senza job_id"
printf '%sPOLL%s job=%s\n' "$CYAN" "$RESET" "$JOB_ID"

if ! smoke_poll_terminal "$JOB_ID"; then
    cp "$SMOKE_LAST_BODY" "$STATUS"
    smoke_echo_safe "$(cat "$STATUS")" >&2
    fail "poll del job fallito o HTTP $SMOKE_LAST_HTTP"
fi
cp "$SMOKE_LAST_BODY" "$STATUS"

if [[ "${SMOKE_LAST_STATUS:-}" != "completed" && "${SMOKE_LAST_STATUS:-}" != "SUCCEEDED" ]]; then
    smoke_echo_safe "$(cat "$STATUS")" >&2
    fail "job terminato con stato ${SMOKE_LAST_STATUS:-unknown}"
fi

smoke_curl GET "/api/jobs/${JOB_ID}/full" >/dev/null
cp "$SMOKE_LAST_BODY" "$FULL"
[[ "$SMOKE_LAST_HTTP" == "200" ]] || fail "GET /full HTTP $SMOKE_LAST_HTTP"

RESULT=$(jq -c '.result.data.result // .result.result // .result.data.items[0].result // .result.items[0].result // .result // empty' "$FULL")
[[ -n "$RESULT" && "$RESULT" != "null" ]] || fail "risultato generazione non presente in /full"

BACKGROUND_KIND=$(jq -r '.overlay_plan.background.kind // empty' <<<"$RESULT")
[[ "$BACKGROUND_KIND" == "color" ]] || fail "background non selezionato nel piano (kind=$BACKGROUND_KIND)"

PERSON_NAMES=$(jq -r '
  ([.entities?.persons[]?.value] +
   [.entity_timeline?.scenes[]?.entities[]? | select(.type == "PERSON") | .name])
  | map(select(type == "string" and length > 0)) | unique | join(", ")
' <<<"$RESULT")
grep -Fq "Ada Lovelace" <<<"$PERSON_NAMES" || fail "PERSON Ada Lovelace non estratta (persons=$PERSON_NAMES)"

PHRASE_COUNT=$(jq -r '
  ([.entities?.important_phrases[]?] +
   [.segments[]?.insights?.important_phrases[]?] +
   [.scenes[]?.annotations?.important_phrases[]?.text])
  | map(select(type == "string" and length > 0)) | unique | length
' <<<"$RESULT")
(( PHRASE_COUNT > 0 )) || fail "nessuna important phrase estratta/renderizzabile"

ENTITY_IMAGE_DRIVE_COUNT=$(jq -r '
  [.. | objects | .image? | objects | .drive_link? |
   select(type == "string" and startswith("http"))] | unique | length
' <<<"$RESULT")
(( ENTITY_IMAGE_DRIVE_COUNT > 0 )) || fail "immagine PERSON non materializzata/pubblicata su Drive"

OVERLAY_ITEMS=$(jq -r '
  [.overlay_plan?.items[]? |
   select((.start_ms // 0) >= 0 and (.end_ms // 0) > (.start_ms // 0))] | length
' <<<"$RESULT")
(( OVERLAY_ITEMS > 0 )) || fail "OverlayPlan senza item con timing valido"

TIMED_ITEMS=$(jq -r '
  [.overlay_plan?.items[]? |
   select((.start_us // 0) >= 0 and (.duration_us // 0) > 0)] | length
' <<<"$RESULT")
(( TIMED_ITEMS > 0 )) || fail "OverlayPlan senza timing canonico in microsecondi"

RENDER_STATUS=$(jq -r '.overlay_render?.status // empty' <<<"$RESULT")
OVERLAY_DRIVE_LINK=$(jq -r '.overlay_render?.artifact?.drive_link // empty' <<<"$RESULT")
CHRONON_VERSION=$(jq -r '.overlay_render?.artifact?.chronon_version // empty' <<<"$RESULT")
OVERLAY_SHA=$(jq -r '.overlay_render?.artifact?.sha256 // empty' <<<"$RESULT")
OVERLAY_DURATION_US=$(jq -r '.overlay_render?.artifact?.duration_us // 0' <<<"$RESULT")
[[ "$RENDER_STATUS" == "COMPLETED" || "$RENDER_STATUS" == "completed" || "$RENDER_STATUS" == "ready" ]] || fail "overlay_render non completato (status=$RENDER_STATUS)"
[[ "$OVERLAY_DRIVE_LINK" == http* ]] || fail "overlay render senza drive_link"
[[ -n "$CHRONON_VERSION" ]] || fail "artifact overlay senza chronon_version"
[[ -n "$OVERLAY_SHA" ]] || fail "artifact overlay senza sha256"
(( OVERLAY_DURATION_US > 0 )) || fail "artifact overlay senza duration_us"

SCRIPT_ID=$(jq -r '.script_id // 0' <<<"$RESULT")
(( SCRIPT_ID > 0 )) || fail "script non persistito nel database (script_id=$SCRIPT_ID)"

FINAL_AUDIO_DRIVE_LINK=$(jq -r '.final_audio?.drive_link // empty' <<<"$RESULT")
[[ "$FINAL_AUDIO_DRIVE_LINK" == http* ]] || fail "final audio senza drive_link"

printf '%sPASS%s job=%s\n' "$GREEN" "$RESET" "$JOB_ID"
printf '  PERSON: %s\n' "$PERSON_NAMES"
printf '  important_phrases: %s\n' "$PHRASE_COUNT"
printf '  entity image Drive links: %s\n' "$ENTITY_IMAGE_DRIVE_COUNT"
printf '  background: %s\n' "$BACKGROUND_KIND"
printf '  overlay items/timed: %s/%s\n' "$OVERLAY_ITEMS" "$TIMED_ITEMS"
printf '  Chronon: %s\n' "$CHRONON_VERSION"
printf '  overlay Drive: %s\n' "$OVERLAY_DRIVE_LINK"
printf '  final audio Drive: %s\n' "$FINAL_AUDIO_DRIVE_LINK"
printf '  full result: %s\n' "$FULL"
