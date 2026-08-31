#!/usr/bin/env bash
#
# voiceover_validation_smoke.sh — black-box FASE A validation fail-closed smoke
# for the voiceover pipeline.
#
# Tests (3 cases per the Voiceover testing plan FASE A — Action Plan 2026-07-04):
#   A.1  items=[]                                        → HTTP 400 + body contains
#                                                          "items: must contain at least one item"
#   A.2  destination.kind=group, group=""                → HTTP 400 + body contains
#                                                          'destination: kind="group" requires non-empty group'
#   A.3  destination.kind=explicit, no folder_id         → HTTP 400 + body contains
#                                                          'destination: kind="explicit" requires non-empty folder_id'
#
# All 3 cases must produce 0 jobs in the DB (validation runs before Enqueue
# in the canonical handler — internal/api/assets/voiceover/handler.go:Generate
# calls apiutil.BadRequest BEFORE the jobsSvc.Enqueue call).
#
# Tag pattern: each test run uses a unique TAG_PREFIX = "vo_validation_$(date +%s)_$$"
# so concurrent test runs don't pollute each other's DB count.
#
# Usage:
#   ./voiceover_validation_smoke.sh            # real probes against live server
#   ./voiceover_validation_smoke.sh --dry      # print the would-be probes, exit 0
#   VELOX_ADMIN_TOKEN=<token> ./voiceover_validation_smoke.sh
#
# Exit codes:
#   0   all 3 cases pass (HTTP 400 + body substring + 0 jobs each)
#   1   one or more cases failed
#   2   setup error (missing token, missing sqlite3, missing jq, missing SMOKE_DB)
#   124 overall timeout exceeded

set -euo pipefail

DIR=$(cd "$(dirname "$0")" && pwd)
# shellcheck disable=SC1091
source "$DIR/lib/common.sh"

# Project-specific binaries (lib/common.sh already smoke_require'd jq)
smoke_require sqlite3

# Help text
if [[ "${1:-}" == "-h" || "${1:-}" == "--help" ]]; then
    sed -n '2,50p' "$0"
    exit 0
fi

# Dry-run mode
if [[ "$DRY_RUN" == "1" ]]; then
    smoke_echo_safe "DRY RUN — would probe:"
    printf '  POST http://%s/api/media/voiceover/generate  (A.1: items=[])\n' "$SMOKE_API_BASE"
    printf '  POST http://%s/api/media/voiceover/generate  (A.2: kind=group, group="")\n' "$SMOKE_API_BASE"
    printf '  POST http://%s/api/media/voiceover/generate  (A.3: kind=explicit, no folder_id)\n' "$SMOKE_API_BASE"
    printf '  sqlite3 %s   …   (DB probe: 0 jobs for each test request_id)\n' "${SMOKE_DB:-data/media/media.db.sqlite}"
    exit 0
fi

# Constants
SMOKE_DB="${SMOKE_DB:?SMOKE_DB must be explicitly set to an isolated or approved database}"
ENDPOINT="/api/media/voiceover/generate"
TAG_PREFIX="vo_validation_$(date +%s)_$$"
RUN_ID="$(date -u +%Y-%m-%dT%H-%M-%SZ)"

# Setup guard
if [[ ! -f "$SMOKE_DB" ]]; then
    printf '%ssetup error: SMOKE_DB=%s does not exist%s\n' \
        "$RED" "$SMOKE_DB" "$RESET" >&2
    exit 2
fi

# Detect canonical correlation column. The Go EnqueueRequest.CorrelationID
# maps to SQLite column "correlation_id" in the canonical jobs schema; some
# legacy schema variants used "request_id" (matching the JSON wire field).
# Auto-detect at smoke startup so the script survives schema drift.
SCHEMA_COLS=$(sqlite3 -separator '|' "$SMOKE_DB" \
    "SELECT name FROM pragma_table_info('jobs') WHERE name IN ('correlation_id', 'request_id')")
case "$SCHEMA_COLS" in
    *correlation_id*) CORR_COL="correlation_id" ;;
    *request_id*)     CORR_COL="request_id" ;;
    *) printf '%ssetup error: jobs table has neither correlation_id nor request_id column%s\n' \
            "$RED" "$RESET" >&2
        exit 2 ;;
esac

declare -a FAILURES=()
fail() { FAILURES+=("$1"); }

# Strict sqlite query (mirrors fase_b_clip_pipeline_smoke.sh's sqlite_q)
sqlite_q() {
    local out
    if ! out=$(sqlite3 -separator '|' "$SMOKE_DB" "$1" 2>/tmp/smoke_sqlite_err); then
        echo >&2 "DB query failed: sqlite3 exit non-zero with stderr:"
        cat >&2 /tmp/smoke_sqlite_err
        rm -f /tmp/smoke_sqlite_err
        exit 1
    fi
    rm -f /tmp/smoke_sqlite_err
    printf '%s' "$out"
}

# run_case: POST the payload, assert HTTP 400, assert body substring,
# assert 0 jobs in DB for THIS specific request_id.
#
# Args:
#   $1 label           e.g. "a1_empty_items"
#   $2 req_id          unique correlation id (also written to the payload)
#   $3 payload         JSON body
#   $4 expected_sub    substring expected in the response body
run_case() {
    local label="$1"
    local req_id="$2"
    local payload="$3"
    local expected_sub="$4"

    smoke_log_section "$label"

    local code
    code=$(smoke_curl POST "$ENDPOINT" --data "$payload")
    if [[ "$code" != "400" ]]; then
        fail "${label}_http_${code}_expected_400"
        smoke_echo_safe "  body: $(head -c 200 "$SMOKE_LAST_BODY" 2>/dev/null || true)" >&2
        return 1
    fi

    local body
    body=$(cat "$SMOKE_LAST_BODY" 2>/dev/null || echo "")
    if [[ "$body" != *"$expected_sub"* ]]; then
        fail "${label}_body_missing_substring"
        printf '  expected substring: %s\n  body: %s\n' "$expected_sub" "$body" >&2
        return 1
    fi

    # DB probe: count jobs for THIS specific request_id (column detected
    # at startup via pragma_table_info so the script survives schema
    # drift). 0 expected because validation runs BEFORE the Enqueue call.
    local jobs_count
    jobs_count=$(sqlite_q "SELECT COUNT(*) FROM jobs WHERE ${CORR_COL} = '${req_id}'")
    if [[ "$jobs_count" != "0" ]]; then
        fail "${label}_db_created_${jobs_count}_jobs"
        printf '  DB has %s jobs for %s %s (expected 0)\n' \
            "$jobs_count" "$CORR_COL" "$req_id" >&2
        return 1
    fi

    printf '  %sPASS: HTTP 400 + body substring + 0 jobs%s\n' "$GREEN" "$RESET"
    return 0
}

# ── Test A.1: items=[] ───────────────────────────────────────────
test_a1_empty_items() {
    local req_id="${TAG_PREFIX}_a1"
    local payload
    payload=$(jq -n --arg rid "$req_id" '{
        request_id: $rid,
        items: []
    }')
    run_case "a1_empty_items" "$req_id" "$payload" \
        "items: must contain at least one item" || true
}

# ── Test A.2: kind=group, group="" ──────────────────────────────
test_a2_group_empty() {
    local req_id="${TAG_PREFIX}_a2"
    local payload
    payload=$(jq -n --arg rid "$req_id" '{
        request_id: $rid,
        items: [{text: "Test", language: "it-IT"}],
        destination: {kind: "group", group: ""}
    }')
    run_case "a2_group_empty" "$req_id" "$payload" \
        'destination: kind="group" requires non-empty group' || true
}

# ── Test A.3: kind=explicit, no folder_id ───────────────────────
test_a3_explicit_no_folder() {
    local req_id="${TAG_PREFIX}_a3"
    local payload
    payload=$(jq -n --arg rid "$req_id" '{
        request_id: $rid,
        items: [{text: "Test", language: "it-IT"}],
        destination: {kind: "explicit"}
    }')
    run_case "a3_explicit_no_folder" "$req_id" "$payload" \
        'destination: kind="explicit" requires non-empty folder_id' || true
}

main() {
    smoke_log_section "Voiceover FASE A — validation fail-closed (3 cases)"
    printf '  target:   %s\n  db:       %s\n  tag:      %s\n  run_id:   %s\n' \
        "$SMOKE_API_BASE" "$SMOKE_DB" "$TAG_PREFIX" "$RUN_ID"

    test_a1_empty_items
    test_a2_group_empty
    test_a3_explicit_no_folder

    echo
    if (( ${#FAILURES[@]} == 0 )); then
        printf '%sOK: FASE A — 3/3 validation cases pass (HTTP 400 + 0 jobs each)%s\n' \
            "$GREEN" "$RESET"
        exit 0
    fi
    printf '%sFAIL: %d FASE A case(s) failed:%s\n' \
        "$RED" "${#FAILURES[@]}" "$RESET" >&2
    for f in "${FAILURES[@]}"; do
        printf '  - %s\n' "$f" >&2
    done
    exit 1
}
main "$@"
