#!/usr/bin/env bash
# Diagnostic annotation contract test.
#
# This deliberately skips only the editorial quality gate. It does not disable
# extraction, cache, entity-image, translation, voiceover, Drive, or render
# code paths. The script is intended to certify annotation wiring, not prose
# quality.
set -euo pipefail

BASE="${BASE:-http://127.0.0.1:8000}"
DEVICE="${DEVICE:-gpu}"
CHECK_IMAGES="${CHECK_IMAGES:-1}"
CHECK_DOCS="${CHECK_DOCS:-0}"
POLL_SECONDS="${POLL_SECONDS:-2}"
POLL_LIMIT="${POLL_LIMIT:-90}"
RUN_LABEL="${RUN_LABEL:-$(date -u +%Y%m%d-%H%M%S)}"
RUN_KEY="$(printf '%s' "$RUN_LABEL" | tr -cs '[:alnum:]' '-' | sed 's/^-*//; s/-*$//')"
ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
export BASE

fail() { echo "ANNOTATIONS_E2E=FAIL: $*" >&2; exit 1; }
pass() { echo "ANNOTATIONS_E2E=$1"; }

case "$CHECK_IMAGES" in
  1|true) IMAGE_ENABLED=true ;;
  0|false) IMAGE_ENABLED=false ;;
  *) fail "CHECK_IMAGES must be 0/1 or true/false" ;;
esac

case "$CHECK_DOCS" in
  1|true)
    [[ -n "${BOXERS_DOCS_FOLDER_ID:-}" ]] || fail "BOXERS_DOCS_FOLDER_ID is required when CHECK_DOCS=1"
    DOCS_ENABLED=true
    DOCS_JSON='"docs":{"enabled":true,"languages":["it"],"folder_id":"'"${BOXERS_DOCS_FOLDER_ID}"'"},'
    SAVE_TO_DB=true
    ;;
  0|false)
    DOCS_ENABLED=false
    DOCS_JSON=''
    SAVE_TO_DB=false
    ;;
  *) fail "CHECK_DOCS must be 0/1 or true/false" ;;
esac
PAYLOAD="$(mktemp /tmp/pipelinegen-annotations-payload.XXXXXX.json)"
DISPATCH="$(mktemp /tmp/pipelinegen-annotations-dispatch.XXXXXX.json)"
FULL="$(mktemp /tmp/pipelinegen-annotations-full.XXXXXX.json)"
trap 'rm -f "$PAYLOAD" "$DISPATCH" "$FULL"' EXIT

command -v curl >/dev/null || fail "curl is required"
command -v jq >/dev/null || fail "jq is required"

curl -fsS "$BASE/health" | jq -e '.ok == true' >/dev/null || fail "health check"

if [[ "$DEVICE" == "gpu" ]]; then
  command -v python3 >/dev/null || fail "python3 is required for GPU mode"
  printf '%s' '{"text":"Mike Tyson became champion in 1986.","language":"en","entity_count":8}' \
    | python3 "$ROOT_DIR/scripts/bridges/local_nlp_gpu.py" \
    | jq -e '.persons | any(.[]; .value == "Mike Tyson")' >/dev/null \
    || fail "GPU bridge did not return Mike Tyson"
  pass "GPU_BRIDGE"
fi

cat >"$PAYLOAD" <<JSON
{
  "version": 2,
  "preset": "custom",
  "correlation_id": "annotations-hardware-${DEVICE}-${RUN_KEY}",
  "items": [{
    "id": "annotations-hardware-${DEVICE}-${RUN_KEY}",
    "title": "Mike Tyson — Annotazioni ${RUN_LABEL}",
    "language": "it",
    "source": {
      "type": "text",
      "topic": "Mike Tyson storia pugilato",
      "source_text": "Mike Tyson costruì la sua leggenda attraverso potenza esplosiva, velocità sorprendente e pressione costante. Nel 1986 conquistò il titolo WBC e a Las Vegas diventò uno dei campioni più giovani della storia del pugilato."
    },
    "script_params": {
      "target_words": 180,
      "skip_quality_gate": true,
      "segments": [
        {
          "id": "tyson-power",
          "topic": "Potenza esplosiva",
          "target_words": 60,
          "source_text": "Mike Tyson costruì la propria identità pugilistica su una potenza esplosiva riconoscibile fin dai primi incontri. La velocità dei suoi colpi gli permetteva di trasformare una breve apertura in un'azione decisiva. Non era soltanto forza fisica: Tyson avanzava con pressione costante, riduceva lo spazio e costringeva l'avversario a reagire senza tempo per organizzarsi. La combinazione tra rapidità, equilibrio e aggressività controllata rese il suo stile immediatamente riconoscibile e spiegò perché molti incontri terminassero prima del limite."
        },
        {
          "id": "tyson-title",
          "topic": "Titolo WBC nel 1986",
          "target_words": 60,
          "source_text": "Nel 1986 Mike Tyson conquistò il titolo WBC dei pesi massimi e diventò il più giovane campione della categoria. Il risultato fu il punto culminante di una crescita rapidissima, sostenuta da preparazione rigorosa e grande continuità agonistica. Il titolo trasformò un giovane pugile di grande talento in una figura centrale del pugilato mondiale. La vittoria aveva un significato concreto: confermava sul ring la superiorità tecnica e fisica che aveva già attirato l'attenzione del pubblico."
        },
        {
          "id": "tyson-las-vegas",
          "topic": "Las Vegas e la leggenda",
          "target_words": 60,
          "source_text": "Las Vegas diventò uno dei luoghi più importanti della narrazione pubblica di Mike Tyson. Le grandi arene e l'attenzione internazionale trasformavano ogni incontro in un evento seguito da milioni di spettatori. In quel contesto Tyson non rappresentava soltanto un campione: era una presenza scenica capace di alimentare attesa, timore e curiosità. La città contribuì così alla costruzione della sua leggenda, collegando i risultati sportivi alla dimensione spettacolare del pugilato professionistico."
        }
      ]
    },
    "media_plan": {
      "extraction": {
        "enabled": true,
        "strategy": "local",
        "device": "${DEVICE}",
        "max_entities_per_segment": 8,
        "max_important_phrases_per_segment": 1,
        "max_important_words_per_segment": 8,
        "entity_images": {
          "enabled": ${IMAGE_ENABLED},
          "entity_types": ["PERSON", "ORG", "GPE"],
          "max_per_entity": 1,
          "upload_to_drive": false
        }
      },
      "force_refresh_extraction": true
    },
    ${DOCS_JSON}
    "output": {
      "extract_entities": "enabled",
      "save_to_db": ${SAVE_TO_DB},
      "generate_voiceover": false
    }
  }]
}
JSON

export ANNOTATIONS_PAYLOAD="$PAYLOAD"
scripts/with-velox-auth bash -c '
  curl -sS -X POST "${BASE}/api/script/generate" \
    -H "Authorization: Bearer ${VELOX_ADMIN_TOKEN}" \
    -H "Content-Type: application/json" \
    -H "Idempotency-Key: annotations-hardware-$(date +%s%N)" \
    --data-binary @"${ANNOTATIONS_PAYLOAD}"
' >"$DISPATCH" || { cat "$DISPATCH" >&2; fail "dispatch transport failed"; }

JOB_ID="$(jq -r '.job_id // empty' "$DISPATCH")"
[[ -n "$JOB_ID" ]] || { cat "$DISPATCH" >&2; fail "dispatch did not return job_id"; }

for _ in $(seq 1 "$POLL_LIMIT"); do
  export ANNOTATIONS_JOB_ID="$JOB_ID"
  scripts/with-velox-auth bash -c '
    curl -fsS "${BASE}/api/jobs/${ANNOTATIONS_JOB_ID}/full" \
      -H "Authorization: Bearer ${VELOX_ADMIN_TOKEN}"
  ' >"$FULL" || fail "poll failed"
  STATUS="$(jq -r '.status // .job.status // .result.status // "UNKNOWN"' "$FULL")"
  case "$STATUS" in
    SUCCEEDED|COMPLETED) break ;;
    FAILED|CANCELLED|DEAD_LETTERED) jq . "$FULL" >&2; fail "job status=${STATUS}" ;;
  esac
  sleep "$POLL_SECONDS"
done

jq -e '.status == "SUCCEEDED" or .status == "COMPLETED"' "$FULL" >/dev/null \
  || fail "job did not reach success"

OUTPUT='(.result.data.items[0].result.output // .job.result.data.items[0].result.output // .result.data.output)'
jq -e "${OUTPUT} | (.specscene.scenes | length) == 3 and all(.specscene.scenes[]; (.text | length) >= 180 and (.text | test(\"Potenza esplosiva|Titolo WBC|Las Vegas\"; \"i\")))" "$FULL" >/dev/null \
  || { jq "${OUTPUT} | {scenes:(.specscene.scenes | map({segment_id, text}))}" "$FULL" >&2; fail "expected three real-text scenes"; }

jq -e "${OUTPUT} | all(.specscene.scenes[]; .annotations.version == 1 and .annotations.language == \"it\" and (.annotations.important_phrases | length) == 1)" "$FULL" >/dev/null \
  || { jq "${OUTPUT} | .specscene.scenes | map({segment_id, text, annotations})" "$FULL" >&2; fail "annotation contract or one-phrase-per-scene failed"; }

jq -e "${OUTPUT} | all(.specscene.scenes[]; .annotations as \$a | all((\$a.important_words // [])[]; . as \$w | all((\$a.important_phrases // [])[]; (.start_rune >= \$w.end_rune or .end_rune <= \$w.start_rune)) and all(((\$a.primary_entities // [])[]?.mentions // [])[]; (.start_rune >= \$w.end_rune or .end_rune <= \$w.start_rune)) and all(((\$a.secondary_entities // [])[]?.mentions // [])[]; (.start_rune >= \$w.end_rune or .end_rune <= \$w.start_rune))))" "$FULL" >/dev/null \
  || { jq "${OUTPUT} | .specscene.scenes | map({segment_id, annotations})" "$FULL" >&2; fail "important words overlap phrases or entities"; }

jq -e "${OUTPUT} | all(.specscene.scenes[]; . as \$s | all(.annotations.important_phrases[]?; .text as \$phrase | (\$phrase | length) > 0 and (\$s.text | contains(\$phrase))))" "$FULL" >/dev/null \
  || fail "important phrase is not grounded in scene text"

jq -e "${OUTPUT} | all(.specscene.scenes[]; . as \$s | all(.annotations.important_words[]?; .start_rune >= 0 and .end_rune > .start_rune and .end_rune <= (\$s.text | explode | length)))" "$FULL" >/dev/null \
  || fail "Unicode keyword offsets invalid"

python3 - "$FULL" <<'PY' || exit 1
import json
import sys

with open(sys.argv[1], encoding="utf-8") as handle:
    payload = json.load(handle)
root = payload.get("result", {}).get("data", {}).get("items", [{}])[0].get("result", {})
if not root:
    root = payload.get("job", {}).get("result", {}).get("data", {}).get("items", [{}])[0].get("result", {})
output = root.get("output") or payload.get("result", {}).get("data", {}).get("output") or {}

def check_span(scene, span, label):
    text = scene.get("text", "")
    runes = list(text)
    start = span.get("start_rune", -1)
    end = span.get("end_rune", -1)
    if start < 0 or end <= start or end > len(runes):
        raise SystemExit(f"{label}: invalid bounds {start}:{end}")
    actual = "".join(runes[start:end])
    if actual != span.get("text", ""):
        raise SystemExit(f"{label}: {actual!r} != {span.get('text')!r}")

for scene in output.get("specscene", {}).get("scenes", []):
    ann = scene.get("annotations") or {}
    for phrase in ann.get("important_phrases", []):
        check_span(scene, phrase, "important_phrase")
    for word in ann.get("important_words", []):
        check_span(scene, word, "important_word")
    for group in ("primary_entities", "secondary_entities"):
        for entity in ann.get(group, []):
            for mention in entity.get("mentions", []):
                check_span(scene, mention, f"{group}:{entity.get('canonical_name')}")
PY

jq -e "${OUTPUT} | all(.specscene.scenes[]; all(.annotations.primary_entities[]?; (.type == \"PERSON\" or .type == \"ORG\" or .type == \"GPE\")))" "$FULL" >/dev/null \
  || fail "invalid primary entity type"

if [[ "$CHECK_IMAGES" == "1" ]]; then
  jq -e "${OUTPUT} | all(.specscene.scenes[]; all(.annotations.primary_entities[]?; (.image == null) or (.image.status == \"resolved\" or .image.status == \"cached\" or .image.status == \"not_found\")))" "$FULL" >/dev/null \
    || fail "primary entity image binding has an invalid status"
  jq -e "${OUTPUT} | all(.specscene.scenes[]; all(.annotations.secondary_entities[]?; .image == null))" "$FULL" >/dev/null \
    || fail "secondary entity received an image"
fi

if [[ "$CHECK_DOCS" == "1" ]]; then
  RESULT='(.result.data.items[0].result // .job.result.data.items[0].result // .result.items[0].result // .result.data.result)'
  DOC_LINK="$(jq -r "${RESULT} | (.artifacts.document.doc_link // .documents.it.link // .output.artifacts.document.doc_link // .output.documents.it.link // empty)" "$FULL")"
  [[ "$DOC_LINK" == https://docs.google.com/document/d/* ]] \
    || { jq "${RESULT} | {script_id, artifacts, documents}" "$FULL" >&2; fail "Google Docs link missing or invalid"; }
  pass "GOOGLE_DOC"
fi

cp "$FULL" /tmp/annotations-full.json
pass "${DEVICE}_ANNOTATIONS"
echo "FULL=/tmp/annotations-full.json"
echo "JOB_ID=$JOB_ID"
