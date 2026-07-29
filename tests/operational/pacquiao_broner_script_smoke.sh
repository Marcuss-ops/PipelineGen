#!/usr/bin/env bash
#
# pacquiao_broner_script_smoke.sh — black-box smoke for the
# Pacquiao/Broner clip-grounded script generation case.
#
# Usage:
#   ./pacquiao_broner_script_smoke.sh       # real run against a live server
#   ./pacquiao_broner_script_smoke.sh --dry # print the would-be request, exit 0
#
# Assertions:
#   1. POST /api/script/generate -> HTTP 202 or 200 with non-empty job_id
#   2. GET  /api/jobs/<job_id>/full -> terminal status within timeout
#   3. accepted_clip_ids contains all 8 clip IDs
#   4. mode_info shows clip_native without fallback
#   5. result.output.text is non-empty and contains both fighter names
#   6. result.output.text is plain narrative prose, not JSON or metadata
#   7. result.data.artifacts.document.doc_link is present when document generation is enabled
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

PAYLOAD_FILE="${PAYLOAD_FILE:-$DIR/../fixtures/script-generation/pacquiao-broner.json}"

if [[ "${1:-}" == "-h" || "${1:-}" == "--help" ]]; then
    sed -n '2,24p' "$0"
    exit 0
fi

smoke_require jq grep

if [[ ! -f "$PAYLOAD_FILE" ]]; then
    printf '%ssetup error: payload file not found: %s%s\n' "$RED" "$PAYLOAD_FILE" "$RESET" >&2
    exit 2
fi

if [[ "$DRY_RUN" == "1" ]]; then
    printf 'DRY RUN — would POST http://%s/api/script/generate\n' "$SMOKE_API_BASE"
    printf 'Payload file: %s\n' "$PAYLOAD_FILE"
    jq . "$PAYLOAD_FILE"
    exit 0
fi

smoke_log_section "POST /api/script/generate"
PAYLOAD=$(cat "$PAYLOAD_FILE")
smoke_curl POST "/api/script/generate" \
    -H "Idempotency-Key: $(smoke_gen_uuid)" \
    --data "$PAYLOAD" >/dev/null
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

smoke_log_section "Poll /api/jobs/${JOB_ID}/full"
if smoke_poll_terminal "$JOB_ID"; then
    :
else
    rc=$?
    printf '%sFAIL: polling did not reach terminal state (rc=%d, last_status=%s)%s\n' \
        "$RED" "$rc" "${SMOKE_LAST_STATUS:-?}" "$RESET" >&2
    exit 1
fi
printf 'final status:   %s%s%s\n' "$CYAN" "$SMOKE_LAST_STATUS" "$RESET"

if [[ "$SMOKE_LAST_STATUS" != "completed" && "$SMOKE_LAST_STATUS" != "SUCCEEDED" ]]; then
    printf '%sFAIL: job status — expected completed or SUCCEEDED, got [%s]%s\n' \
        "$RED" "$SMOKE_LAST_STATUS" "$RESET" >&2
    smoke_echo_safe "$(head -c 400 "$SMOKE_LAST_BODY" 2>/dev/null || true)" >&2
    exit 1
fi

SCRIPT=$(jq -r '
    .result.data.data.output.text
    // .result.data.output.text
    // .result.output.text
    // .result.items[0].result.output.text
    // .result.items[0].output.text
    // ""
' "$SMOKE_LAST_BODY")
if [[ -z "$SCRIPT" || "$SCRIPT" == "null" ]]; then
    printf '%sFAIL: generated script text is empty%s\n' "$RED" "$RESET" >&2
    smoke_echo_safe "$(head -c 2000 "$SMOKE_LAST_BODY" 2>/dev/null || true)" >&2
    exit 1
fi
printf 'script length:  %s%s%s chars\n' "$YELLOW" "${#SCRIPT}" "$RESET"

WORD_COUNT=$(jq -r '
    .result.data.data.output.word_count
    // .result.data.output.word_count
    // .result.output.word_count
    // .result.items[0].result.output.word_count
    // .result.items[0].output.word_count
    // 0
' "$SMOKE_LAST_BODY")
if [[ ! "$WORD_COUNT" =~ ^[0-9]+$ ]] || (( WORD_COUNT <= 0 )); then
    printf '%sFAIL: word_count must be a positive integer (got: %s)%s\n' \
        "$RED" "${WORD_COUNT:-<empty>}" "$RESET" >&2
    exit 1
fi
printf 'word_count:     %s%s%s\n' "$YELLOW" "$WORD_COUNT" "$RESET"

ACCEPTED_COUNT=$(jq -r '(
    .result.data.data.source.accepted_clip_ids
    // .result.data.source.accepted_clip_ids
    // .result.source.accepted_clip_ids
    // .result.items[0].result.source.accepted_clip_ids
    // .result.items[0].source.accepted_clip_ids
    // []
) | length' "$SMOKE_LAST_BODY")
if [[ "$ACCEPTED_COUNT" != "8" ]]; then
    printf '%sFAIL: expected 8 accepted clip ids, got %s%s\n' "$RED" "$ACCEPTED_COUNT" "$RESET" >&2
    smoke_echo_safe "$(head -c 2000 "$SMOKE_LAST_BODY" 2>/dev/null || true)" >&2
    exit 1
fi
printf 'accepted clips: %s%s%s\n' "$YELLOW" "$ACCEPTED_COUNT" "$RESET"

if ! jq -e '
    (
      .result.data.data.mode_info.requested_mode
      // .result.data.mode_info.requested_mode
      // .result.mode_info.requested_mode
      // .result.items[0].result.mode_info.requested_mode
      // .result.items[0].mode_info.requested_mode
    ) as $requested
    | (
      .result.data.data.mode_info.used_mode
      // .result.data.mode_info.used_mode
      // .result.mode_info.used_mode
      // .result.items[0].result.mode_info.used_mode
      // .result.items[0].mode_info.used_mode
    ) as $used
    | (
      .result.data.data.mode_info.fallback_used
      // .result.data.mode_info.fallback_used
      // .result.mode_info.fallback_used
      // .result.items[0].result.mode_info.fallback_used
      // .result.items[0].mode_info.fallback_used
    ) as $fallback
    | $requested == "clip_native"
      and $used == "clip_native"
      and ($fallback == false or $fallback == null)
' "$SMOKE_LAST_BODY" >/dev/null; then
    printf '%sFAIL: clip-native fallback contract not satisfied%s\n' "$RED" "$RESET" >&2
    smoke_echo_safe "$(head -c 2000 "$SMOKE_LAST_BODY" 2>/dev/null || true)" >&2
    exit 1
fi

if ! grep -q 'Pacquiao' <<<"$SCRIPT" || ! grep -q 'Broner' <<<"$SCRIPT"; then
    printf '%sFAIL: generated script does not mention both fighters%s\n' "$RED" "$RESET" >&2
    exit 1
fi

if grep -Eq '```|schema_version|specscene|clip_id|drive\.google\.com|accepted_clip_ids' <<<"$SCRIPT"; then
    printf '%sFAIL: generated script contains JSON/metadata artifacts%s\n' "$RED" "$RESET" >&2
    exit 1
fi

DOC_LINK=$(jq -r '
    .result.data.data.artifacts.document.doc_link
    // .result.data.artifacts.document.doc_link
    // .result.items[0].result.artifacts.document.doc_link
    // .result.items[0].result.data.artifacts.document.doc_link
    // ""
' "$SMOKE_LAST_BODY")
if [[ -z "$DOC_LINK" || "$DOC_LINK" == "null" ]]; then
    printf '%sFAIL: expected a Google Docs link in the generated result%s\n' "$RED" "$RESET" >&2
    smoke_echo_safe "$(head -c 2000 "$SMOKE_LAST_BODY" 2>/dev/null || true)" >&2
    exit 1
fi
printf 'doc link:       %s%s%s\n' "$YELLOW" "$DOC_LINK" "$RESET"

printf '\n%sOK: Pacquiao/Broner clip-grounded generation looks consistent%s\n' \
    "$GREEN" "$RESET"
exit 0
