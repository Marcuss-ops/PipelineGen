#!/usr/bin/env bash
#
# script_generate_smoke.sh — PipelineGen black-box smoke for the
# canonical /api/script/generate endpoint (GenerationEnvelopeV2).
#
# Usage:
#   ./script_generate_smoke.sh        # real run against a live server
#   ./script_generate_smoke.sh --dry # print the would-be request, exit 0
#
# Asserts:
#   1. POST /api/script/generate → HTTP 202 with a non-empty job_id
#   2. GET  /api/jobs/<job_id>/full → terminal status within timeout
#   3. status == "completed"
#   4. result.script is non-empty
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
      "title": "Operational pipeline smoke",
      "language": "en",
      "tone": "explanatory",
      "source": {
        "type": "text",
        "topic": "Why automated operational pipelines matter",
        "source_text": "Automated pipelines reduce manual toil and catch regressions early."
      },
      "script_params": {
        "target_words": 200,
        "skip_quality_gate": true
      },
      "output": {
        "generate_metadata": false,
        "extract_entities": false
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

PAYLOAD=$(jq -n \
    --arg topic "Why automated operational pipelines matter" \
    --arg text "Automated pipelines reduce manual toil and catch regressions early." \
    '{
        version: 2,
        preset: "custom",
        items: [
          {
            id: "smoke-script-generate-1",
            title: "Operational pipeline smoke",
            language: "en",
            tone: "explanatory",
            source: {
              type: "text",
              topic: $topic,
              source_text: $text
            },
            script_params: {
              target_words: 200,
              skip_quality_gate: true
            },
            output: {
              generate_metadata: false,
              extract_entities: false
            }
          }
        ]
    }')

# NOTE: smoke_curl sets SMOKE_LAST_HTTP/SMOKE_LAST_BODY as side-effects;
# do NOT run it inside $(...) or the exported state is lost.
smoke_curl POST "/api/script/generate" --data "$PAYLOAD" >/dev/null
HTTP="$SMOKE_LAST_HTTP"
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
if [[ "$SMOKE_LAST_STATUS" != "completed" && "$SMOKE_LAST_STATUS" != "SUCCEEDED" ]]; then
    printf '%sFAIL: job status — expected completed or SUCCEEDED, got [%s]%s\n' \
        "$RED" "$SMOKE_LAST_STATUS" "$RESET" >&2
    smoke_echo_safe "$(head -c 400 "$SMOKE_LAST_BODY" 2>/dev/null || true)" >&2
    exit 1
fi

# The job result for a single-item /api/script/generate job is nested
# under result.data.items[0].result.output. Keep a fallback to the
# legacy top-level result.output.text for backwards compatibility.
SCRIPT=$(jq -r '.result.data.items[0].result.output.text // .result.output.text // ""' "$SMOKE_LAST_BODY")
if [[ -z "$SCRIPT" || "$SCRIPT" == "null" ]]; then
    printf '%sFAIL: script text is empty%s\n' "$RED" "$RESET" >&2
    printf '%sResult body (first 2000 chars):%s\n' "$YELLOW" "$RESET" >&2
    smoke_echo_safe "$(head -c 2000 "$SMOKE_LAST_BODY" 2>/dev/null || true)" >&2
    exit 1
fi
printf 'script length:  %s%s%s chars\n' "$YELLOW" "${#SCRIPT}" "$RESET"

WORD_COUNT=$(jq -r '.result.data.items[0].result.output.word_count // .result.output.word_count // 0' "$SMOKE_LAST_BODY")
if [[ ! "$WORD_COUNT" =~ ^[0-9]+$ ]] || (( WORD_COUNT <= 0 )); then
    printf '%sFAIL: word_count must be a positive integer (got: %s)%s\n' \
        "$RED" "${WORD_COUNT:-<empty>}" "$RESET" >&2
    exit 1
fi
printf 'word_count:     %s%s%s\n' "$YELLOW" "$WORD_COUNT" "$RESET"

printf '\n%sOK: /api/script/generate produced a non-empty script with positive word_count%s\n' \
    "$GREEN" "$RESET"
exit 0
