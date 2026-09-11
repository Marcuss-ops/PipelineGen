#!/usr/bin/env bash
# Live E2E: five PERSON entities -> verified image materialization ->
# entity image overlay plan -> RenderingGen/Chronon -> Drive.

set -Eeuo pipefail

DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
# shellcheck disable=SC1091
source "$DIR/lib/common.sh"
smoke_require curl jq
# common.sh installs a 120s default for ordinary smoke probes. This canary
# includes TTS plus a real GPU render, so its own default must cover the full
# end-to-end path; callers can still shorten/extend it explicitly.
SMOKE_POLL_TIMEOUT_SECONDS="${JORDAN_SMOKE_POLL_TIMEOUT_SECONDS:-900}"

if [[ "${HELP_REQUESTED:-0}" == "1" ]]; then
    sed -n '1,12p' "${BASH_SOURCE[0]}"
    exit 0
fi

RESULTS_DIR="${RESULTS_DIR:-$DIR/results/jordan-entity-overlay-drive}"
mkdir -p "$RESULTS_DIR"
chmod 700 "$RESULTS_DIR"

RUN_ID="jordan-entity-overlay-drive-$(date -u +%Y%m%dT%H%M%SZ)-${RANDOM}"
DRIVE_FOLDER_ID="1J_xUGo_bchzXDIGqSX04CU44c_Dm3SxS"
SOURCE_TEXT='Michael Jordan became a defining figure in basketball because his career combined elite scoring, defensive intensity, competitive focus, and a public standard of preparation. He was born in Brooklyn and grew up in Wilmington, where sport became a daily discipline rather than a shortcut to fame. His early development was shaped by repetition, physical conditioning, and the pressure of learning to compete against stronger opponents. Those lessons later became part of the story told about his professional career.

The history of basketball began decades earlier when James Naismith designed an indoor game that could keep students active during winter. The original experiment was simple, but its structure created a sport in which coordination, spacing, passing, and decision-making mattered as much as strength. Over time the game changed from a local activity into an international spectacle. The evolution of the sport gave exceptional players a stage on which individual skill could influence an entire team and, eventually, an entire culture.

At the University of North Carolina, Dean Smith helped Jordan turn raw athletic ability into a more complete understanding of basketball. Smith emphasized responsibility, team movement, and the idea that a decisive play should serve the group rather than merely advertise the player. Jordan responded to that environment by improving his footwork, reading defensive schemes, and learning how to remain effective when an opponent removed his first option. The result was a foundation built on both instinct and deliberate study.

When Jordan entered the professional game, he immediately attracted attention for his ability to change the rhythm of a contest. He could attack the basket, create space in the midrange, finish through contact, and use his length to disrupt an opposing offense. His influence was not limited to highlight plays. He raised the expectations placed on training, recovery, concentration, and accountability. Teammates and opponents understood that a close game could become a test of endurance as much as a test of tactics.

The Chicago years also showed that a great scorer needed an equally strong structure around him. Scottie Pippen became a versatile partner who could defend multiple positions, advance the ball, create opportunities, and take responsibility when Jordan faced a double team. Their partnership worked because it joined different strengths instead of asking every player to imitate the same style. Pippen gave the team length, anticipation, and connective play, while Jordan supplied pressure at the most important moments.

Phil Jackson later guided the group through a system that used spacing, cutting, patience, and trust. His coaching asked players to recognize the whole floor and to make decisions before the defense could settle. Jackson did not remove Jordan’s individual authority; he placed it inside a larger pattern so that every possession could produce several threats. The approach helped transform talent into repeatable execution and allowed the team to handle long series, hostile arenas, injuries, and the mental strain of expectation.

Jordan’s public image grew alongside his results. Fans saw the championships, the final shots, the defensive possessions, and the visible refusal to treat an important moment as ordinary. Critics also examined the commercial side of his fame, the pressure of constant comparison, and the cost of making excellence look effortless. His story therefore includes both performance and representation: he became an athlete, a symbol of competitive ambition, and a reference point for later generations trying to define what leadership in sport could mean.

The lasting significance of Jordan is not that every player should copy his personality or career path. It is that his example made preparation, accountability, and decisive action central parts of the basketball conversation. Naismith supplied the game’s basic structure, Smith contributed a framework for learning, Pippen demonstrated complementary excellence, and Jackson organized collective intelligence. Jordan connected those lessons to a standard that audiences could recognize instantly. The history of basketball is broader than one person, but his career remains one of its clearest case studies in how skill, environment, partnership, coaching, and pressure can combine into cultural impact.'

PAYLOAD="$RESULTS_DIR/payload-${RUN_ID}.json"
FULL="$RESULTS_DIR/full-${RUN_ID}.json"
STATUS="$RESULTS_DIR/status-${RUN_ID}.json"

jq -n \
    --arg run_id "$RUN_ID" \
    --arg source_text "$SOURCE_TEXT" \
    --arg folder_id "$DRIVE_FOLDER_ID" \
    '{
      version: 2,
      preset: "custom",
      correlation_id: $run_id,
      force_refresh: true,
      items: [{
        id: $run_id,
        project: "jordan-entity-overlay-drive-e2e",
        title: "Michael Jordan entity image overlay canary",
        language: "en",
        tone: "clear, factual documentary narration",
        style: "Use only the supplied facts. Do not introduce named people beyond those present in the source. Preserve the five-entity extraction boundary and keep the narration factual.",
        source: {
          type: "text",
          topic: "Michael Jordan and the history of basketball",
          source_text: $source_text
        },
        script_params: {
          target_words: 430,
          min_words: 300,
          segment_words: 430,
          single_scene: true,
          images_per_scene: 5,
          skip_quality_gate: true,
          use_memory: false
        },
        output: {
          save_to_db: true,
          extract_entities: true,
          generate_metadata: false,
          generate_scene_images: false,
          generate_timeline: true,
          voiceover_enabled: true,
          render: { enabled: true, drive_folder_id: $folder_id },
          drive_folder_id: $folder_id
        },
        overlay_background: {
          kind: "color",
          color: [238/255, 241/255, 231/255, 1],
          fit: "cover",
          opacity: 1,
          loop: false
        },
        audio: {
          mode: "COMBINED_TIMELINE",
          timing: { mode: "required", boundary: "word", formats: ["json", "srt", "vtt"] }
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
            include: ["entities", "special_names"],
            max_entities_per_segment: 5,
            max_image_queries_per_segment: 5,
            entity_images: {
              enabled: true,
              entity_types: ["PERSON"],
              max_per_entity: 1,
              upload_to_drive: true
            }
          },
          materialization: {
            mode: "selected",
            upload_to_drive: true,
            wait_for_ready: true
          },
          force_refresh_extraction: true,
          force_refresh_assets: true,
          force_refresh_bindings: true,
          planner: { candidate_limit: 5 },
          include_trace: true
        },
        docs: { enabled: true, languages: ["en"], folder_id: $folder_id }
      }]
    }' > "$PAYLOAD"

if [[ "$DRY_RUN" == "1" ]]; then
    printf '%sDRY RUN%s — payload scritto in %s\n' "$CYAN" "$RESET" "$PAYLOAD"
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
[[ "$SMOKE_LAST_HTTP" == "200" || "$SMOKE_LAST_HTTP" == "202" ]] || fail "submit HTTP $SMOKE_LAST_HTTP"

JOB_ID=$(jq -r '.job_id // .id // .job.id // empty' "$SMOKE_LAST_BODY")
[[ -n "$JOB_ID" && "$JOB_ID" != "null" ]] || fail "submit response senza job_id"
printf '%sPOLL%s job=%s\n' "$CYAN" "$RESET" "$JOB_ID"
if ! smoke_poll_terminal "$JOB_ID"; then
    cp "$SMOKE_LAST_BODY" "$STATUS"
    fail "poll job fallito o HTTP $SMOKE_LAST_HTTP"
fi
cp "$SMOKE_LAST_BODY" "$STATUS"
[[ "${SMOKE_LAST_STATUS:-}" == "completed" || "${SMOKE_LAST_STATUS:-}" == "SUCCEEDED" ]] || fail "job terminato con stato ${SMOKE_LAST_STATUS:-unknown}"

smoke_curl GET "/api/jobs/${JOB_ID}/full" >/dev/null
cp "$SMOKE_LAST_BODY" "$FULL"
[[ "$SMOKE_LAST_HTTP" == "200" ]] || fail "GET /full HTTP $SMOKE_LAST_HTTP"
RESULT=$(jq -c '.result.data.result // .result.result // .result.data.items[0].result // .result.items[0].result // .result // empty' "$FULL")
[[ -n "$RESULT" && "$RESULT" != "null" ]] || fail "risultato non presente in /full"

PERSON_NAMES=$(jq -r '
  ([.entities?.persons[]?.value] + [.segments[]?.insights?.entities[]? | select(.type == "PERSON") | .value] + [.overlay_plan?.items[]? | select(.kind == "entity_card") | .text])
  | map(select(type == "string" and length > 0)) | unique | .[]
' <<<"$RESULT")
PERSON_COUNT=$(printf '%s\n' "$PERSON_NAMES" | sed "/^$/d" | wc -l | tr -d ' ')
(( PERSON_COUNT == 5 )) || fail "PERSON uniche=$PERSON_COUNT, attese 5: $(tr '\n' ', ' <<<"$PERSON_NAMES")"

IMAGE_BINDINGS=$(jq -r '
  [.scenes[]?.annotations?.primary_entities[]?.image? // empty]
  | map(select(.status == "resolved" and ((.drive_link // "") | startswith("http")))) | length
' <<<"$RESULT")
(( IMAGE_BINDINGS == 5 )) || fail "binding immagine Drive risolti=$IMAGE_BINDINGS, attesi 5"

ENTITY_ITEMS=$(jq -r '[.overlay_plan?.items[]? | select(.kind == "entity_card" and (.asset_refs | length) > 0 and (.image_preset_id // "") != "")] | length' <<<"$RESULT")
(( ENTITY_ITEMS == 5 )) || fail "layer entity immagine renderizzabili=$ENTITY_ITEMS, attesi 5"

BACKGROUND=$(jq -c '.overlay_plan?.background // {}' <<<"$RESULT")
EXPECTED_BACKGROUND='[0.9333333333333333,0.9450980392156862,0.9058823529411765,1]'
[[ "$(jq -c '.kind' <<<"$BACKGROUND")" == '"color"' ]] || fail "background non color: $BACKGROUND"
[[ "$(jq -c '.color' <<<"$BACKGROUND")" == "$EXPECTED_BACKGROUND" ]] || fail "background non Pale Olive Classic: $BACKGROUND"

PRESETS=$(jq -r '[.overlay_plan.items[]? | select(.kind == "entity_card") | .image_preset_id] | join(", ")' <<<"$RESULT")
[[ -n "$PRESETS" ]] || fail "image_preset_id mancanti"

RENDER_STATUS=$(jq -r '.overlay_render?.status // empty' <<<"$RESULT")
OVERLAY_LINK=$(jq -r '.overlay_render?.artifact?.drive_link // empty' <<<"$RESULT")
[[ "$RENDER_STATUS" == "COMPLETED" || "$RENDER_STATUS" == "completed" || "$RENDER_STATUS" == "ready" ]] || fail "overlay_render non completato: $RENDER_STATUS"
[[ "$OVERLAY_LINK" == http* ]] || fail "overlay render senza drive_link"

printf '%sPASS%s job=%s\n' "$GREEN" "$RESET" "$JOB_ID"
printf '  PERSON (5):\n%s\n' "$PERSON_NAMES"
printf '  entity image bindings Drive: %s/5\n' "$IMAGE_BINDINGS"
printf '  entity image overlay layers: %s/5\n' "$ENTITY_ITEMS"
printf '  image presets: %s\n' "$PRESETS"
printf '  background: Pale Olive Classic %s\n' "$BACKGROUND"
printf '  RenderingGen: %s\n' "$RENDER_STATUS"
printf '  overlay Drive: %s\n' "$OVERLAY_LINK"
printf '  target Drive folder: https://drive.google.com/drive/folders/%s\n' "$DRIVE_FOLDER_ID"
printf '  full result: %s\n' "$FULL"
