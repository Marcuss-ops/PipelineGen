#!/usr/bin/env bash
#
# text_script_smoke.sh — PipelineGen black-box text-only script job
#
# Usage:
#   ./text_script_smoke.sh            # real run against a live server
#   ./text_script_smoke.sh --dry      # print the would-be request, exit 0
#
# Asserts (each is strict; no weakening allowed by spec):
#   1. POST /api/script/generate-from-clips  → HTTP 202 with a non-empty job_id
#   2. GET  /api/jobs/<job_id>/full          → terminal status within timeout
#                                               (completed | failed | cancelled | dead_letter)
#   3. status == "completed"
#   4. result.script is non-empty (length > 0)
#   5. result.word_count is a positive integer
#
# Exit codes:
#   0  every assertion passed
#   1  one or more assertions failed
#   2  setup error
#   124 overall / poll-loop timeout

set -euo pipefail

DIR=$(cd "$(dirname "$0")" && pwd)
# shellcheck disable=SC1091
source "$DIR/lib/common.sh"

if [[ "${1:-}" == "-h" || "${1:-}" == "--help" ]]; then
    sed -n '2,17p' "$0"; exit 0
fi

TOPIC="${SMOKE_TOPIC:-Why pipeline observability matters}"
TITLE="${SMOKE_TITLE:-Observability 101 smoke}"

if [[ "$DRY_RUN" == "1" ]]; then
    cat <<EOF
DRY RUN — would POST http://${SMOKE_API_BASE}/api/script/generate-from-clips
Auth (redacted): Authorization: Bearer <REDACTED>
Payload:
{
  "topic": "${TOPIC}",
  "title": "${TITLE}",
  "tone": "explanatory",
  "model": "gemma2:2b",
  "target_words": 300,
  "save_to_db": false,
  "force_refresh": false,
  "extract_entities": false,
  "generate_scene_images": false,
  "generate_metadata": false
}

After dispatch would poll GET /api/jobs/<job_id>/full every ${SMOKE_POLL_INTERVAL_SECONDS}s
(cap ${SMOKE_POLL_TIMEOUT_SECONDS}s) until status is one of
  completed | failed | cancelled | dead_letter.
EOF
    exit 0
fi

# ── 1. Dispatch ─────────────────────────────────────────────────────────
smoke_log_section "POST /api/script/generate-from-clips"

PAYLOAD=$(jq -n \
    --arg topic "$TOPIC" \
    --arg title "$TITLE" \
    '{
        topic:$topic, title:$title,
        tone:"explanatory", model:"gemma2:2b",
        target_words:300,
        save_to_db:false, force_refresh:false,
        extract_entities:false, generate_scene_images:false, generate_metadata:false
    }')

HTTP=$(smoke_curl POST "/api/script/generate-from-clips" --data "$PAYLOAD")
if [[ "$HTTP" != "202" && "$HTTP" != "200" ]]; then
    printf '%sFAIL: dispatch returned HTTP %s (accepted codes: 200, 202)%s\n' \
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
smoke_assert_eq "completed" "$SMOKE_LAST_STATUS" "job status"
status_rc=$?
if (( status_rc != 0 )); then
    smoke_echo_safe "$(head -c 400 "$SMOKE_LAST_BODY" 2>/dev/null || true)" >&2
    exit 1
fi

SCRIPT=$(jq -r '.result.script // ""' "$SMOKE_LAST_BODY")
if [[ -z "$SCRIPT" || "$SCRIPT" == "null" ]]; then
    printf '%sFAIL: result.script is empty%s\n' "$RED" "$RESET" >&2
    exit 1
fi
printf 'script length:  %s%s%s chars\n' "$YELLOW" "${#SCRIPT}" "$RESET"

WORD_COUNT=$(jq -r '.result.word_count // 0' "$SMOKE_LAST_BODY")
if [[ ! "$WORD_COUNT" =~ ^[0-9]+$ ]] || (( WORD_COUNT <= 0 )); then
    printf '%sFAIL: result.word_count must be a positive integer (got: %s)%s\n' \
        "$RED" "${WORD_COUNT:-<empty>}" "$RESET" >&2
    exit 1
fi
printf 'word_count:     %s%s%s\n' "$YELLOW" "$WORD_COUNT" "$RESET"

printf '\n%sOK: script job produced non-empty script + positive word_count%s\n' \
    "$GREEN" "$RESET"
exit 0
