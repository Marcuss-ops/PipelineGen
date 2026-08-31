#!/usr/bin/env bash
#
# voiceover_c2_legacy_fallback_smoke.sh — black-box FASE C2 failure path smoke
# for the voiceover pipeline.
#
# Test: legacy fallback when persistent TTS worker is unavailable.
#   Setup: rename ONLY tts_edge_server.py to .bak (tts_edge.py remains).
#          The Go processor's persistent-worker lazy start fails
#          (worker_process.go: "tts_edge_server.py not found at ..."), then
#          Generate() falls back to the legacy spawn-per-call path which
#          uses tts_edge.py. Audio is generated → child SUCCEEDED.
#   Cleanup: trap-based restore of tts_edge_server.py on any exit.
#
# Atteso finale (6 assertions: 5 count + 1 status):
#   - 1 parent job   (status=SUCCEEDED)
#   - 1 child job    (status=SUCCEEDED — generated via legacy fallback)
#   - 1 voiceover    row (drive_file_id + drive_link non-empty)
#   - 1 media_asset  row (Drive file present)
#   - 1 outbox event (asset.index.requested)
#   - parent.status='SUCCEEDED'
#
# Setup safety (godlike/07 fail-closed): if tts_edge_server.py is ALREADY
# missing (environment pre-broken), the script aborts with exit 2 (refuses
# to run a false-positive test). If the .bak file already exists (from a
# previous unclean exit), the script aborts with exit 2.
#
# Usage:
#   ./voiceover_c2_legacy_fallback_smoke.sh            # real probe
#   ./voiceover_c2_legacy_fallback_smoke.sh --dry      # print would-be, exit 0
#   VELOX_ADMIN_TOKEN=<token> \
#     DRIVE_FOLDER_ID=<id> \
#     ./voiceover_c2_legacy_fallback_smoke.sh
#
# Exit codes:
#   0   all 6 assertions pass
#   1   one or more assertions failed
#   2   setup error (token/db/folder/script pre-state)
#   124 poll loop or wall-clock timeout

set -euo pipefail

DIR=$(cd "$(dirname "$0")" && pwd)
# shellcheck disable=SC1091
source "$DIR/lib/common.sh"

# Project-specific binaries (lib/common.sh already smoke_require'd jq)
smoke_require sqlite3

if [[ "${1:-}" == "-h" || "${1:-}" == "--help" ]]; then
    sed -n '2,50p' "$0"
    exit 0
fi

if [[ "$DRY_RUN" == "1" ]]; then
    smoke_echo_safe "DRY RUN — would probe:"
    printf '  GET  http://%s/health  (Go server up)\n' "$SMOKE_API_BASE"
    printf '  precheck: tts_edge_server.py + tts_edge.py present\n'
    printf '  setup: rename tts_edge_server.py → .bak (tts_edge.py stays)\n'
    printf '  POST http://%s/api/media/voiceover/generate  (1 item: it-IT, expect legacy fallback)\n' "$SMOKE_API_BASE"
    printf '  poll http://%s/api/jobs/<id>/full  (terminal)\n' "$SMOKE_API_BASE"
    printf '  sqlite3 %s   …   (6 count + status assertions; expect SUCCEEDED via legacy)\n' "${SMOKE_DB:-data/media/media.db.sqlite}"
    printf '  cleanup: restore tts_edge_server.py\n'
    exit 0
fi

# ── Configuration ────────────────────────────────────────────────
SMOKE_DB="${SMOKE_DB:?SMOKE_DB must be explicitly set to an isolated or approved database}"
SMOKE_TTS_WORKER_PATH="${SMOKE_TTS_WORKER_PATH:-scripts/bridges/tts_edge_server.py}"
SMOKE_TTS_LEGACY_PATH="${SMOKE_TTS_LEGACY_PATH:-scripts/bridges/tts_edge.py}"
SMOKE_DRIVE_FOLDER_ID="${SMOKE_DRIVE_FOLDER_ID:-}"
ENDPOINT="/api/media/voiceover/generate"
HEALTH_ENDPOINT="/health"
TAG_PREFIX="vo_c2_$(date +%s)_$$"
RUN_ID="$(date -u +%Y-%m-%dT%H-%M-%SZ)"
REQ_ID="${TAG_PREFIX}_legacy_fallback"
JOB_ID=""
SETUP_DONE=0

cleanup_c2() {
    if (( SETUP_DONE == 1 )); then
        if [[ -f "${SMOKE_TTS_WORKER_PATH}.bak" && ! -f "$SMOKE_TTS_WORKER_PATH" ]]; then
            mv "${SMOKE_TTS_WORKER_PATH}.bak" "$SMOKE_TTS_WORKER_PATH" || true
        fi
    fi
}
trap cleanup_c2 EXIT INT TERM

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
    printf '%ssetup error: tts_edge.py already missing at %s — C2 requires the legacy script for the fallback path%s\n' \
        "$RED" "$SMOKE_TTS_LEGACY_PATH" "$RESET" >&2
    exit 2
fi
if [[ -f "${SMOKE_TTS_WORKER_PATH}.bak" ]]; then
    printf '%ssetup error: %s.bak already exists (likely from a previous unclean exit). Restore it manually first.%s\n' \
        "$RED" "$SMOKE_TTS_WORKER_PATH" "$RESET" >&2
    exit 2
fi

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

# Setup: rename ONLY tts_edge_server.py → .bak (legacy tts_edge.py stays)
setup_remove_persistent_worker() {
    smoke_log_section "Setup: rename tts_edge_server.py → .bak (legacy tts_edge.py stays)"
    mv "$SMOKE_TTS_WORKER_PATH" "${SMOKE_TTS_WORKER_PATH}.bak" || {
        printf '%sFAIL: could not rename %s%s\n' "$RED" "$SMOKE_TTS_WORKER_PATH" "$RESET" >&2
        return 1
    }
    SETUP_DONE=1
    printf '  %sOK: tts_edge_server.py renamed to .bak (legacy fallback will activate; cleanup via trap on EXIT)%s\n' "$GREEN" "$RESET"
    return 0
}

post_single_item() {
    smoke_log_section "POST /api/media/voiceover/generate (1 item: it-IT, expect legacy fallback SUCCEEDED)"
    local payload
    payload=$(jq -n --arg rid "$REQ_ID" --arg fid "$SMOKE_DRIVE_FOLDER_ID" '{
        request_id: $rid,
        items: [
            {text: "Test C2 legacy fallback.", language: "it-IT", voice: "it-IT-DiegoNeural", filename: "vo_c2_legacy_fallback.mp3", required: true}
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

poll_parent_to_terminal() {
    smoke_log_section "Poll parent to terminal (timeout ${SMOKE_POLL_TIMEOUT_SECONDS}s, expect SUCCEEDED via legacy)"
    if ! smoke_poll_terminal "$JOB_ID"; then
        printf '%sFAIL: parent job %s did not reach terminal in %ss (legacy fallback should have completed in ~10-20s + 5s aggregator tick)%s\n' \
            "$RED" "$JOB_ID" "$SMOKE_POLL_TIMEOUT_SECONDS" "$RESET" >&2
        return 1
    fi
    printf '  %sOK: parent reached terminal status=%s%s\n' "$GREEN" "$SMOKE_LAST_STATUS" "$RESET"
    return 0
}

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

assert_one_child_succeeded() {
    smoke_log_section "Assert 2: 1 child job (type=voiceover.generate_item, status=SUCCEEDED via legacy fallback)"
    local count
    count=$(sqlite_q "SELECT COUNT(*) FROM jobs WHERE ${CORR_COL} = '${REQ_ID}' AND type = 'voiceover.generate_item' AND LOWER(status) = 'succeeded'")
    if [[ "$count" != "1" ]]; then
        fail "assert2_child_succeeded_count_${count}_expected_1"
        printf '  %sFAIL: %s SUCCEEDED child jobs found (expected 1)%s\n' "$RED" "$count" "$RESET" >&2
        local per_sibling
        per_sibling=$(sqlite_q "SELECT id || '|' || status || '|' || COALESCE(error, '') FROM jobs WHERE ${CORR_COL} = '${REQ_ID}' AND type = 'voiceover.generate_item' AND LOWER(status) != 'succeeded' ORDER BY created_at")
        if [[ -n "$per_sibling" ]]; then
            printf '  non-SUCCEEDED children (per-sibling diagnostic):\n' >&2
            while IFS='|' read -r sid sstatus serror; do
                [[ -z "$sid" ]] && continue
                printf '    child %s status=%s error=%s\n' "$sid" "$sstatus" "${serror:0:120}" >&2
            done <<< "$per_sibling"
        fi
        return 1
    fi
    printf '  %sOK: 1 SUCCEEDED child job (legacy fallback path)%s\n' "$GREEN" "$RESET"
    return 0
}

assert_one_voiceover() {
    smoke_log_section "Assert 3: 1 voiceover row with Drive file (legacy path populates finalizer)"
    local count
    count=$(sqlite_q "SELECT COUNT(*) FROM voiceovers WHERE request_id = '${REQ_ID}' AND drive_file_id != '' AND drive_link != ''")
    if [[ "$count" != "1" ]]; then
        fail "assert3_voiceovers_count_${count}_expected_1"
        printf '  %sFAIL: %s voiceovers rows found (expected 1)%s\n' "$RED" "$count" "$RESET" >&2
        return 1
    fi
    printf '  %sOK: 1 voiceover row with Drive file%s\n' "$GREEN" "$RESET"
    return 0
}

assert_one_media_asset() {
    smoke_log_section "Assert 4: 1 media_asset row with Drive file"
    local count
    count=$(sqlite_q "SELECT COUNT(*) FROM media_assets WHERE source = 'voiceover' AND drive_file_id != '' AND drive_link != '' AND drive_file_id IN (SELECT drive_file_id FROM voiceovers WHERE request_id = '${REQ_ID}')")
    if [[ "$count" != "1" ]]; then
        fail "assert4_media_assets_count_${count}_expected_1"
        printf '  %sFAIL: %s media_assets rows found (expected 1)%s\n' "$RED" "$count" "$RESET" >&2
        return 1
    fi
    printf '  %sOK: 1 media_asset row with Drive file%s\n' "$GREEN" "$RESET"
    return 0
}

assert_one_outbox_event() {
    smoke_log_section "Assert 5: 1 outbox event (event_type=asset.index.requested)"
    local count
    count=$(sqlite_q "SELECT COUNT(*) FROM outbox_events WHERE event_type = 'asset.index.requested' AND aggregate_id IN (SELECT id FROM voiceovers WHERE request_id = '${REQ_ID}')")
    if [[ "$count" != "1" ]]; then
        fail "assert5_outbox_count_${count}_expected_1"
        printf '  %sFAIL: %s outbox events found (expected 1)%s\n' "$RED" "$count" "$RESET" >&2
        return 1
    fi
    printf '  %sOK: 1 outbox event%s\n' "$GREEN" "$RESET"
    return 0
}

assert_parent_status_succeeded() {
    smoke_log_section "Assert 6: parent job status='SUCCEEDED'"
    local count
    count=$(sqlite_q "SELECT COUNT(*) FROM jobs WHERE ${CORR_COL} = '${REQ_ID}' AND type = 'voiceover.generate' AND LOWER(status) = 'succeeded'")
    if [[ "$count" != "1" ]]; then
        local actual_status
        actual_status=$(sqlite_q "SELECT COALESCE(status, '(null)') FROM jobs WHERE ${CORR_COL} = '${REQ_ID}' AND type = 'voiceover.generate' LIMIT 1")
        fail "assert6_parent_status_${actual_status}_expected_SUCCEEDED"
        printf '  %sFAIL: parent status=%s (expected SUCCEEDED)%s\n' "$RED" "$actual_status" "$RESET" >&2
        return 1
    fi
    printf '  %sOK: parent status=SUCCEEDED (legacy fallback path complete)%s\n' "$GREEN" "$RESET"
    return 0
}

main() {
    smoke_log_section "Voiceover FASE C2 — legacy fallback (tts_edge_server.py absent, tts_edge.py present)"
    printf '  target:   %s\n  db:       %s\n  worker:   %s\n  legacy:   %s\n  folder:   %s\n  tag:      %s\n  run_id:   %s\n' \
        "$SMOKE_API_BASE" "$SMOKE_DB" "$SMOKE_TTS_WORKER_PATH" "$SMOKE_TTS_LEGACY_PATH" \
        "$SMOKE_DRIVE_FOLDER_ID" "$TAG_PREFIX" "$RUN_ID"

    precheck_go_server_up || { fail "precheck_go_server_up"; }
    setup_remove_persistent_worker || { fail "setup_remove_persistent_worker"; }

    if (( ${#FAILURES[@]} > 0 )); then
        printf '%sFAIL: setup failed, aborting%s\n' "$RED" "$RESET" >&2
        exit 1
    fi

    post_single_item || { fail "post_single_item"; exit 1; }
    poll_parent_to_terminal || { fail "poll_parent_to_terminal"; }

    assert_one_parent || true
    assert_one_child_succeeded || true
    assert_one_voiceover || true
    assert_one_media_asset || true
    assert_one_outbox_event || true
    assert_parent_status_succeeded || true

    echo
    if (( ${#FAILURES[@]} == 0 )); then
        printf '%sOK: FASE C2 — legacy fallback PASS (child SUCCEEDED + parent SUCCEEDED via legacy spawn-per-call)%s\n' "$GREEN" "$RESET"
        printf '  parent terminal status: %s\n' "${SMOKE_LAST_STATUS:-?}"
        exit 0
    fi
    printf '%sFAIL: %d FASE C2 assertion(s) failed:%s\n' "$RED" "${#FAILURES[@]}" "$RESET" >&2
    for f in "${FAILURES[@]}"; do printf '  - %s\n' "$f" >&2; done
    exit 1
}
main "$@"
