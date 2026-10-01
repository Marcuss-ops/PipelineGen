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
# The background is the canonical Pale Olive Classic color layer, so a missing
# background asset cannot hide a failure in NLP, timing or Chronon.

set -Eeuo pipefail

DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
# Chronon can render image/text-only overlays below realtime on this host.
# Preserve an explicit caller override, but give this E2E enough time to
# observe a valid GPU job instead of reporting a false polling failure.
PERSON_OVERLAY_POLL_TIMEOUT_SECONDS="${SMOKE_POLL_TIMEOUT_SECONDS:-300}"
# A completed script job can precede its asynchronous Drive outbox receipts.
# Give the complete smoke (render + eventual publication + Docs checks) room to
# finish even when the renderer uses the full poll window.
SMOKE_TIMEOUT_SECONDS="${SMOKE_TIMEOUT_SECONDS:-600}"
OVERLAY_DRIVE_LINK_TIMEOUT_SECONDS="${OVERLAY_DRIVE_LINK_TIMEOUT_SECONDS:-120}"
# Warm leg by default. The certification corpus was previously cold by
# construction because this flag was hard-coded to true, which made the
# cross-run cache unauditable: a run could never show a cache hit, and every
# recorded timing was inflated by forced re-extraction. FORCE_REFRESH=true
# restores the deliberate cold run when that is the thing being measured.
FORCE_REFRESH="${FORCE_REFRESH:-false}"
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
    --arg force_refresh "$FORCE_REFRESH" \
    '{
      version: 2,
      preset: "custom",
      correlation_id: $run_id,
      force_refresh: ($force_refresh == "true"),
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
          voiceover_enabled: true,
          render: { enabled: true }
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

GENERATED_TEXT=$(jq -r '(.output?.text // .script?.text // .text // empty)' <<<"$RESULT")
(( ${#GENERATED_TEXT} >= 80 )) || fail "testo generato assente o troppo corto"
grep -Fqi "Ada Lovelace" <<<"$GENERATED_TEXT" || fail "testo generato senza Ada Lovelace"

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

PHRASE_OVERLAY_ITEMS=$(jq -r '
  [.overlay_plan?.items[]? |
   select((.kind == "text_phrase" or .template_id == "IMPORTANT_PHRASE") and
          ((.preset_id // "") | length > 0) and
          ((.start_ms // 0) >= 0 and (.end_ms // 0) > (.start_ms // 0)))] | length
' <<<"$RESULT")
(( PHRASE_OVERLAY_ITEMS > 0 )) || fail "nessun important_phrase con preset e timing nel piano"

TIMED_ITEMS=$(jq -r '
  [.overlay_plan?.items[]? |
   select((.start_us // 0) >= 0 and (.duration_us // 0) > 0)] | length
' <<<"$RESULT")
(( TIMED_ITEMS > 0 )) || fail "OverlayPlan senza timing canonico in microsecondi"

RENDER_STATUS=$(jq -r '.overlay_render?.status // empty' <<<"$RESULT")
[[ "$RENDER_STATUS" == "COMPLETED" || "$RENDER_STATUS" == "completed" || "$RENDER_STATUS" == "ready" ]] || fail "overlay_render non completato (status=$RENDER_STATUS)"

# Drive publication is deliberately asynchronous. The artifact's embedded
# drive_link is only populated by synchronous publication; the durable outbox
# records ordinary per-item receipts in result.overlay_links after the job
# result commits. Wait for every rendered item and validate those canonical
# receipts instead of treating the intentionally empty artifact field as a
# publication failure.
SOURCE_LANGUAGE=$(jq -r '.source_language // .overlay_plan?.language // "en"' <<<"$RESULT")
OVERLAY_ITEM_IDS=$(jq -c '
  (.overlay_render?.items // [] | map(.item_id // empty) | map(select(type == "string" and length > 0))) as $rendered
  | if ($rendered | length) > 0 then $rendered
    else [.overlay_plan?.items[]? |
      select((.template_id // "" | ascii_upcase) != "BACKGROUND" and
             (.template_id // "" | ascii_upcase) != "VIDEO_BACKGROUND") |
      .id | select(type == "string" and length > 0)]
    end
' <<<"$RESULT")
OVERLAY_ITEM_COUNT=$(jq -r 'length' <<<"$OVERLAY_ITEM_IDS")
(( OVERLAY_ITEM_COUNT > 0 )) || fail "overlay render senza item pubblicabili"
# Folder routing is owned by the application and may create/reuse a
# per-project folder. Only pin an ID when the caller explicitly requests it.
EXPECTED_OVERLAY_DRIVE_FOLDER="${EXPECTED_OVERLAY_DRIVE_FOLDER:-}"
OVERLAY_LINK_DEADLINE=$(( $(date +%s) + OVERLAY_DRIVE_LINK_TIMEOUT_SECONDS ))
OVERLAY_LINKS_READY=0
while (( $(date +%s) < OVERLAY_LINK_DEADLINE )); do
    OVERLAY_LINKS=$(jq -c '.overlay_links // []' <<<"$RESULT")
    if jq -e --arg language "$SOURCE_LANGUAGE" --arg folder "$EXPECTED_OVERLAY_DRIVE_FOLDER" --argjson item_ids "$OVERLAY_ITEM_IDS" '
      . as $links
      | all($item_ids[];
          . as $item_id
          | any($links[]?;
              .item_id == $item_id and .language == $language and
              ((.drive_link // "") | startswith("http")) and
              ((.drive_folder_id // "") | length > 0) and
              ($folder == "" or .drive_folder_id == $folder)))
      and ([ $links[]? | select(.language == $language and (.item_id as $id | ($item_ids | index($id)) != null)) | .drive_folder_id ] | unique | length) == 1
    ' <<<"$OVERLAY_LINKS" >/dev/null; then
        OVERLAY_LINKS_READY=1
        break
    fi
    smoke_wallclock_check
    sleep "${SMOKE_POLL_INTERVAL_SECONDS:-2}"
    smoke_curl GET "/api/jobs/${JOB_ID}/full" >/dev/null
    [[ "$SMOKE_LAST_HTTP" == "200" ]] || fail "GET /full durante attesa link Drive HTTP $SMOKE_LAST_HTTP"
    cp "$SMOKE_LAST_BODY" "$FULL"
    RESULT=$(jq -c '.result.data.result // .result.result // .result.data.items[0].result // .result.items[0].result // .result // empty' "$FULL")
    [[ -n "$RESULT" && "$RESULT" != "null" ]] || fail "risultato generazione scomparso durante attesa link Drive"
done
(( OVERLAY_LINKS_READY == 1 )) || fail "link Drive overlay asincroni incompleti entro ${OVERLAY_DRIVE_LINK_TIMEOUT_SECONDS}s (attesi $OVERLAY_ITEM_COUNT item)"
OVERLAY_LINKS=$(jq -c '.overlay_links // []' <<<"$RESULT")
FIRST_OVERLAY_ITEM_ID=$(jq -r '.[0]' <<<"$OVERLAY_ITEM_IDS")
OVERLAY_DRIVE_LINK=$(jq -r --arg language "$SOURCE_LANGUAGE" --arg item_id "$FIRST_OVERLAY_ITEM_ID" '
  (.overlay_render?.artifact?.drive_link // "") as $artifact_link
  | if ($artifact_link | startswith("http")) then $artifact_link
    else ([.overlay_links[]? | select(.language == $language and .item_id == $item_id) | .drive_link] | .[0] // "") end
' <<<"$RESULT")
OVERLAY_DRIVE_FOLDER=$(jq -r --arg language "$SOURCE_LANGUAGE" --arg item_id "$FIRST_OVERLAY_ITEM_ID" '
  (.overlay_render?.artifact?.drive_folder_id // "") as $artifact_folder
  | if $artifact_folder != "" then $artifact_folder
    else ([.overlay_links[]? | select(.language == $language and .item_id == $item_id) | .drive_folder_id] | .[0] // "") end
' <<<"$RESULT")
CHRONON_VERSION=$(jq -r '.overlay_render?.artifact?.chronon_version // empty' <<<"$RESULT")
OVERLAY_SHA=$(jq -r '.overlay_render?.artifact?.sha256 // empty' <<<"$RESULT")
OVERLAY_DURATION_US=$(jq -r '.overlay_render?.artifact?.duration_us // 0' <<<"$RESULT")
[[ "$OVERLAY_DRIVE_LINK" == http* ]] || fail "nessun link Drive valido nell'overlay artifact o nella ricevuta outbox"
[[ -n "$OVERLAY_DRIVE_FOLDER" ]] || fail "ricevuta overlay senza drive_folder_id"
if [[ -n "$EXPECTED_OVERLAY_DRIVE_FOLDER" && "$OVERLAY_DRIVE_FOLDER" != "$EXPECTED_OVERLAY_DRIVE_FOLDER" ]]; then
    fail "overlay render pubblicato nella cartella errata (folder=$OVERLAY_DRIVE_FOLDER expected=$EXPECTED_OVERLAY_DRIVE_FOLDER)"
fi
[[ -n "$CHRONON_VERSION" ]] || fail "artifact overlay senza chronon_version"
[[ -n "$OVERLAY_SHA" ]] || fail "artifact overlay senza sha256"
(( OVERLAY_DURATION_US > 0 )) || fail "artifact overlay senza duration_us"

# Production lowers the semantic timeline into one short transparent video
# per overlay item; these clips are composited over the master audio/video by
# the downstream editor and are NOT full-length master renders. Certify every
# child artifact is present with a positive duration bounded by the maximum
# eight-second composite window plus frame/mux quantization slack.
OVERLAY_RENDERED_ARTIFACTS=$(jq -c '
  if (.overlay_render?.items // [] | length) > 0 then
    [.overlay_render.items[]?.artifact? | select(type == "object")]
  elif (.overlay_render?.artifact? | type) == "object" then
    [.overlay_render.artifact]
  else [] end
' <<<"$RESULT")
OVERLAY_RENDERED_ARTIFACT_COUNT=$(jq -r 'length' <<<"$OVERLAY_RENDERED_ARTIFACTS")
(( OVERLAY_RENDERED_ARTIFACT_COUNT == OVERLAY_ITEM_COUNT )) || fail "artifact overlay certificati=$OVERLAY_RENDERED_ARTIFACT_COUNT, item renderizzati=$OVERLAY_ITEM_COUNT"
if ! jq -e 'all(.[]; (.duration_us // 0) > 0 and (.duration_us // 0) <= 8500000)' <<<"$OVERLAY_RENDERED_ARTIFACTS" >/dev/null; then
    fail "uno o più overlay artifact hanno durata nulla o superano gli 8s del composito"
fi
AUDIO_DURATION_US=$(jq -r '(.final_audio?.duration_us // ((.final_audio?.duration_ms // 0) * 1000))' <<<"$RESULT")
(( AUDIO_DURATION_US > 0 )) || fail "risultato senza durata audio canonica"

GPU_VULKAN_FRAMES=$(jq -r '.overlay_render?.artifact?.metrics?.chronon_job_gpu_vulkan_frames // 0' <<<"$RESULT")
GPU_NVENC_FRAMES=$(jq -r '.overlay_render?.artifact?.metrics?.chronon_job_gpu_nvenc_frames // 0' <<<"$RESULT")
GPU_SOFTWARE_FRAMES=$(jq -r '.overlay_render?.artifact?.metrics?.chronon_job_gpu_software_encode_frames // 0' <<<"$RESULT")
GPU_READBACK_BYTES=$(jq -r '.overlay_render?.artifact?.metrics?.chronon_job_gpu_gpu_readback_bytes // 0' <<<"$RESULT")
OVERLAY_FRAME_COUNT=$(jq -r '.overlay_render?.artifact?.frame_count // 0' <<<"$RESULT")
(( OVERLAY_FRAME_COUNT > 0 )) || fail "artifact overlay senza frame_count"
(( GPU_VULKAN_FRAMES == OVERLAY_FRAME_COUNT )) || fail "Chronon non ha certificato Vulkan su tutti i frame (vulkan=$GPU_VULKAN_FRAMES frames=$OVERLAY_FRAME_COUNT)"
(( GPU_NVENC_FRAMES == OVERLAY_FRAME_COUNT )) || fail "Chronon non ha certificato NVENC su tutti i frame (nvenc=$GPU_NVENC_FRAMES frames=$OVERLAY_FRAME_COUNT)"
(( GPU_SOFTWARE_FRAMES == 0 )) || fail "Chronon ha usato software encode ($GPU_SOFTWARE_FRAMES frame)"
(( GPU_READBACK_BYTES == 0 )) || fail "Chronon ha usato readback GPU→CPU ($GPU_READBACK_BYTES bytes)"

SCRIPT_ID=$(jq -r '.script_id // 0' <<<"$RESULT")
(( SCRIPT_ID > 0 )) || fail "script non persistito nel database (script_id=$SCRIPT_ID)"

FINAL_AUDIO_DRIVE_LINK=$(jq -r '.final_audio?.drive_link // empty' <<<"$RESULT")
[[ "$FINAL_AUDIO_DRIVE_LINK" == http* ]] || fail "final audio senza drive_link"

DOC_LINK=$(jq -r '.documents?.en?.link // empty' <<<"$RESULT")
[[ "$DOC_LINK" == https://docs.google.com/document/* ]] || fail "Docs endpoint senza link documento"
DOC_ID=$(sed -nE 's#^https://docs\.google\.com/document/d/([^/]+)/.*#\1#p' <<<"$DOC_LINK")
[[ -n "$DOC_ID" ]] || fail "impossibile estrarre document_id dal link Docs"
DOC_HTML="$RESULTS_DIR/document-${RUN_ID}.html"
curl -L --max-time "$SMOKE_HTTP_TIMEOUT_SECONDS" -sS \
    "https://docs.google.com/document/d/${DOC_ID}/export?format=html" \
    -o "$DOC_HTML" || fail "export del Google Doc fallito"
grep -Fq "Semantic Overlay" "$DOC_HTML" || fail "Docs HTML senza sezione Semantic Overlay"
grep -Fq "Semantic Overlay JSON" "$DOC_HTML" || fail "Docs HTML senza JSON overlay associato"
grep -Fq "person:ada-lovelace" "$DOC_HTML" || fail "Docs HTML senza identità canonica Ada Lovelace"
grep -Fq "Rendered Overlay JSON" "$DOC_HTML" || fail "Docs HTML senza JSON dell'artefatto Chronon"
grep -Fq "Entities" "$DOC_HTML" || fail "Docs HTML senza sezione Entities"
grep -Fq "Person:" "$DOC_HTML" || fail "Docs HTML senza riga Person compatta"
if grep -Fq "<img" "$DOC_HTML"; then
    fail "Docs HTML contiene ancora un'immagine inline nell'entity summary"
fi

printf '%sPASS%s job=%s\n' "$GREEN" "$RESET" "$JOB_ID"
printf '  PERSON: %s\n' "$PERSON_NAMES"
printf '  generated text chars: %s\n' "${#GENERATED_TEXT}"
printf '  important_phrases: %s\n' "$PHRASE_COUNT"
printf '  entity image Drive links: %s\n' "$ENTITY_IMAGE_DRIVE_COUNT"
printf '  background: %s\n' "$BACKGROUND_KIND"
printf '  overlay items/timed: %s/%s\n' "$OVERLAY_ITEMS" "$TIMED_ITEMS"
printf '  overlay Drive receipts: %s/%s\n' "$OVERLAY_ITEM_COUNT" "$(jq -r --arg language "$SOURCE_LANGUAGE" '[.[] | select(.language == $language and ((.drive_link // "") | startswith("http")))] | length' <<<"$OVERLAY_LINKS")"
printf '  phrase preset items: %s\n' "$PHRASE_OVERLAY_ITEMS"
printf '  GPU Vulkan/NVENC/software/readback: %s/%s/%s/%s\n' "$GPU_VULKAN_FRAMES" "$GPU_NVENC_FRAMES" "$GPU_SOFTWARE_FRAMES" "$GPU_READBACK_BYTES"
printf '  Chronon: %s\n' "$CHRONON_VERSION"
printf '  overlay Drive: %s\n' "$OVERLAY_DRIVE_LINK"
printf '  overlay Drive folder: %s\n' "$OVERLAY_DRIVE_FOLDER"
printf '  short overlay artifacts: %s (first duration: %s us)\n' "$OVERLAY_RENDERED_ARTIFACT_COUNT" "$OVERLAY_DURATION_US"
printf '  final audio Drive: %s\n' "$FINAL_AUDIO_DRIVE_LINK"
printf '  Docs: %s\n' "$DOC_LINK"
printf '  full result: %s\n' "$FULL"
