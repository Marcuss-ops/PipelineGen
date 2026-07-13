#!/usr/bin/env bash
#
# pacquiao_broner_script_mini_smoke.sh — smaller clip-native smoke for the
# Pacquiao/Broner script generation path.
#
# Usage:
#   ./pacquiao_broner_script_mini_smoke.sh
#   ./pacquiao_broner_script_mini_smoke.sh --dry
#
# Assertions:
#   1. POST /api/script/generate -> HTTP 202 or 200 with non-empty job_id
#   2. GET  /api/jobs/<job_id>/full -> terminal status within timeout
#   3. accepted_clip_ids contains exactly 2 clip IDs
#   4. mode_info shows clip_native without fallback
#   5. result.output.text is non-empty and mentions both fighters
#   6. result.data.artifacts.document.doc_link is present
#   7. Google Doc HTML contains only the canonical SpecScene surface

set -euo pipefail

DIR=$(cd "$(dirname "$0")" && pwd)
# shellcheck disable=SC1091
source "$DIR/lib/common.sh"

PAYLOAD_FILE="${PAYLOAD_FILE:-$DIR/pacquiao_broner_script_mini_test.json}"

if [[ "${1:-}" == "-h" || "${1:-}" == "--help" ]]; then
    sed -n '2,24p' "$0"
    exit 0
fi

smoke_require jq grep curl

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
if ! smoke_poll_terminal "$JOB_ID"; then
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

ACCEPTED_COUNT=$(jq -r '(
    .result.data.data.source.accepted_clip_ids
    // .result.data.source.accepted_clip_ids
    // .result.source.accepted_clip_ids
    // .result.items[0].result.source.accepted_clip_ids
    // .result.items[0].source.accepted_clip_ids
    // []
) | length' "$SMOKE_LAST_BODY")
if [[ "$ACCEPTED_COUNT" != "2" ]]; then
    printf '%sFAIL: expected 2 accepted clip ids, got %s%s\n' "$RED" "$ACCEPTED_COUNT" "$RESET" >&2
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

DOC_HTML="$WORK_DIR/pacquiao_broner_mini_doc.html"
DOC_HTTP=$(curl -sS --max-time "$SMOKE_HTTP_TIMEOUT_SECONDS" -L -o "$DOC_HTML" -w '%{http_code}' "$DOC_LINK" 2>/dev/null || echo "000")
if [[ "$DOC_HTTP" != "200" ]]; then
    printf '%sFAIL: could not read generated doc HTML (HTTP %s)%s\n' "$RED" "$DOC_HTTP" "$RESET" >&2
    smoke_echo_safe "$(head -c 2000 "$SMOKE_LAST_BODY" 2>/dev/null || true)" >&2
    exit 1
fi

DOC_BODY=$(cat "$DOC_HTML")
for want in \
    "<h1>Manny Pacquiao vs Adrien Broner: recap essenziale</h1>" \
    "<h2>SpecScene JSON</h2>" \
    "yt_RRJvrDKunyA_32_37_v1" \
    "yt_RRJvrDKunyA_993_998_v1"; do
    if ! grep -Fq "$want" <<<"$DOC_BODY"; then
        printf '%sFAIL: generated doc HTML missing %q%s\n' "$RED" "$want" "$RESET" >&2
        exit 1
    fi
done

for unwanted in \
    "<h2>Script</h2>" \
    "<h2>Scenes</h2>" \
    "<h2>Technical Provenance</h2>"; do
    if grep -Fq "$unwanted" <<<"$DOC_BODY"; then
        printf '%sFAIL: generated doc HTML unexpectedly contains %q%s\n' "$RED" "$unwanted" "$RESET" >&2
        exit 1
    fi
done

printf '\n%sOK: mini Pacquiao/Broner clip-native smoke passed and the generated doc is canonical%s\n' \
    "$GREEN" "$RESET"
exit 0
