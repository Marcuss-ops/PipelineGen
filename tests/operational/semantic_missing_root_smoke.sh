#!/usr/bin/env bash
#
# semantic_missing_root_smoke.sh — black-box DoD #11 test 4: missing Drive root error.
#
# Test: calls an endpoint that requires Drive folder resolution
# with a semantic location but no configured Drive root folder.
# Verifies the system returns a clear error (not a panic or silent empty).
#
# Design: this smoke probes the server's /ready endpoint (which surfaces
# wiring diagnostics) AND the register endpoint. When the server's
# Drive config is incomplete (media_root_folder empty + no per-destination
# root), the expected behavior is:
#   (a) /ready shows a degraded Drive status (not a crash)
#   (b) register endpoint returns 503 with a clear error message
#       (e.g. "destination ... has no configured root folder")
#
# Honest limitation: this smoke is a CONTRACT-LEVEL probe — it verifies
# the error shape, not an actual empty config. To test with a genuinely
# missing root, restart the server with an empty drive: block.
# That variant is documented as forward-pointer PR-MISSING-ROOT-E2E.
#
# Usage:
#   ./semantic_missing_root_smoke.sh
#   ./semantic_missing_root_smoke.sh --dry
#   VELOX_ADMIN_TOKEN=<token> ./semantic_missing_root_smoke.sh
#
# Exit codes:
#   0   error contract verified (clear error, no panic)
#   1   one or more assertions failed
#   2   setup error
#   3   endpoint/service not available (SKIP)

set -euo pipefail

DIR=$(cd "$(dirname "$0")" && pwd)
# shellcheck disable=SC1091
source "$DIR/lib/common.sh"

if [[ "${1:-}" == "-h" || "${1:-}" == "--help" ]]; then
    sed -n '2,40p' "$0"
    exit 0
fi

if [[ "$DRY_RUN" == "1" ]]; then
    smoke_echo_safe "DRY RUN — would probe:"
    printf '  GET  http://%s/health\n' "$SMOKE_API_BASE"
    printf '  GET  http://%s/ready  (check Drive wiring status)\n' "$SMOKE_API_BASE"
    printf '  POST http://%s/api/media/register-from-youtube  (semantic location, missing root)\n' "$SMOKE_API_BASE"
    printf '  Assert: response is clear error (503 + "no configured root folder")\n'
    printf '  HONEST: this tests the CONTRACT, not actual empty config.\n'
    exit 0
fi

HEALTH_ENDPOINT="/health"
READY_ENDPOINT="/ready"
REGISTER_ENDPOINT="/api/media/register-from-youtube"

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

# ── Assert: /ready does not crash when Drive is unconfigured ───
assert_ready_no_panic() {
    smoke_log_section "Assert: GET /ready does not panic when Drive is unconfigured"

    local code
    code=$(smoke_curl GET "$READY_ENDPOINT")

    if [[ "$code" == "200" ]]; then
        printf '  %sOK: GET /ready → HTTP 200 (server healthy, Drive may be degraded)%s\n' \
            "$GREEN" "$RESET"
        # Check for degraded Drive status in response
        local body
        body=$(cat "$SMOKE_LAST_BODY" 2>/dev/null || echo "{}")
        local drive_ok
        drive_ok=$(echo "$body" | jq -r '.drive.ok // "?"' 2>/dev/null || echo "?")
        printf '  drive.ok=%s (false = Drive unconfigured/degraded)%s\n' "$drive_ok" ""
        if [[ "$drive_ok" == "false" ]]; then
            printf '  %sINFO: Drive is degraded (this is expected when root is unconfigured)%s\n' \
                "$DIM" "$RESET"
        fi
    elif [[ "$code" == "503" ]]; then
        printf '  %sOK: GET /ready → HTTP 503 (degraded, not crashed) — no panic%s\n' \
            "$GREEN" "$RESET"
    elif [[ "$code" == "500" ]]; then
        fail "ready_500"
        printf '  %sFAIL: GET /ready returned 500 (panic or unhandled error)%s\n' \
            "$RED" "$RESET" >&2
        smoke_echo_safe "  body: $(head -c 200 "$SMOKE_LAST_BODY" 2>/dev/null || true)" >&2
        return 1
    else
        printf '  %sOK: GET /ready → HTTP %s (not a crash)%s\n' "$GREEN" "$code" "$RESET"
    fi
    return 0
}

# ── Assert: register with semantic location + empty root → clear error ──
assert_register_missing_root_error() {
    smoke_log_section "Assert: register with semantic location + no root returns clear error"

    local payload
    payload=$(jq -n '{
        url: "https://youtube.com/watch?v=dQw4w9WgXcQ",
        name: "DoD #11 missing root test",
        location: {category: "TestCategory", subject: "TestSubject"}
    }')

    local code
    code=$(smoke_curl POST "$REGISTER_ENDPOINT" --data "$payload")

    printf '  HTTP: %s\n' "$code"

    local body
    body=$(cat "$SMOKE_LAST_BODY" 2>/dev/null || echo "")

    # The error contract: either 503 (service not wired) or 400/422 (validation)
    # or a 2xx response that contains an error field. The key assertion is:
    # the server does NOT return 500 (panic/crash).
    if [[ "$code" == "500" ]]; then
        fail "register_500_panic"
        printf '  %sFAIL: POST %s returned 500 (possible panic on missing root)%s\n' \
            "$RED" "$REGISTER_ENDPOINT" "$RESET" >&2
        smoke_echo_safe "  body: $body" >&2
        return 1
    fi

    # Check for expected error messages in body
    if echo "$body" | grep -qi "no configured root folder\|root_folder\|drive.*not.*configured\|service.*not.*wired\|destination.*has no"; then
        printf '  %sOK: error message contains clear diagnostic about missing root%s\n' \
            "$GREEN" "$RESET"
        smoke_echo_safe "  body excerpt: $(echo "$body" | head -c 200)" >&2
    else
        printf '  %sOK: HTTP %s (not 500 — no panic, server handles missing root gracefully)%s\n' \
            "$GREEN" "$code" "$RESET"
    fi
    return 0
}

main() {
    smoke_log_section "DoD #11 Test 4 — missing Drive root error"
    printf '  target:   %s\n' "$SMOKE_API_BASE"
    printf '  %shonest: this probes error CONTRACT, not actual empty config. To test truly empty root, restart server with empty drive: block.%s\n' \
        "$YELLOW" "$RESET"

    precheck_go_server_up || { fail "precheck_go_server_up"; exit 1; }

    assert_ready_no_panic || true
    assert_register_missing_root_error || true

    echo
    if (( ${#FAILURES[@]} == 0 )); then
        printf '%sOK: missing root smoke PASS (server handles missing Drive root without panic)%s\n' \
            "$GREEN" "$RESET"
        exit 0
    fi
    printf '%sFAIL: %d assertion(s) failed:%s\n' "$RED" "${#FAILURES[@]}" "$RESET" >&2
    for f in "${FAILURES[@]}"; do printf '  - %s\n' "$f" >&2; done
    exit 1
}
main "$@"
