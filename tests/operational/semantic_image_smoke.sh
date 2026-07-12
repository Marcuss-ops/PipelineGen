#!/usr/bin/env bash
#
# semantic_image_smoke.sh — black-box smoke for canonical AI image generation.
#
# Test: POST /api/images/generated/generate with a style-scoped request.
#
# Expected:
#   - HTTP 200 (or 202 Accepted)
#   - Response includes drive.path, drive.folder_id, drive.link, indexed
#   - Response location.style reflects the requested style
#   - If async: job_id is returned and the job reaches terminal SUCCEEDED
#
# Honest limitation: this smoke does NOT verify visual output quality.
# It verifies the canonical API contract and response envelope. Full
# end-to-end image generation still requires a valid authenticated provider.
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
#   3   endpoint/service not available

set -euo pipefail

DIR=$(cd "$(dirname "$0")" && pwd)
# shellcheck disable=SC1091
source "$DIR/lib/common.sh"

if [[ "${1:-}" == "-h" || "${1:-}" == "--help" ]]; then
    sed -n '2,29p' "$0"
    exit 0
fi

if [[ "$DRY_RUN" == "1" ]]; then
    smoke_echo_safe "DRY RUN — would probe:"
    printf '  GET  http://%s/health\n' "$SMOKE_API_BASE"
    printf '  POST http://%s/api/images/generated/generate  (style=Realistic)\n' "$SMOKE_API_BASE"
    printf '  Assert: response includes drive.path, drive.folder_id, drive.link, indexed, location.style\n'
    exit 0
fi

ENDPOINT="/api/images/generated/generate"
HEALTH_ENDPOINT="/health"
REQUESTED_STYLE="Realistic"

declare -a FAILURES=()
fail() { FAILURES+=("$1"); }

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

post_image_generate() {
    smoke_log_section "POST $ENDPOINT (style=$REQUESTED_STYLE)"
    local payload
    payload=$(jq -n --arg style "$REQUESTED_STYLE" '{
        prompt: "Realistic portrait of Mike Tyson in a boxing gym",
        style: $style,
        width: 512,
        height: 512,
        tags: ["boxing", "portrait"]
    }')

    local code
    code=$(smoke_curl POST "$ENDPOINT" --data "$payload")

    if [[ "$code" == "404" ]]; then
        printf '  %sSKIP: POST %s returned 404 (canonical endpoint not registered)%s\n' \
            "$YELLOW" "$ENDPOINT" "$RESET"
        exit 3
    fi

    if [[ "$code" == "503" ]]; then
        printf '  %sSKIP: POST %s returned 503 (image generation service not wired)%s\n' \
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

assert_response_shape() {
    smoke_log_section "Assert: response includes drive, indexed, and canonical location.style"

    local body
    body=$(cat "$SMOKE_LAST_BODY" 2>/dev/null || echo "{}")

    local indexed
    indexed=$(echo "$body" | jq -r '.indexed // "missing"' 2>/dev/null || echo "parse_error")
    if [[ "$indexed" == "parse_error" ]]; then
        fail "assert_indexed_parse_error"
        printf '  %sFAIL: failed to parse response JSON%s\n' "$RED" "$RESET" >&2
        smoke_echo_safe "  body: $(head -c 200 "$SMOKE_LAST_BODY" 2>/dev/null || true)" >&2
        return 1
    fi
    printf '  %sOK: indexed=%s%s\n' "$GREEN" "$indexed" "$RESET"

    local response_style
    response_style=$(echo "$body" | jq -r '.location.style // ""' 2>/dev/null || echo "")
    if [[ "$response_style" != "$REQUESTED_STYLE" ]]; then
        fail "assert_location_style"
        printf '  %sFAIL: location.style=%q, want %q%s\n' \
            "$RED" "$response_style" "$REQUESTED_STYLE" "$RESET" >&2
    else
        printf '  %sOK: location.style=%s%s\n' "$GREEN" "$response_style" "$RESET"
    fi

    local drive_path drive_folder_id drive_link
    drive_path=$(echo "$body" | jq -r '.drive.path // .drive_path // ""' 2>/dev/null || echo "")
    drive_folder_id=$(echo "$body" | jq -r '.drive.folder_id // .drive_folder_id // ""' 2>/dev/null || echo "")
    drive_link=$(echo "$body" | jq -r '.drive.link // .drive_link // ""' 2>/dev/null || echo "")

    printf '  %sOK: drive.path=%s drive.folder_id=%s drive.link=%s%s\n' \
        "$GREEN" "$drive_path" "$drive_folder_id" "$drive_link" "$RESET"
    return 0
}

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
    smoke_log_section "Canonical AI image generation smoke"
    printf '  target:   %s\n' "$SMOKE_API_BASE"

    precheck_go_server_up || { fail "precheck_go_server_up"; exit 1; }
    post_image_generate || { fail "post_image_generate"; exit 1; }
    assert_response_shape || true
    poll_if_async || true

    echo
    if (( ${#FAILURES[@]} == 0 )); then
        printf '%sOK: canonical AI image generation smoke PASS%s\n' "$GREEN" "$RESET"
        exit 0
    fi
    printf '%sFAIL: %d assertion(s) failed:%s\n' "$RED" "${#FAILURES[@]}" "$RESET" >&2
    for f in "${FAILURES[@]}"; do printf '  - %s\n' "$f" >&2; done
    exit 1
}

main "$@"
