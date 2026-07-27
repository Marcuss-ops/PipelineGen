#!/usr/bin/env bash
# ai_images_e2e.sh — one script scene bundle with AI image generation enabled.

set -euo pipefail

DIR=$(cd "$(dirname "$0")" && pwd)
SMOKE_TIMEOUT_SECONDS="${SMOKE_TIMEOUT_SECONDS:-900}"
SMOKE_POLL_TIMEOUT_SECONDS="${SMOKE_POLL_TIMEOUT_SECONDS:-900}"
# shellcheck disable=SC1091
source "$DIR/lib/common.sh"
smoke_require curl jq

CASE_PREFIX="ai-images-$(smoke_gen_uuid)"
IDEMPOTENCY_KEY="$CASE_PREFIX-cold-001"

PAYLOAD=$(jq -n --arg marker "$CASE_PREFIX" --arg item_id "$CASE_PREFIX-item" ' {
  version: 2,
  preset: "custom",
  items: [{
    id: $item_id,
    title: ("AI image scene test " + $marker),
    language: "it",
    tone: "documentary",
    source: {
      type: "text",
      topic: ("AI image scene test " + $marker),
      source_text: ("Un ricercatore osserva una mappa antica in un archivio silenzioso. " +
        "La luce radente rivela dettagli che guidano una nuova ricostruzione storica.\n\n" +
        "Il team confronta i documenti con le tracce del territorio e registra ogni evidenza.")
    },
    script_params: {target_words: 260, skip_quality_gate: true, use_memory: false},
    output: {
      extract_entities: false,
      generate_metadata: false,
      generate_scene_images: "enabled",
      save_to_db: true
    },
    media_plan: {mode: "disabled"}
  }]
}')

smoke_log_section "AI-01 one generation per SpecScene scene"
export SMOKE_IDEMPOTENCY_KEY="$IDEMPOTENCY_KEY"
smoke_curl POST "/api/script/generate" --data "$PAYLOAD" >/dev/null
unset SMOKE_IDEMPOTENCY_KEY
[[ "$SMOKE_LAST_HTTP" == "202" ]] || { echo "FAIL: dispatch HTTP=$SMOKE_LAST_HTTP" >&2; exit 1; }
JOB_ID=$(jq -r '.job_id // empty' "$SMOKE_LAST_BODY")
[[ -n "$JOB_ID" ]] || { echo "FAIL: missing job_id" >&2; exit 1; }
smoke_poll_terminal "$JOB_ID"
[[ "$SMOKE_LAST_STATUS" == "completed" || "$SMOKE_LAST_STATUS" == "SUCCEEDED" ]] || {
    echo "FAIL: job ended with $SMOKE_LAST_STATUS" >&2
    exit 1
}
smoke_curl GET "/api/jobs/$JOB_ID/full" >/dev/null
RESULT=$(jq -c '.result.data.items[0].result // .result.items[0].result // .result.output // .result // empty' "$SMOKE_LAST_BODY")
[[ -n "$RESULT" && "$RESULT" != "null" ]] || { echo "FAIL: result missing" >&2; exit 1; }

jq -e '
  (.output.specscene.scenes | length > 0)
  and ((.output.specscene.scenes | length) == ([.output.specscene.scenes[].index] | unique | length))
  and all(.output.specscene.scenes[];
    (.bindings.image | type == "object")
    and (.bindings.image.status == "generated")
    and (.bindings.image.url | type == "string" and length > 0)
  )
' <<<"$RESULT" >/dev/null || {
    echo "FAIL: every SpecScene scene must have a generated AI image binding" >&2
    jq '{script_id,output_specscene:(.output.specscene // {})}' <<<"$RESULT" >&2
    exit 1
}

printf '%sAI-01 passed: job=%s scenes=%s generated_images=%s%s\n' \
    "$GREEN" "$JOB_ID" \
    "$(jq '.output.specscene.scenes | length' <<<"$RESULT")" \
    "$(jq '[.output.specscene.scenes[].bindings.image.status] | map(select(. == "generated")) | length' <<<"$RESULT")" \
    "$RESET"
