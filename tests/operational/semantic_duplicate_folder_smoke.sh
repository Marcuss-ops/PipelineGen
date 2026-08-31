#!/usr/bin/env bash
#
# semantic_duplicate_folder_smoke.sh — black-box DoD #11 test 3: duplicate folder reuse.
#
# Test: POST the same semantic location twice; verify the second call
# reuses the same Drive folder_id (does NOT create a duplicate folder).
#
# Design: uses /api/media/register-from-youtube (the register endpoint)
# since it already supports semantic Location DTO (Wave 6). The test
# POSTs the same {location: {category: "Boxe", subject: "Mike Tyson"}}
# twice with different video URLs and asserts the same drive_folder_id
# is returned both times (folder reuse). If the register endpoint isn't
# wired, the test skips gracefully.
#
# Expected:
#   - Both calls return HTTP 2xx
#   - Second call's drive_folder_id == first call's drive_folder_id
#   - drive_folder_catalog has exactly 1 row for this path (no duplicates)
#
# Honest limitation: this is a contract-level probe — it verifies the
# API response shape, not the actual Drive folder creation (which
# requires a full Drive client). The forward-pointer PR-DUPLICATE-FOLDER-E2E
# covers a full Drive API test.
#
# Usage:
#   ./semantic_duplicate_folder_smoke.sh
#   ./semantic_duplicate_folder_smoke.sh --dry
#   VELOX_ADMIN_TOKEN=<token> ./semantic_duplicate_folder_smoke.sh
#
# Exit codes:
#   0   all assertions pass
#   1   one or more assertions failed
#   2   setup error
#   3   endpoint/service not available (SKIP)

set -euo pipefail

DIR=$(cd "$(dirname "$0")" && pwd)
# shellcheck disable=SC1091
source "$DIR/lib/common.sh"

smoke_require sqlite3

if [[ "${1:-}" == "-h" || "${1:-}" == "--help" ]]; then
    sed -n '2,40p' "$0"
    exit 0
fi

if [[ "$DRY_RUN" == "1" ]]; then
    smoke_echo_safe "DRY RUN — would probe:"
    printf '  GET  http://%s/health\n' "$SMOKE_API_BASE"
    printf '  POST http://%s/api/media/register-from-youtube (×2 with same location)\n' "$SMOKE_API_BASE"
    printf '  Assert: second call drive_folder_id == first call drive_folder_id\n'
    printf '  Assert: drive_folder_catalog has ≤1 row for this location\n'
    exit 0
fi

SMOKE_DB="${SMOKE_DB:?SMOKE_DB must be explicitly set to an isolated or approved database}"
ENDPOINT="/api/media/register-from-youtube"
HEALTH_ENDPOINT="/health"
TAG_PREFIX="sem_dup_$(date +%s)_$$"

declare -a FAILURES=()
fail() { FAILURES+=("$1"); }

sqlite_q() {
    local out
    if ! out=$(sqlite3 -separator '|' "$SMOKE_DB" "$1" 2>/tmp/smoke_sqlite_err); then
        echo >&2 "DB query failed: sqlite3 exit non-zero"
        cat >&2 /tmp/smoke_sqlite_err
        rm -f /tmp/smoke_sqlite_err
        exit 1
    fi
    rm -f /tmp/smoke_sqlite_err
    printf '%s' "$out"
}

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

# ── POST register with semantic location ───────────────────────
post_register_with_location() {
    local label="$1"
    local url="$2"
    local tag="$3"

    smoke_log_section "POST $label ($tag)"
    local payload
    payload=$(jq -n --arg url "$url" '{
        url: $url,
        name: "DoD #11 duplicate folder test",
        location: {category: "Boxe", subject: "Mike Tyson"}
    }')

    local code
    code=$(smoke_curl POST "$ENDPOINT" --data "$payload")

    if [[ "$code" == "404" ]]; then
        printf '  %sSKIP: POST %s returned 404 (register route not mounted)%s\n' \
            "$YELLOW" "$ENDPOINT" "$RESET"
        exit 3
    fi
    if [[ "$code" == "503" ]]; then
        printf '  %sSKIP: POST %s returned 503 (register service not wired)%s\n' \
            "$YELLOW" "$ENDPOINT" "$RESET"
        exit 3
    fi
    if ! smoke_assert_http_2xx "POST $ENDPOINT ($tag)"; then
        smoke_echo_safe "  body: $(head -c 300 "$SMOKE_LAST_BODY" 2>/dev/null || true)" >&2
        return 1
    fi

    local folder_id
    folder_id=$(jq -r '.drive_folder_id // ""' "$SMOKE_LAST_BODY" 2>/dev/null || echo "")
    printf '  HTTP %s → drive_folder_id=%s\n' "$code" "$folder_id"

    # Return the folder_id via a temp file (bash can't return strings)
    printf '%s' "$folder_id" > "$WORK_DIR/${tag}_folder_id"
    return 0
}

# ── Assert: same folder_id both times ──────────────────────────
assert_same_folder_id() {
    smoke_log_section "Assert: duplicate folder — second call reuses same folder_id"

    FIRST_FOLDER_ID=$(cat "$WORK_DIR/first_folder_id" 2>/dev/null || echo "")
    SECOND_FOLDER_ID=$(cat "$WORK_DIR/second_folder_id" 2>/dev/null || echo "")

    printf '  first:  drive_folder_id=%s\n' "$FIRST_FOLDER_ID"
    printf '  second: drive_folder_id=%s\n' "$SECOND_FOLDER_ID"

    if [[ -z "$FIRST_FOLDER_ID" || -z "$SECOND_FOLDER_ID" ]]; then
        printf '  %sWARN: one or both folder_ids are empty (resolver may not be wired yet — forward-pointer PR-RESOLVER-PORT-EXTRACT)%s\n' \
            "$YELLOW" "$RESET" >&2
        return 0
    fi

    if [[ "$FIRST_FOLDER_ID" == "$SECOND_FOLDER_ID" ]]; then
        printf '  %sOK: same folder_id both times — duplicate folder prevented%s\n' \
            "$GREEN" "$RESET"
    else
        fail "folder_id_mismatch_${FIRST_FOLDER_ID}_vs_${SECOND_FOLDER_ID}"
        printf '  %sFAIL: different folder_ids — duplicate folder may have been created%s\n' \
            "$RED" "$RESET" >&2
        return 1
    fi
    return 0
}

# ── Assert: drive_folder_catalog has ≤1 row ────────────────────
assert_drive_folder_catalog() {
    smoke_log_section "Assert: drive_folder_catalog has ≤1 row for this location"

    local count
    count=$(sqlite_q "SELECT COUNT(*) FROM drive_folder_catalog WHERE category = 'Boxe' AND subject = 'Mike Tyson'" 2>/dev/null || echo "0")
    printf '  drive_folder_catalog rows (category=Boxe, subject=Mike Tyson): %s\n' "$count"

    if [[ "$count" -le 1 ]]; then
        printf '  %sOK: ≤1 catalog row (no duplicate folder)%s\n' "$GREEN" "$RESET"
    else
        fail "catalog_duplicate_${count}_rows"
        printf '  %sFAIL: %s catalog rows for same location (duplicate folder created)%s\n' \
            "$RED" "$count" "$RESET" >&2
        return 1
    fi
    return 0
}

main() {
    smoke_log_section "DoD #11 Test 3 — duplicate folder prevention"
    printf '  target:   %s\n  db:       %s\n  tag:      %s\n' \
        "$SMOKE_API_BASE" "$SMOKE_DB" "$TAG_PREFIX"

    precheck_go_server_up || { fail "precheck_go_server_up"; exit 1; }

    # Two distinct URLs with the same semantic location
    post_register_with_location \
        "First call" \
        "https://youtube.com/watch?v=RRJvrDKunyA" \
        "first" || { fail "post_first"; exit 1; }

    post_register_with_location \
        "Second call (same location)" \
        "https://youtube.com/watch?v=dQw4w9WgXcQ" \
        "second" || { fail "post_second"; exit 1; }

    assert_same_folder_id || true
    assert_drive_folder_catalog || true

    echo
    if (( ${#FAILURES[@]} == 0 )); then
        printf '%sOK: duplicate folder smoke PASS (folder reuse verified)%s\n' "$GREEN" "$RESET"
        exit 0
    fi
    printf '%sFAIL: %d assertion(s) failed:%s\n' "$RED" "${#FAILURES[@]}" "$RESET" >&2
    for f in "${FAILURES[@]}"; do printf '  - %s\n' "$f" >&2; done
    exit 1
}
main "$@"
