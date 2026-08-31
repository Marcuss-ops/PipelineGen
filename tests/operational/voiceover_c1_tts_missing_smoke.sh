#!/usr/bin/env bash
#
# voiceover_c1_tts_missing_smoke.sh — black-box FASE C1 failure path smoke
# for the voiceover pipeline.
#
# Test: TTS mancante (both tts_edge_server.py AND tts_edge.py absent).
#   Setup: rename both Python TTS scripts to .bak (processor.Generate will
#          fail to lazy-start the persistent worker AND the legacy spawn-per-call
#          path will also fail because the script file is missing).
#   Cleanup: trap-based restore on any exit (success, failure, crash, signal).
#
# Atteso finale (6 assertions: 5 count + 1 status):
#   - 1 parent job  (type='voiceover.generate',   correlation_id=<req_id>, status='FAILED')
#   - 1 child job   (type='voiceover.generate_item', correlation_id=<req_id>, status='FAILED')
#   - 0 voiceovers rows   (TTS missing → no row in the finalizer)
#   - 0 media_assets rows (linked via voiceovers)
#   - 0 outbox events     (asset.index.requested — TTS missing → no emission)
#   - 1 status assertion: parent.status='FAILED' (godlike/07 no-fake-availability)
#
# Setup safety (godlike/07 fail-closed): if EITHER tts script is ALREADY
# missing before the test starts, the script aborts with exit 2 (the
# environment is already broken — running the test would be a false
# positive). If both .bak files already exist (from a previous unclean
# exit), the script aborts with exit 2 (would clobber the .bak).
#
# Usage:
#   ./voiceover_c1_tts_missing_smoke.sh            # real probe
#   ./voiceover_c1_tts_missing_smoke.sh --dry      # print would-be, exit 0
#   VELOX_ADMIN_TOKEN=<token> \
#     DRIVE_FOLDER_ID=<id> \
#     ./voiceover_c1_tts_missing_smoke.sh
#
# Exit codes:
#   0   all 6 assertions pass (child + parent FAILED, no downstream rows)
#   1   one or more assertions failed
#   2   setup error (token/db/folder/script pre-state)
#   124 poll loop or wall-clock timeout

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
    printf '  GET  http://%s/health  (Go server up)\n' "$SMOKE_API_BASE"
    printf '  precheck: both TTS scripts present\n'
    printf '  setup: rename tts_edge_server.py + tts_edge.py → .bak\n'
    printf '  POST http://%s/api/media/voiceover/generate  (1 item: it-IT)\n' "$SMOKE_API_BASE"
    printf '  poll http://%s/api/jobs/<id>/full  (terminal)\n' "$SMOKE_API_BASE"
    printf '  sqlite3 %s   …   (6 count + status assertions; expect FAILED)\n' "${SMOKE_DB:-data/media/media.db.sqlite}"
    printf '  cleanup: restore both .bak files\n'
    exit 0
fi

# ── Configuration ────────────────────────────────────────────────
SMOKE_DB="${SMOKE_DB:?SMOKE_DB must be explicitly set to an isolated or approved database}"
SMOKE_TTS_WORKER_PATH="${SMOKE_TTS_WORKER_PATH:-scripts/bridges/tts_edge_server.py}"
SMOKE_TTS_LEGACY_PATH="${SMOKE_TTS_LEGACY_PATH:-scripts/bridges/tts_edge.py}"
SMOKE_DRIVE_FOLDER_ID="${SMOKE_DRIVE_FOLDER_ID:-}"
ENDPOINT="/api/media/voiceover/generate"
HEALTH_ENDPOINT="/health"
TAG_PREFIX="vo_c1_$(date +%s)_$$"
RUN_ID="$(date -u +%Y-%m-%dT%H-%M-%SZ)"
REQ_ID="${TAG_PREFIX}_tts_missing"
JOB_ID=""
SETUP_DONE=0

# Cleanup function: restore the renamed TTS scripts if the setup was done.
# Idempotent: if .bak doesn't exist, the restore is a no-op (would happen
# if cleanup ran twice or if the original file was already missing).
cleanup_c1() {
    if (( SETUP_DONE == 1 )); then
        if [[ -f "${SMOKE_TTS_WORKER_PATH}.bak" && ! -f "$SMOKE_TTS_WORKER_PATH" ]]; then
            mv "${SMOKE_TTS_WORKER_PATH}.bak" "$SMOKE_TTS_WORKER_PATH" || true
        fi
        if [[ -f "${SMOKE_TTS_LEGACY_PATH}.bak" && ! -f "$SMOKE_TTS_LEGACY_PATH" ]]; then
            mv "${SMOKE_TTS_LEGACY_PATH}.bak" "$SMOKE_TTS_LEGACY_PATH" || true
        fi
    fi
}
trap cleanup_c1 EXIT INT TERM

# ── Setup guards ────────────────────────────────────────────────
if [[ ! -f "$SMOKE_DB" ]]; then
    printf '%ssetup error: SMOKE_DB=%s does not exist%s\n' "$RED" "$SMOKE_DB" "$RESET" >&2
    exit 2
fi
if [[ -z "$SMOKE_DRIVE_FOLDER_ID" ]]; then
    printf '%ssetup error: SMOKE_DRIVE_FOLDER_ID env var unset%s\n' "$RED" "$RESET" >&2
    exit 2
fi
if [[ ! -f "$SMOKE_TTS_WORKER_PATH" ]]; then
    printf '%ssetup error: tts_edge_server.py already missing at %s — environment is pre-broken, refusing to run a false-positive test%s\n' \
        "$RED" "$SMOKE_TTS_WORKER_PATH" "$RESET" >&2
    exit 2
fi
if [[ ! -f "$SMOKE_TTS_LEGACY_PATH" ]]; then
    printf '%ssetup error: tts_edge.py already missing at %s — environment is pre-broken, refusing to run a false-positive test%s\n' \
        "$RED" "$SMOKE_TTS_LEGACY_PATH" "$RESET" >&2
    exit 2
fi
if [[ -f "${SMOKE_TTS_WORKER_PATH}.bak" || -f "${SMOKE_TTS_LEGACY_PATH}.bak" ]]; then
    printf '%ssetup error: .bak file(s) already exist (likely from a previous unclean exit). Restore them manually first:%s\n  %s.bak\n  %s.bak\n' \
        "$RED" "$RESET" "$SMOKE_TTS_WORKER_PATH" "$SMOKE_TTS_LEGACY_PATH" >&2
    exit 2
fi

# Detect canonical correlation column (mirrors B1 + B2 pattern)
SCHEMA_COLS=$(sqlite3 -separator '|' "$SMOKE_DB" \
    "SELECT name FROM pragma_table_info('jobs') WHERE name IN ('correlation_id', 'request_id')" \
    2>/tmp/smoke_schema_err)
SCHEMA_RC=$?
if (( SCHEMA_RC != 0 )); then
    printf '%ssetup error: pragma_table_info failed (exit %d):%s %s\n' \
        "$RED" "$SCHEMA_RC" "$RESET" \
        "$(cat /tmp/smoke_schema_err 2>/dev/null || true)" >&2
    rm -f /tmp/smoke_schema_err
    exit 2
fi
rm -f /tmp/smoke_schema_err
case "$SCHEMA_COLS" in
    *correlation_id*) CORR_COL="correlation_id" ;;
    *request_id*)     CORR_COL="request_id" ;;
    *) printf '%ssetup error: jobs table has neither correlation_id nor request_id column%s\n' \
            "$RED" "$RESET" >&2
        exit 2 ;;
esac

declare -a FAILURES=()
fail() { FAILURES+=("$1"); }

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

# ── Setup: rename both TTS scripts to .bak ─────────────────────
# After this, the Go processor's persistent-worker lazy start fails
# (no tts_edge_server.py) AND the legacy fallback fails (no tts_edge.py).
# The Generate() call returns an error → child job FAILED.
setup_break_tts() {
    smoke_log_section "Setup: rename both TTS scripts → .bak (TTS mancante)"
    mv "$SMOKE_TTS_WORKER_PATH" "${SMOKE_TTS_WORKER_PATH}.bak" || {
        printf '%sFAIL: could not rename %s%s\n' "$RED" "$SMOKE_TTS_WORKER_PATH" "$RESET" >&2
        return 1
    }
    mv "$SMOKE_TTS_LEGACY_PATH" "${SMOKE_TTS_LEGACY_PATH}.bak" || {
        printf '%sFAIL: could not rename %s%s\n' "$RED" "$SMOKE_TTS_LEGACY_PATH" "$RESET" >&2
        return 1
    }
    SETUP_DONE=1
    printf '  %sOK: both TTS scripts renamed to .bak (cleanup via trap on EXIT)%s\n' "$GREEN" "$RESET"
    return 0
}

# ── POST single item (will fail at TTS stage) ──────────────────
post_single_item() {
    smoke_log_section "POST /api/media/voiceover/generate (1 item: it-IT, TTS missing)"
    local payload
    payload=$(jq -n --arg rid "$REQ_ID" --arg fid "$SMOKE_DRIVE_FOLDER_ID" '{
        request_id: $rid,
        items: [
            {text: "Test C1 TTS missing.", language: "it-IT", voice: "it-IT-DiegoNeural", filename: "vo_c1_tts_missing.mp3", required: true}
        ],
        destination: {kind: "explicit", folder_id: $fid},
        options: {remove_silence: false, strategy: "verify", parallelism: 1}
    }')
    local code
    code=$(smoke_curl POST "$ENDPOINT" --data "$payload")
    if ! smoke_assert_http_2xx "POST $ENDPOINT"; then
        smoke_echo_safe "  body: $(head -c 300 "$SMOKE_LAST_BODY" 2>/dev/null || true)" >&2
        return 1
    fi
    JOB_ID=$(jq -r '.job_id // .id // empty' "$SMOKE_LAST_BODY" 2>/dev/null || echo "")
    if [[ -z "$JOB_ID" ]]; then
        printf '%sFAIL: POST returned no job_id in body%s\n' "$RED" "$RESET" >&2
        smoke_echo_safe "  body: $(head -c 300 "$SMOKE_LAST_BODY" 2>/dev/null || true)" >&2
        return 1
    fi
    printf '  %senqueued parent job_id=%s (correlation_id=%s)%s\n' "$GREEN" "$JOB_ID" "$REQ_ID" "$RESET"
    return 0
}

# ── Poll parent to terminal ───────────────────────────────────
poll_parent_to_terminal() {
    smoke_log_section "Poll parent to terminal (timeout ${SMOKE_POLL_TIMEOUT_SECONDS}s, expect FAILED)"
    if ! smoke_poll_terminal "$JOB_ID"; then
        printf '%swarning: poll timeout — proceeding with whatever DB state exists%s\n' "$YELLOW" "$RESET" >&2
        return 0  # Don't fail the test on poll timeout; assertions will catch the state
    fi
    printf '  %sOK: parent reached terminal status=%s%s\n' "$GREEN" "$SMOKE_LAST_STATUS" "$RESET"
    return 0
}

# ── Assertion 1: 1 parent job of type voiceover.generate ────────
assert_one_parent() {
    smoke_log_section "Assert 1: 1 parent job (type=voiceover.generate)"
    local count
    count=$(sqlite_q "SELECT COUNT(*) FROM jobs WHERE ${CORR_COL} = '${REQ_ID}' AND type = 'voiceover.generate'")
    if [[ "$count" != "1" ]]; then
        fail "assert1_parent_count_${count}_expected_1"
        printf '  %sFAIL: %s parent jobs found%s\n' "$RED" "$count" "$RESET" >&2
        return 1
    fi
    printf '  %sOK: 1 parent job%s\n' "$GREEN" "$RESET"
    return 0
}

# ── Assertion 2: 1 child job with status=FAILED ─────────────────
assert_one_child_failed() {
    smoke_log_section "Assert 2: 1 child job (type=voiceover.generate_item, status=FAILED)"
    local count
    count=$(sqlite_q "SELECT COUNT(*) FROM jobs WHERE ${CORR_COL} = '${REQ_ID}' AND type = 'voiceover.generate_item' AND LOWER(status) = 'failed'")
    if [[ "$count" != "1" ]]; then
        fail "assert2_child_failed_count_${count}_expected_1"
        printf '  %sFAIL: %s FAILED child jobs found (expected 1)%s\n' "$RED" "$count" "$RESET" >&2
        local per_sibling
        per_sibling=$(sqlite_q "SELECT id || '|' || status || '|' || COALESCE(error, '') FROM jobs WHERE ${CORR_COL} = '${REQ_ID}' AND type = 'voiceover.generate_item' AND LOWER(status) != 'failed' ORDER BY created_at")
        if [[ -n "$per_sibling" ]]; then
            printf '  non-FAILED children (per-sibling diagnostic):\n' >&2
            while IFS='|' read -r sid sstatus serror; do
                [[ -z "$sid" ]] && continue
                printf '    child %s status=%s error=%s\n' "$sid" "$sstatus" "${serror:0:120}" >&2
            done <<< "$per_sibling"
        fi
        return 1
    fi
    local child_error
    child_error=$(sqlite_q "SELECT COALESCE(error, '(null)') FROM jobs WHERE ${CORR_COL} = '${REQ_ID}' AND type = 'voiceover.generate_item' LIMIT 1")
    printf '  %sOK: 1 FAILED child job (error=%s)%s\n' "$GREEN" "${child_error:0:100}" "$RESET"
    return 0
}

# ── Assertion 3: 0 voiceovers rows (TTS missing → no finalizer row) ─
assert_zero_voiceovers() {
    smoke_log_section "Assert 3: 0 voiceovers rows (TTS missing → no finalizer emission)"
    local count
    count=$(sqlite_q "SELECT COUNT(*) FROM voiceovers WHERE request_id = '${REQ_ID}'")
    if [[ "$count" != "0" ]]; then
        fail "assert3_voiceovers_count_${count}_expected_0"
        printf '  %sFAIL: %s voiceovers rows found (expected 0)%s\n' "$RED" "$count" "$RESET" >&2
        return 1
    fi
    printf '  %sOK: 0 voiceovers rows (TTS missing → finalizer did not run)%s\n' "$GREEN" "$RESET"
    return 0
}

# ── Assertion 4: 0 media_assets rows ──────────────────────────
assert_zero_media_assets() {
    smoke_log_section "Assert 4: 0 media_assets rows (TTS missing → no row)"
    local count
    count=$(sqlite_q "SELECT COUNT(*) FROM media_assets WHERE source = 'voiceover' AND drive_file_id IN (SELECT drive_file_id FROM voiceovers WHERE request_id = '${REQ_ID}')")
    if [[ "$count" != "0" ]]; then
        fail "assert4_media_assets_count_${count}_expected_0"
        printf '  %sFAIL: %s media_assets rows found (expected 0)%s\n' "$RED" "$count" "$RESET" >&2
        return 1
    fi
    printf '  %sOK: 0 media_assets rows%s\n' "$GREEN" "$RESET"
    return 0
}

# ── Assertion 5: 0 outbox events ───────────────────────────────
assert_zero_outbox_events() {
    smoke_log_section "Assert 5: 0 outbox events (TTS missing → no asset.index.requested emission)"
    local count
    count=$(sqlite_q "SELECT COUNT(*) FROM outbox_events WHERE event_type = 'asset.index.requested' AND aggregate_id IN (SELECT id FROM voiceovers WHERE request_id = '${REQ_ID}')")
    if [[ "$count" != "0" ]]; then
        fail "assert5_outbox_count_${count}_expected_0"
        printf '  %sFAIL: %s outbox events found (expected 0)%s\n' "$RED" "$count" "$RESET" >&2
        return 1
    fi
    printf '  %sOK: 0 outbox events%s\n' "$GREEN" "$RESET"
    return 0
}

# ── Assertion 6: parent.status='FAILED' ─────────────────────────
assert_parent_status_failed() {
    smoke_log_section "Assert 6: parent job status='FAILED'"
    local count
    count=$(sqlite_q "SELECT COUNT(*) FROM jobs WHERE ${CORR_COL} = '${REQ_ID}' AND type = 'voiceover.generate' AND LOWER(status) = 'failed'")
    if [[ "$count" != "1" ]]; then
        local actual_status
        actual_status=$(sqlite_q "SELECT COALESCE(status, '(null)') FROM jobs WHERE ${CORR_COL} = '${REQ_ID}' AND type = 'voiceover.generate' LIMIT 1")
        fail "assert6_parent_status_${actual_status}_expected_FAILED"
        printf '  %sFAIL: parent status=%s (expected FAILED)%s\n' "$RED" "$actual_status" "$RESET" >&2
        return 1
    fi
    printf '  %sOK: parent status=FAILED%s\n' "$GREEN" "$RESET"
    return 0
}

main() {
    smoke_log_section "Voiceover FASE C1 — TTS mancante (both tts_edge_server.py + tts_edge.py absent)"
    printf '  target:   %s\n  db:       %s\n  worker:   %s\n  legacy:   %s\n  folder:   %s\n  tag:      %s\n  run_id:   %s\n' \
        "$SMOKE_API_BASE" "$SMOKE_DB" "$SMOKE_TTS_WORKER_PATH" "$SMOKE_TTS_LEGACY_PATH" \
        "$SMOKE_DRIVE_FOLDER_ID" "$TAG_PREFIX" "$RUN_ID"

    precheck_go_server_up || { fail "precheck_go_server_up"; }
    setup_break_tts || { fail "setup_break_tts"; }

    if (( ${#FAILURES[@]} > 0 )); then
        printf '%sFAIL: setup failed, aborting%s\n' "$RED" "$RESET" >&2
        exit 1
    fi

    post_single_item || { fail "post_single_item"; }
    poll_parent_to_terminal || true

    assert_one_parent || true
    assert_one_child_failed || true
    assert_zero_voiceovers || true
    assert_zero_media_assets || true
    assert_zero_outbox_events || true
    assert_parent_status_failed || true

    echo
    if (( ${#FAILURES[@]} == 0 )); then
        printf '%sOK: FASE C1 — TTS mancante PASS (child FAILED + parent FAILED + 0 downstream rows)%s\n' "$GREEN" "$RESET"
        printf '  parent terminal status: %s\n' "${SMOKE_LAST_STATUS:-?}"
        exit 0
    fi
    printf '%sFAIL: %d FASE C1 assertion(s) failed:%s\n' "$RED" "${#FAILURES[@]}" "$RESET" >&2
    for f in "${FAILURES[@]}"; do printf '  - %s\n' "$f" >&2; done
    exit 1
}
main "$@"
