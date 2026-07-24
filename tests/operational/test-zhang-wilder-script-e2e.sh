#!/usr/bin/env bash
#
# test-zhang-wilder-script-e2e.sh — PipelineGen black-box engine-only E2E
# for /api/script/generate using a documentary-style multi-segment payload
# (Zhilei Zhang vs Deontay Wilder — neutral technical boxing analysis).
#
# Purpose:
#   Validate the script generation engine end-to-end WITHOUT involving any
#   external dependency chain (no images, no voiceover, no scene assets,
#   no Drive document, no rendering). This isolates the LLM + worker +
#   dispatcher from provider orchestration so regressions can be attributed
#   to a single layer quickly. Re-enable providers in a follow-up probe
#   only after this engine-only E2E passes locally.
#
# Usage:
#   ./test-zhang-wilder-script-e2e.sh
#
# Asserts:
#   1. POST /api/script/generate → HTTP 200 or 202 with non-empty job_id
#   2. GET  /api/jobs/<id>/full   → terminal status within SMOKE_POLL_TIMEOUT_SECONDS
#   3. status is `completed` or `SUCCEEDED`
#   4. result.output.text is non-empty AND contains ≥ 250 real words
#   5. "Zhang" and "Wilder" appear (case-insensitive)
#   6. No leaked internal markers:
#        - "SEGMENT "
#        - "Source text:"
#        - "schema_version"
#        - "specscene"
#        - "clip_id"
#
# Tunables (all overridable via env):
#   API_BASE                    host:port         (default 127.0.0.1:${VELOX_PORT:-8080})
#   VELOX_ADMIN_TOKEN / TOKEN_FILE                 bearer token (mandatory)
#   SMOKE_TIMEOUT_SECONDS        per-script wall clock (default 180; we tighten to 600)
#   SMOKE_POLL_TIMEOUT_SECONDS   poll loop ceiling  (default 120)
#   SMOKE_POLL_INTERVAL_SECONDS  poll sleep         (default 2)
#   SMOKE_OUTPUT_DIR             override script artifact dir (default /tmp/zhang-wilder-e2e-<RUN_ID>)
#
# Exit codes (consistent with lib/common.sh):
#   0   every assertion passed
#   1   one or more assertions failed
#   2   setup error (missing binary, missing token, unknown flag)
#   124 polling or overall timeout exceeded

set -euo pipefail

DIR=$(cd "$(dirname "$0")" && pwd)
# shellcheck disable=SC1091
source "$DIR/lib/common.sh"

# This script needs curl beyond what smoke_curl auto-stamps (we want to
# forward an explicit X-Request-Id header in addition to Idempotency-Key).
smoke_require curl

# Tighten the script-level wall clock so a stuck LLM cannot consume the
# generic SMOKE_TIMEOUT_SECONDS window shared across the suite.
SMOKE_DEADLINE=$(( $(date +%s) + 600 ))

# ── 1. Server health ─────────────────────────────────────────────────────
smoke_log_section "GET /health"
smoke_curl GET "/health" >/dev/null
smoke_assert_http_2xx "GET /health" || exit 1
printf 'health:          %sOK%s\n' "$GREEN" "$RESET"

# ── 2. Dispatch ──────────────────────────────────────────────────────────
smoke_log_section "POST /api/script/generate"

RUN_ID="$(date +%Y%m%d-%H%M%S)"
ITEM_ID="zhang-wilder-${RUN_ID}"
REQ_ID="zhang-wilder-${RUN_ID}"

# Multi-segment payload. Every source_text is intentionally neutral; the
# prompts DO NOT pre-suppose dates, results, rankings, injuries, private
# statements or quotations so that any leakage in the output must come from
# the engine itself, not the upstream prompt.
PAYLOAD=$(jq -n \
    --arg id "$ITEM_ID" \
    '{
        version: 2,
        preset: "custom",
        items: [
          {
            id: $id,
            title: "Zhilei Zhang vs Deontay Wilder: Power, Pressure and Tactical Keys",
            language: "en",
            tone: "documentary",
            source: {
              type: "text",
              topic: "Technical boxing analysis of Zhilei Zhang and Deontay Wilder",
              source_text: "Create a neutral documentary-style boxing analysis using only the supplied segment information. Do not invent dates, results, rankings, injuries, private statements or quotations."
            },
            script_params: {
              target_words: 700,
              segments: [
                {
                  topic: "Opening",
                  source_text: "Zhilei Zhang and Deontay Wilder represent two dangerous heavyweight styles built around timing, reach and knockout power.",
                  target_words: 100
                },
                {
                  topic: "Deontay Wilder profile",
                  source_text: "Deontay Wilder fights from an orthodox stance and is especially dangerous when he creates distance for his straight right hand.",
                  target_words: 110
                },
                {
                  topic: "Zhilei Zhang profile",
                  source_text: "Zhilei Zhang is a southpaw heavyweight who uses patient pressure, counterpunching and compact combinations.",
                  target_words: 110
                },
                {
                  topic: "Technical matchup",
                  source_text: "The orthodox versus southpaw matchup places additional importance on lead-foot positioning, distance control and the battle between the straight punches.",
                  target_words: 130
                },
                {
                  topic: "Tactical keys",
                  source_text: "Wilder needs space, disciplined movement and opportunities for the right hand. Zhang needs controlled pressure, defensive awareness and chances to counter as Wilder resets.",
                  target_words: 140
                },
                {
                  topic: "Conclusion",
                  source_text: "The matchup is defined by which boxer controls range, remains patient and lands the first clean power punch without becoming vulnerable to a counter.",
                  target_words: 110
                }
              ]
            },
            output: {
              generate_document: false,
              generate_scene_images: false,
              generate_voiceover: false,
              generate_metadata: false,
              extract_entities: false
            }
          }
        ]
    }')

# smoke_curl auto-stamps `Idempotency-Key` for non-GET requests; we forward
# an extra `X-Request-Id` for end-to-end correlation with server-side logs.
smoke_curl POST "/api/script/generate" \
    --data "$PAYLOAD" \
    -H "X-Request-Id: $REQ_ID" >/dev/null
HTTP="$SMOKE_LAST_HTTP"
if [[ "$HTTP" != "200" && "$HTTP" != "202" ]]; then
    printf '%sFAIL: dispatch returned HTTP %s (accepted codes: 200, 202)%s\n' \
        "$RED" "$HTTP" "$RESET" >&2
    smoke_echo_safe "$(head -c 400 "$SMOKE_LAST_BODY" 2>/dev/null || true)" >&2
    exit 1
fi
printf 'dispatch HTTP:    %s%s%s\n' "$YELLOW" "$HTTP" "$RESET"
printf 'X-Request-Id:     %s%s%s\n' "$YELLOW" "$REQ_ID" "$RESET"

JOB_ID=$(jq -r '.job_id // ""' "$SMOKE_LAST_BODY")
if [[ -z "$JOB_ID" || "$JOB_ID" == "null" ]]; then
    printf '%sFAIL: dispatch did not return a non-empty job_id%s\n' "$RED" "$RESET" >&2
    smoke_echo_safe "$(head -c 400 "$SMOKE_LAST_BODY" 2>/dev/null || true)" >&2
    exit 1
fi
printf 'job_id:           %s%s%s\n' "$YELLOW" "$JOB_ID" "$RESET"

# ── 3. Poll until terminal ──────────────────────────────────────────────
smoke_log_section "Poll /api/jobs/${JOB_ID}/full"
if ! smoke_poll_terminal "$JOB_ID"; then
    rc=$?
    printf '%sFAIL: polling did not reach terminal state (rc=%d, last_status=%s)%s\n' \
        "$RED" "$rc" "${SMOKE_LAST_STATUS:-?}" "$RESET" >&2
    exit 1
fi
printf 'final status:     %s%s%s\n' "$CYAN" "$SMOKE_LAST_STATUS" "$RESET"

if [[ "$SMOKE_LAST_STATUS" != "completed" && "$SMOKE_LAST_STATUS" != "SUCCEEDED" ]]; then
    printf '%sFAIL: job status — expected completed or SUCCEEDED, got [%s]%s\n' \
        "$RED" "$SMOKE_LAST_STATUS" "$RESET" >&2
    smoke_echo_safe "$(head -c 400 "$SMOKE_LAST_BODY" 2>/dev/null || true)" >&2
    exit 1
fi

# ── 4. Output extraction ────────────────────────────────────────────────
# Mirrors the dual-path read used by script_generate_smoke.sh: nested under
# items first (current schema), legacy top-level fallback second.
TEXT=$(jq -r '.result.data.items[0].result.output.text // .result.output.text // ""' "$SMOKE_LAST_BODY")
if [[ -z "$TEXT" || "$TEXT" == "null" ]]; then
    printf '%sFAIL: script text is empty%s\n' "$RED" "$RESET" >&2
    smoke_echo_safe "$(head -c 2000 "$SMOKE_LAST_BODY" 2>/dev/null || true)" >&2
    exit 1
fi

API_WORD_COUNT=$(jq -r '.result.data.items[0].result.output.word_count // .result.output.word_count // 0' "$SMOKE_LAST_BODY")

# Persist artefacts under a stable, run-stamped directory. SMOKE_OUTPUT_DIR
# lets callers override the path (e.g. inside a probe harness).
OUT_BASE="${SMOKE_OUTPUT_DIR:-/tmp/zhang-wilder-e2e-${RUN_ID}}"
mkdir -p "$OUT_BASE"
chmod 700 "$OUT_BASE"
printf '%s' "$REQ_ID" > "$OUT_BASE/request_id.txt"
printf '%s' "$JOB_ID" > "$OUT_BASE/job_id.txt"
printf '%s\n' "$TEXT" > "$OUT_BASE/zhang-vs-wilder.txt"
chmod 600 "$OUT_BASE/zhang-vs-wilder.txt"

# ── 5. Validation ───────────────────────────────────────────────────────
REAL_WORD_COUNT=$(wc -w < "$OUT_BASE/zhang-vs-wilder.txt" | tr -d ' ')
printf 'script length:    %s%s%s chars\n' "$YELLOW" "${#TEXT}" "$RESET"
printf 'API word count:   %s%s%s\n' "$YELLOW" "${API_WORD_COUNT:-?}" "$RESET"
printf 'real word count:  %s%s%s\n' "$YELLOW" "$REAL_WORD_COUNT" "$RESET"

if (( REAL_WORD_COUNT < 250 )); then
    printf '%sFAIL: script too short — %s real words, expected ≥ 250%s\n' \
        "$RED" "$REAL_WORD_COUNT" "$RESET" >&2
    exit 1
fi

if ! grep -qi "Zhang" "$OUT_BASE/zhang-vs-wilder.txt"; then
    printf '%sFAIL: "Zhang" not present in the script%s\n' "$RED" "$RESET" >&2
    exit 1
fi
if ! grep -qi "Wilder" "$OUT_BASE/zhang-vs-wilder.txt"; then
    printf '%sFAIL: "Wilder" not present in the script%s\n' "$RED" "$RESET" >&2
    exit 1
fi

# Banned internal markers. Their presence means the engine bled schema
# detail into user-visible output. Fail-closed; never silence with sed.
declare -a BANNED_MARKERS=(
    "SEGMENT "
    "Source text:"
    "schema_version"
    "specscene"
    "clip_id"
)
BANNED_HITS=()
for marker in "${BANNED_MARKERS[@]}"; do
    if grep -qF "$marker" "$OUT_BASE/zhang-vs-wilder.txt"; then
        BANNED_HITS+=("$marker")
    fi
done
if (( ${#BANNED_HITS[@]} > 0 )); then
    printf '%sFAIL: leaked internal markers in script: %s%s\n' \
        "$RED" "${BANNED_HITS[*]}" "$RESET" >&2
    exit 1
fi

# ── 6. Summary ──────────────────────────────────────────────────────────
printf '\n%sOK: Zhang vs Wilder script E2E (engine-only) produced a clean, non-empty script%s\n' \
    "$GREEN" "$RESET"
printf '  job_id:           %s\n' "$JOB_ID"
printf '  request_id:       %s\n' "$REQ_ID"
printf '  script artefact:  %s/zhang-vs-wilder.txt\n' "$OUT_BASE"
exit 0
