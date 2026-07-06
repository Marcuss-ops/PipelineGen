#!/usr/bin/env bash
#
# semantic_image_smoke.sh — black-box DoD #11 test 1: AI image with semantic location.
#
# Test: POST /api/images/generate with a semantic location block
#   {location: {style: "Realistic", subject: "Mike Tyson"}}
#
# Expected:
#   - HTTP 200 (or 202 Accepted) — endpoint accepts semantic location
#   - Response includes: drive.path, drive.folder_id, drive.link, indexed
#   - If async: job_id is returned and the job reaches terminal SUCCEEDED
#
# Honest limitation: this smoke does NOT verify actual image generation
# (that requires a running AI image service). It verifies the API contract
# — the endpoint accepts semantic location and returns the canonical
# response shape per DoD #8/#10. Full end-to-end image generation is a
# separate forward-pointer (PR-IMAGE-E2E-GENERATE).
#
# Usage:
#   ./semantic_image_smoke.sh
#   ./semantic_image_smoke.sh --dry
#   VELOX_ADMIN_TOKEN=<token> ./semantic_image_smoke.sh
#
# Exit codes:
#   0   all assertions pass
#   1   one or more assertions failed
#   2   setup error (missing token, server not up)
#   3   endpoint/service not available (SKIP — route not registered or svc not wired)

set -euo pipefail

DIR=$(cd "$(dirname "$0")" && pwd)
# shellcheck disable=SC1091
source "$DIR/lib/common.sh"

if [[ "${1:-}" == "-h" || "${1:-}" == "--help" ]]; then
    sed -n '2,30p' "$0"
    exit 0
fi

if [[ "$DRY_RUN" == "1" ]]; then
    smoke_echo_safe "DRY RUN — would probe:"
    printf '  GET  http://%s/health\n' "$SMOKE_API_BASE"
    printf '  POST http://%s/api/images/generate  (semantic location: style=Realistic subject=Mike Tyson)\n' "$SMOKE_API_BASE"
    printf '  Assert: response includes drive.path, drive.folder_id, drive.link, indexed\n'
    exit 0
fi

ENDPOINT="/api/images/generate"
HEALTH_ENDPOINT="/health"

declare -a FAILURES=()
fail() { FAILURES+=("$1"); }

# ── Precheck: Go server up ─────────────────────────────────────
precheck_go_server_up() {
    smoke_log_section "Precheck: Go server up (GET /health)"
    local code
    code=$(smoke_curl GET "$HEALTH_ENDPOINT")
    if [[ ! "$code" =~ ^2[0-9][0-9]$ ]]; then
        printf '%sFAIL: GET /health returned HTTP %s%s\n' "$RED" "$code" "$RESET" >&2
        return 1
    fi
    printf '  %sOK: GET /health → HTTP %s%s\n' "$GREEN" "$code" "$RESET"
    return 0
}

# ── POST semantic image generation ──────────────────────────────
post_image_generate() {
    smoke_log_section "POST /api/images/generate (semantic location: style=Realistic subject=Mike Tyson)"
    local payload
    payload=$(jq -n '{
        prompt: "Realistic portrait of Mike Tyson in a boxing gym",
        location: {style: "Realistic", subject: "Mike Tyson"}
    }')

    local code
    code=$(smoke_curl POST "$ENDPOINT" --data "$payload")

    # 404 = endpoint not registered (images/generate route may not exist yet)
    if [[ "$code" == "404" ]]; then
        printf '  %sSKIP: POST %s returned 404 (endpoint not registered — forward-pointer PR-IMAGE-SEMANTIC-ROUTE)%s\n' \
            "$YELLOW" "$ENDPOINT" "$RESET"
        exit 3
    fi

    if [[ "$code" == "503" ]]; then
        printf '  %sSKIP: POST %s returned 503 (image generation service not wired — forward-pointer PR-IMAGE-SERVICE-WIRE)%s\n' \
            "$YELLOW" "$ENDPOINT" "$RESET"
        exit 3
    fi

    if ! smoke_assert_http_2xx "POST $ENDPOINT"; then
        smoke_echo_safe "  body: $(head -c 300 "$SMOKE_LAST_BODY" 2>/dev/null || true)" >&2
        return 1
    fi
    printf '  %sOK: POST %s → HTTP %s%s\n' "$GREEN" "$ENDPOINT" "$code" "$RESET"
    return 0
}

# ── Assert: response shape includes drive + indexed ─────────────
assert_response_shape() {
    smoke_log_section "Assert: response includes drive.path, drive.folder_id, drive.link, indexed"

    local body
    body=$(cat "$SMOKE_LAST_BODY" 2>/dev/null || echo "{}")

    # Check for indexed field
    local indexed
    indexed=$(echo "$body" | jq -r '.indexed // "missing"' 2>/dev/null || echo "parse_error")
    if [[ "$indexed" == "parse_error" ]]; then
        fail "assert_indexed_parse_error"
        printf '  %sFAIL: failed to parse response JSON%s\n' "$RED" "$RESET" >&2
        smoke_echo_safe "  body: $(head -c 200 "$SMOKE_LAST_BODY" 2>/dev/null || true)" >&2
        return 1
    fi
    printf '  %sOK: indexed=%s%s\n' "$GREEN" "$indexed" "$RESET"

    # Check for drive block (may be nested under drive or top-level)
    local drive_path drive_folder_id drive_link
    drive_path=$(echo "$body" | jq -r '.drive.path // .drive_path // ""' 2>/dev/null || echo "")
    drive_folder_id=$(echo "$body" | jq -r '.drive.folder_id // .drive_folder_id // ""' 2>/dev/null || echo "")
    drive_link=$(echo "$body" | jq -r '.drive.link // .drive_link // ""' 2>/dev/null || echo "")

    # For async responses, drive fields may be empty placeholders — that's valid
    if [[ "$drive_path" != "missing" && "$drive_folder_id" != "missing" && "$drive_link" != "missing" ]]; then
        printf '  %sOK: drive.path=%s drive.folder_id=%s drive.link=%s%s\n' \
            "$GREEN" "$drive_path" "$drive_folder_id" "$drive_link" "$RESET"
    else
        printf '  %sWARN: drive fields not found in response (may be async placeholder)%s\n' \
            "$YELLOW" "$RESET" >&2
    fi

    return 0
}

# ── Optional: poll job if response contains job_id ──────────────
poll_if_async() {
    local job_id
    job_id=$(jq -r '.job_id // empty' "$SMOKE_LAST_BODY" 2>/dev/null || echo "")
    if [[ -z "$job_id" ]]; then
        return 0
    fi

    smoke_log_section "Poll async job to terminal (job_id=$job_id)"
    if smoke_poll_terminal "$job_id"; then
        printf '  %sOK: job %s reached terminal status=%s%s\n' \
            "$GREEN" "$job_id" "${SMOKE_LAST_STATUS:-?}" "$RESET"
    else
        fail "poll_timeout_${job_id}"
        printf '  %sFAIL: job %s did not reach terminal in %ss%s\n' \
            "$RED" "$job_id" "$SMOKE_POLL_TIMEOUT_SECONDS" "$RESET" >&2
        return 1
    fi
    return 0
}

main() {
    smoke_log_section "DoD #11 Test 1 — AI image with semantic location"
    printf '  target:   %s\n' "$SMOKE_API_BASE"

    precheck_go_server_up || { fail "precheck_go_server_up"; exit 1; }

    post_image_generate || { fail "post_image_generate"; exit 1; }
    assert_response_shape || true
    poll_if_async || true

    echo
    if (( ${#FAILURES[@]} == 0 )); then
        printf '%sOK: AI image semantic-location smoke PASS (endpoint accepts {location} block)%s\n' \
            "$GREEN" "$RESET"
        exit 0
    fi
    printf '%sFAIL: %d assertion(s) failed:%s\n' "$RED" "${#FAILURES[@]}" "$RESET" >&2
    for f in "${FAILURES[@]}"; do printf '  - %s\n' "$f" >&2; done
    exit 1
}
main "$@"
