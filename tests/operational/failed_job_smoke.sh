#!/usr/bin/env bash
#
# failed_job_smoke.sh — PipelineGen black-box error-path smoke
#
# Usage:
#   ./failed_job_smoke.sh            # real probes against a live server
#   ./failed_job_smoke.sh --dry      # print the would-be probes, exit 0
#
# Asserts:
#   1. POST /api/script/generate with an INVALID GenerationEnvelopeV2
#      → HTTP 4xx (the server must NOT silently accept a malformed job)
#   2. GET /api/jobs/<nonexistent>/full → HTTP 404.
#
# Exit codes: 0 success, 1 assertion failure, 2 setup error.

set -euo pipefail

DIR=$(cd "$(dirname "$0")" && pwd)
# shellcheck disable=SC1091
source "$DIR/lib/common.sh"

if [[ "${1:-}" == "-h" || "${1:-}" == "--help" ]]; then
    sed -n '2,14p' "$0"; exit 0
fi

if [[ "$DRY_RUN" == "1" ]]; then
    smoke_echo_safe "DRY RUN — would probe:"
    printf '  POST http://%s/api/script/generate  (invalid V2 payload)\n' "$SMOKE_API_BASE"
    printf '  GET  http://%s/api/jobs/nonexistent-smoke-test-deadbeef/full\n' "$SMOKE_API_BASE"
    exit 0
fi

smoke_log_section "Invalid canonical payload → 4xx"
INVALID_PAYLOAD='{"version":2,"preset":"custom","items":[{"source":{"type":"clips","clip_ids":[]}}]}'
HTTP=$(smoke_curl POST "/api/script/generate" --data "$INVALID_PAYLOAD")
if [[ ! "$HTTP" =~ ^[4][0-9][0-9]$ ]]; then
    printf '%sFAIL: invalid payload accepted with HTTP %s (expected 4xx)%s\n' \
        "$RED" "$HTTP" "$RESET" >&2
    smoke_echo_safe "$(head -c 400 "$SMOKE_LAST_BODY" 2>/dev/null || true)" >&2
    exit 1
fi
printf 'invalid-payload rejected with HTTP %s%s%s (correct)\n' \
    "$YELLOW" "$HTTP" "$RESET"

smoke_log_section "Nonexistent job_id → 404"
GHOST_ID="nonexistent-$(smoke_gen_uuid)"
HTTP=$(smoke_curl GET "/api/jobs/${GHOST_ID}/full")
if [[ "$HTTP" != "404" ]]; then
    printf '%sFAIL: nonexistent job_id returned HTTP %s (expected 404)%s\n' \
        "$RED" "$HTTP" "$RESET" >&2
    smoke_echo_safe "$(head -c 400 "$SMOKE_LAST_BODY" 2>/dev/null || true)" >&2
    exit 1
fi
printf 'nonexistent job_id %s%s%s rejected with HTTP 404 (correct)\n' \
    "$DIM" "$GHOST_ID" "$RESET"

printf '\n%sOK: both error paths behave correctly%s\n' "$GREEN" "$RESET"
exit 0
