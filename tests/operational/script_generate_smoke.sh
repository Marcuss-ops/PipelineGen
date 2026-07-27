#!/usr/bin/env bash
#
# script_generate_smoke.sh — PipelineGen black-box smoke for the
# canonical /api/script/generate endpoint (GenerationEnvelopeV2).
#
# Usage:
#   ./script_generate_smoke.sh        # real run against a live server
#   ./script_generate_smoke.sh --dry # print the would-be request, exit 0
#
# Asserts the complete asynchronous contract: dispatch metadata, worker
# completion, semantic output, segment integrity, and HTTP idempotency replay.
#
# Exit codes:
#   0  every assertion passed
#   1  one or more assertions failed
#   2  setup error
#   124 overall / poll-loop timeout

set -euo pipefail

DIR=$(cd "$(dirname "$0")" && pwd)
# This battery waits for the real worker/postprocessor path. Local LLM/provider
# retries can exceed the generic smoke poll budget, so keep an explicit bounded
# budget for the full asynchronous contract.
SMOKE_TIMEOUT_SECONDS="${SMOKE_TIMEOUT_SECONDS:-900}"
SMOKE_POLL_TIMEOUT_SECONDS="${SMOKE_POLL_TIMEOUT_SECONDS:-900}"
# shellcheck disable=SC1091
source "$DIR/lib/common.sh"

if [[ "${1:-}" == "-h" || "${1:-}" == "--help" ]]; then
    sed -n '2,20p' "$0"; exit 0
fi

if [[ "$DRY_RUN" == "1" ]]; then
    cat <<EOF
DRY RUN — would POST http://${SMOKE_API_BASE}/api/script/generate
Auth (redacted): Authorization: Bearer <REDACTED>
Payload:
{
  "version": 2,
  "preset": "custom",
  "items": [
    {
      "id": "smoke-script-generate-1",
      "title": "Test generazione PipelineGen",
      "language": "it",
      "tone": "documentary",
      "source": {
        "type": "text",
        "topic": "energia solare",
        "source_text": "L'energia solare sta trasformando il modo in cui famiglie e imprese producono elettricità. I pannelli installati sui tetti permettono di ridurre i consumi provenienti dalla rete."
      },
      "script_params": {
        "target_words": 300,
        "use_memory": true
      },
      "output": {
        "extract_entities": true,
        "generate_metadata": true,
        "save_to_db": true
      }
    }
  ]
}

After dispatch would poll GET /api/jobs/<job_id>/full every ${SMOKE_POLL_INTERVAL_SECONDS}s
(cap ${SMOKE_POLL_TIMEOUT_SECONDS}s) until status is one of
  completed | failed | cancelled | dead_letter.
EOF
    exit 0
fi

# ── 1. Dispatch ─────────────────────────────────────────────────────────
smoke_log_section "POST /api/script/generate"

CASE_PREFIX="script-smoke-$(smoke_gen_uuid)"
IDEMPOTENCY_KEY="$CASE_PREFIX-key"
PAYLOAD=$(jq -n --arg case_marker "$CASE_PREFIX" \
    '{
        version: 2,
        preset: "custom",
        items: [
          {
            id: ($case_marker + "-item"),
            title: ("Test generazione PipelineGen " + $case_marker),
            language: "it",
            tone: "documentary",
            source: {
              type: "text",
              topic: ("energia solare " + $case_marker),
              source_text: ("L\u0027energia solare sta trasformando il modo in cui famiglie e imprese producono elettricità. I pannelli installati sui tetti permettono di ridurre i consumi provenienti dalla rete. " +
                "La luce del sole viene convertita in energia attraverso celle fotovoltaiche, mentre gli inverter rendono questa corrente utilizzabile nelle abitazioni. " +
                "Durante le ore centrali della giornata una casa può coprire una parte importante del proprio fabbisogno e, quando la produzione supera i consumi, l\u0027energia può essere accumulata o immessa nella rete. " +
                "L\u0027installazione richiede una valutazione dell\u0027orientamento, dell\u0027inclinazione e delle ombre presenti sul tetto. Anche la manutenzione è relativamente semplice: occorre controllare i collegamenti, mantenere puliti i moduli e verificare nel tempo le prestazioni dell\u0027impianto. " +
                "Per famiglie e imprese il vantaggio non è soltanto economico. Produrre elettricità vicino al luogo in cui viene consumata riduce le perdite di trasmissione e rende più resiliente il sistema locale. " +
                "L\u0027energia solare non elimina da sola ogni problema della transizione energetica, perché la disponibilità varia con il meteo e con le stagioni. Tuttavia, insieme a batterie, reti intelligenti e consumi più efficienti, offre uno strumento concreto per ridurre le emissioni e usare meglio le risorse. " +
                "Un progetto ben valutato parte dai consumi annuali e dalla loro distribuzione durante la giornata. Una famiglia che usa più energia al mattino o alla sera può combinare i pannelli con un sistema di accumulo, mentre un\u0027azienda può spostare alcune attività nelle ore di maggiore produzione. Queste scelte non cambiano il funzionamento delle celle, ma migliorano l\u0027uso dell\u0027energia prodotta. " +
                "La dimensione dell\u0027impianto deve essere compatibile con lo spazio disponibile, con la struttura dell\u0027edificio e con i vincoli locali. Prima dei lavori è utile controllare il tetto, i collegamenti elettrici e le eventuali ombre create da alberi o costruzioni vicine. Dopo l\u0027installazione, i dati dell\u0027inverter aiutano a individuare cali di rendimento e anomalie. " +
                "Anche la rete elettrica trae beneficio da una generazione distribuita quando la produzione e i consumi vengono coordinati. I contatori e i sistemi di gestione permettono di osservare i flussi e di programmare l\u0027accumulo. In questo modo l\u0027energia solare diventa parte di un sistema più flessibile, insieme all\u0027efficienza energetica e alle altre fonti rinnovabili. " +
                "Questo caso operativo " + $case_marker + " verifica una generazione documentaria in italiano, ancorata al tema dell\u0027energia solare e senza introdurre fonti esterne non presenti nel testo.")
            },
            script_params: {
              target_words: 600,
              skip_quality_gate: true,
              use_memory: true
            },
            output: {
              extract_entities: true,
              generate_metadata: true,
              save_to_db: true
            }
          }
        ]
    }')

# NOTE: smoke_curl sets SMOKE_LAST_HTTP/SMOKE_LAST_BODY as side-effects;
# do NOT run it inside $(...) or the exported state is lost.
export SMOKE_IDEMPOTENCY_KEY="$IDEMPOTENCY_KEY"
smoke_curl POST "/api/script/generate" --data "$PAYLOAD" >/dev/null
unset SMOKE_IDEMPOTENCY_KEY
HTTP="$SMOKE_LAST_HTTP"
if [[ "$HTTP" != "202" ]]; then
    printf '%sFAIL: dispatch returned HTTP %s (expected 202)%s\n' \
        "$RED" "$HTTP" "$RESET" >&2
    smoke_echo_safe "$(head -c 400 "$SMOKE_LAST_BODY" 2>/dev/null || true)" >&2
    exit 1
fi
printf 'dispatch HTTP: %s%s%s\n' "$YELLOW" "$HTTP" "$RESET"

JOB_ID=$(jq -r '.job_id // ""' "$SMOKE_LAST_BODY")
if [[ -z "$JOB_ID" || "$JOB_ID" == "null" ]]; then
    printf '%sFAIL: dispatch did not return a non-empty job_id%s\n' "$RED" "$RESET" >&2
    smoke_echo_safe "$(head -c 400 "$SMOKE_LAST_BODY" 2>/dev/null || true)" >&2
    exit 1
fi
DISPATCH_BODY="$SMOKE_LAST_BODY"
jq -e --arg expected "/api/jobs/${JOB_ID}/full" '
  .ok == true
  and (.status | type == "string" and length > 0)
  and .status_url == $expected
  and (.current_stage | type == "string" and length > 0)
' "$DISPATCH_BODY" >/dev/null || {
    printf '%sFAIL: dispatch missing canonical async fields or current_stage%s\n' "$RED" "$RESET" >&2
    smoke_echo_safe "$(head -c 1200 "$DISPATCH_BODY")" >&2
    exit 1
}
printf 'job_id:         %s%s%s\n' "$YELLOW" "$JOB_ID" "$RESET"

# ── 2. Poll until terminal ────────────────────────────────────────────
smoke_log_section "Poll /api/jobs/${JOB_ID}/full"

if ! smoke_poll_terminal "$JOB_ID"; then
    rc=$?
    printf '%sFAIL: polling did not reach terminal state (rc=%d, last_status=%s)%s\n' \
        "$RED" "$rc" "${SMOKE_LAST_STATUS:-?}" "$RESET" >&2
    exit 1
fi
printf 'final status:   %s%s%s\n' "$CYAN" "$SMOKE_LAST_STATUS" "$RESET"

# ── 3. Assertions ──────────────────────────────────────────────────────
if [[ "$SMOKE_LAST_STATUS" != "completed" && "$SMOKE_LAST_STATUS" != "SUCCEEDED" ]]; then
    printf '%sFAIL: job status — expected completed or SUCCEEDED, got [%s]%s\n' \
        "$RED" "$SMOKE_LAST_STATUS" "$RESET" >&2
    smoke_echo_safe "$(head -c 400 "$SMOKE_LAST_BODY" 2>/dev/null || true)" >&2
    exit 1
fi

# The job result for a single-item /api/script/generate job is nested
# under result.data.items[0].result.output. Keep a fallback to the
# legacy top-level result.output.text for backwards compatibility.
RESULT=$(jq -c '.result.data.items[0].result // .result.items[0].result // .result.output // .result // empty' "$SMOKE_LAST_BODY")
[[ -n "$RESULT" && "$RESULT" != "null" ]] || { echo "missing canonical generation result" >&2; exit 1; }
SCRIPT=$(jq -r '.output.text // .script // .text // .content // empty' <<<"$RESULT")
if [[ -z "$SCRIPT" || "$SCRIPT" == "null" ]]; then
    printf '%sFAIL: script text is empty%s\n' "$RED" "$RESET" >&2
    printf '%sResult body (first 2000 chars):%s\n' "$YELLOW" "$RESET" >&2
    smoke_echo_safe "$(head -c 2000 "$SMOKE_LAST_BODY" 2>/dev/null || true)" >&2
    exit 1
fi
printf 'script length:  %s%s%s chars\n' "$YELLOW" "${#SCRIPT}" "$RESET"

WORDS=$(printf '%s' "$SCRIPT" | wc -w | tr -d ' ')
(( WORDS >= 420 && WORDS <= 900 )) || {
    printf '%sFAIL: expected 420..900 words for target_words=600, got %s%s\n' "$RED" "$WORDS" "$RESET" >&2
    exit 1
}
printf '%s' "$SCRIPT" | grep -Eiq 'energia solare|pannelli solari|elettricità|energia rinnovabile' || {
    printf '%sFAIL: generated script is not semantically about energia solare%s\n' "$RED" "$RESET" >&2; exit 1;
}
printf '%s' "$SCRIPT" | grep -Eiq '\b(il|la|gli|della|energia|produzione|elettricità)\b' || {
    printf '%sFAIL: generated script does not contain indicative Italian language markers%s\n' "$RED" "$RESET" >&2; exit 1;
}
if printf '%s' "$SCRIPT" | grep -Fq "$(printf '\x60\x60\x60')" ||
   printf '%s' "$SCRIPT" | grep -Eiq '"(prompt|source_text|target_words)"|Ecco lo script richiesto|As an AI|Here is'; then
    printf '%sFAIL: generated script contains raw JSON, prompt instructions, or placeholder prose%s\n' "$RED" "$RESET" >&2
    exit 1
fi

if jq -e 'has("segments")' <<<"$RESULT" >/dev/null; then
    jq -e '(.segments|length)>0 and all(.segments[]; (.segment_id|type=="string" and length>0) and (.text|type=="string" and length>0) and (.text_hash|type=="string" and length>0)) and ([.segments[].segment_id]|length)==([.segments[].segment_id]|unique|length)' <<<"$RESULT" >/dev/null || {
        printf '%sFAIL: segments are empty, duplicated, or missing stable hashes%s\n' "$RED" "$RESET" >&2; exit 1;
    }
fi

WORD_COUNT=$(jq -r '.result.data.items[0].result.output.word_count // .result.output.word_count // 0' "$SMOKE_LAST_BODY")
if [[ -n "$WORD_COUNT" && "$WORD_COUNT" != "null" && ! "$WORD_COUNT" =~ ^[0-9]+$ ]]; then
    printf '%sFAIL: word_count must be a positive integer (got: %s)%s\n' \
        "$RED" "${WORD_COUNT:-<empty>}" "$RESET" >&2
    exit 1
fi
printf 'word_count:     %s%s%s\n' "$YELLOW" "$WORD_COUNT" "$RESET"

# Same key + same payload must be an HTTP replay, not a second submission.
export SMOKE_IDEMPOTENCY_KEY="$IDEMPOTENCY_KEY"
smoke_curl POST "/api/script/generate" --data "$PAYLOAD" >/dev/null
unset SMOKE_IDEMPOTENCY_KEY
[[ "$SMOKE_LAST_HTTP" == "202" ]] || { echo "FAIL: idempotency replay HTTP=$SMOKE_LAST_HTTP" >&2; exit 1; }
REPLAY_JOB=$(jq -r '.job_id // empty' "$SMOKE_LAST_BODY")
[[ "$REPLAY_JOB" == "$JOB_ID" ]] || { echo "FAIL: idempotency replay returned a different job" >&2; exit 1; }
replay_http=$(curl -sS --max-time "$SMOKE_HTTP_TIMEOUT_SECONDS" \
    -D "$WORK_DIR/replay.headers" -o /dev/null -X POST \
    -H "Authorization: Bearer $SMOKE_TOKEN" \
    -H "Idempotency-Key: $IDEMPOTENCY_KEY" \
    -H 'Content-Type: application/json' --data "$PAYLOAD" \
    -w '%{http_code}' "http://${SMOKE_API_BASE}/api/script/generate")
[[ "$replay_http" == "202" ]] || { echo "FAIL: replay header probe HTTP=$replay_http" >&2; exit 1; }
grep -Eiq '^X-Idempotency-Replay:[[:space:]]*true' "$WORK_DIR/replay.headers" || { echo "FAIL: missing X-Idempotency-Replay: true" >&2; exit 1; }

conflict_http=$(jq '.items[0].title = "Payload differente"' <<<"$PAYLOAD" | curl -sS --max-time "$SMOKE_HTTP_TIMEOUT_SECONDS" -X POST \
    -H "Authorization: Bearer $SMOKE_TOKEN" -H "Idempotency-Key: $IDEMPOTENCY_KEY" \
    -H 'Content-Type: application/json' --data-binary @- -o "$WORK_DIR/conflict.json" \
    -w '%{http_code}' "http://${SMOKE_API_BASE}/api/script/generate")
[[ "$conflict_http" == "409" ]] || { echo "FAIL: idempotency conflict HTTP=$conflict_http" >&2; exit 1; }
jq -e '.code == "IDEMPOTENCY_KEY_CONFLICT"' "$WORK_DIR/conflict.json" >/dev/null || { echo "FAIL: missing IDEMPOTENCY_KEY_CONFLICT" >&2; exit 1; }

missing_key_http=$(curl -sS --max-time "$SMOKE_HTTP_TIMEOUT_SECONDS" -X POST \
    -H "Authorization: Bearer $SMOKE_TOKEN" -H 'Content-Type: application/json' \
    --data "$PAYLOAD" -o "$WORK_DIR/missing-key.json" -w '%{http_code}' \
    "http://${SMOKE_API_BASE}/api/script/generate")
[[ "$missing_key_http" == "400" ]] || { echo "FAIL: missing Idempotency-Key HTTP=$missing_key_http" >&2; exit 1; }
jq -e '.code == "IDEMPOTENCY_KEY_REQUIRED"' "$WORK_DIR/missing-key.json" >/dev/null || { echo "FAIL: missing IDEMPOTENCY_KEY_REQUIRED" >&2; exit 1; }

printf '\n%sOK: /api/script/generate produced a non-empty script with positive word_count%s\n' \
    "$GREEN" "$RESET"
exit 0
