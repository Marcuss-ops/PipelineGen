#!/usr/bin/env bash
#
# voiceover_c3_drive_folder_invalid_smoke.sh — black-box FASE C3 failure path smoke
# for the voiceover pipeline.
#
# Test: Drive folder invalido (destination.kind=explicit + fake folder_id).
#   Setup: use a syntactically valid but non-existent Drive folder_id
#          (a 33-char string of 'f' characters). The POST is accepted (the
#          handler enqueues the parent job), but the child's Drive upload
#          step fails when the Publisher tries to write to the fake folder.
#   Cleanup: no files renamed; nothing to restore.
#
# Atteso finale (6 assertions: 5 count + 1 status):
#   - 1 parent job  (type='voiceover.generate',   correlation_id=<req_id>, status='FAILED')
#   - 1 child job   (type='voiceover.generate_item', correlation_id=<req_id>, status='FAILED')
#   - 0 voiceovers rows   (Drive upload failed → no finalizer row)
#   - 0 media_assets rows (linked via voiceovers — none exist)
#   - 0 outbox events     (asset.index.requested — Drive upload failed → no emission)
#   - 1 status assertion: parent.status='FAILED' (godlike/07 no-fake-availability)
#
# Setup safety (godlike/07 fail-closed): if SMOKE_DRIVE_FOLDER_ID_INVALID is
# not provided, the script defaults to a 33-char string of 'f'.
#
# Note on the 'f'-fill format: Google Drive folder_ids are base32-ish
# alphanumeric ([A-Za-z0-9_-]). A 33-char string of 'f' is NOT a valid
# base32 string — the Drive API rejects it on format validation BEFORE
# any folder lookup. So the failure is fast (<1s) and deterministic (no
# network rate-limit dependency). The intent is "Drive upload fails";
# whether it's a format reject or a folder-not-found, both surface the
# same canonical child FAILED → parent FAILED → 0 downstream rows path.
# The invalid ID is generated at startup so each test run uses a unique
# (yet consistently fake) folder_id — log lines + DB rows are easy to
# correlate.
#
# TTS precheck (parity with B1): if TTS is also missing/broken, the
# child fails at TTS (C1 behavior) BEFORE reaching Drive upload, and
# the test would silently OK for the wrong reason. To keep C3
# specifically targeting Drive upload failure, we precheck the Python
# worker script presence + VOICE_OVERRIDES dict contains 'it' (the
# same gate B1 uses for the happy path). If TTS precheck fails, the
# test aborts with exit 1 before any state-mutating POST — operators
# get a clear "fix TTS first" diagnostic instead of a misleading
# C3-pass-that-was-actually-a-C1-failure.
#
# Usage:
#   ./voiceover_c3_drive_folder_invalid_smoke.sh            # real probe
#   ./voiceover_c3_drive_folder_invalid_smoke.sh --dry      # print would-be, exit 0
#   VELOX_ADMIN_TOKEN=<token> \
#     ./voiceover_c3_drive_folder_invalid_smoke.sh
#
# Exit codes:
#   0   all 6 assertions pass (child + parent FAILED, no downstream rows)
#   1   one or more assertions failed
#   2   setup error (token/db/folder pre-state)
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
    printf '  use fake folder_id (33 x "f" by default) for destination.kind=explicit\n'
    printf '  POST http://%s/api/media/voiceover/generate  (1 item: it-IT, expect Drive upload FAILED)\n' "$SMOKE_API_BASE"
    printf '  poll http://%s/api/jobs/<id>/full  (terminal)\n' "$SMOKE_API_BASE"
    printf '  sqlite3 %s   …   (6 count + status assertions; expect FAILED)\n' "${SMOKE_DB:-data/media/media.db.sqlite}"
    exit 0
fi

# ── Configuration ────────────────────────────────────────────────
SMOKE_DB="${SMOKE_DB:?SMOKE_DB must be explicitly set to an isolated or approved database}"
# SMOKE_DRIVE_FOLDER_ID is the REAL folder (used for the reference, not here)
# SMOKE_DRIVE_FOLDER_ID_INVALID is the fake one used by the actual POST
SMOKE_DRIVE_FOLDER_ID_INVALID="${SMOKE_DRIVE_FOLDER_ID_INVALID:-}"
# Normalise to 33 chars of 'f' (one-liner: pad with 'f' on the right).
# Canonical Google Drive folder_id length is 33 chars; the 'f'-fill
# produces an invalid base32 string (Drive API rejects on format).
SMOKE_DRIVE_FOLDER_ID_INVALID="$(printf '%-33s' "${SMOKE_DRIVE_FOLDER_ID_INVALID:-}" | tr ' ' 'f')"

ENDPOINT="/api/media/voiceover/generate"
HEALTH_ENDPOINT="/health"
TAG_PREFIX="vo_c3_$(date +%s)_$$"
RUN_ID="$(date -u +%Y-%m-%dT%H-%M-%SZ)"
REQ_ID="${TAG_PREFIX}_drive_folder_invalid"
JOB_ID=""

# ── Setup guards ────────────────────────────────────────────────
if [[ ! -f "$SMOKE_DB" ]]; then
    printf '%ssetup error: SMOKE_DB=%s does not exist%s\n' "$RED" "$SMOKE_DB" "$RESET" >&2
    exit 2
fi
# Sanity: fake folder_id must NOT match a real one (would silently become B1)
if [[ -n "${SMOKE_DRIVE_FOLDER_ID:-}" && "$SMOKE_DRIVE_FOLDER_ID_INVALID" == "$SMOKE_DRIVE_FOLDER_ID" ]]; then
    printf '%ssetup error: SMOKE_DRIVE_FOLDER_ID_INVALID=%s matches the REAL SMOKE_DRIVE_FOLDER_ID — refusing to run (would become B1, not C3)%s\n' \
        "$RED" "$SMOKE_DRIVE_FOLDER_ID_INVALID" "$RESET" >&2
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

# ── Precheck: VOICE_OVERRIDES contains 'it' (TTS path must work) ─
# Without this, C3 could pass for the wrong reason: if TTS is missing
# too, the child fails at TTS (C1 behavior) BEFORE reaching Drive
# upload, and the assertions (1 child FAILED + 1 parent FAILED + 0
# downstream rows) still pass. This precheck guarantees C3 specifically
# targets Drive upload failure.
# Mirrors the B1 precheck_voice_overrides shape.
precheck_voice_overrides() {
    smoke_log_section "Precheck: VOICE_OVERRIDES contains 'it' (C3 needs TTS to work so the failure is at Drive upload)"
    if ! grep -qE '^VOICE_OVERRIDES[[:space:]]*[:=][[:space:]]*\{' "$SMOKE_TTS_WORKER_PATH"; then
        printf '%sFAIL: VOICE_OVERRIDES dict literal not found at top-level in %s — C3 cannot guarantee Drive upload is the failure point%s\n' \
            "$RED" "$SMOKE_TTS_WORKER_PATH" "$RESET" >&2
        return 1
    fi
    if ! grep -Eq "['\"]it['\"]:[[:space:]]*['\"][^'\"]+['\"]" "$SMOKE_TTS_WORKER_PATH"; then
        printf '%sFAIL: VOICE_OVERRIDES missing entry for 'it' — fix TTS before running C3%s\n' \
            "$RED" "$RESET" >&2
        return 1
    fi
    local voice
    voice=$(grep -E "['\"]it['\"]:[[:space:]]*['\"][^'\"]+['\"]" "$SMOKE_TTS_WORKER_PATH" |
        head -1 | sed -E "s/.*['\"]it['\"]:[[:space:]]*['\"]([^'\"]+)['\"].*/\1/")
    printf '  %slang=it → voice=%s (TTS path will succeed → Drive upload will fail)%s\n' \
        "$DIM" "$voice" "$RESET"
    return 0
}

# ── POST single item with INVALID folder_id (will fail at Drive upload) ─
# The POST itself returns 2xx (the job is enqueued). Failure happens during
# the child's Drive upload when the Publisher can't write to the fake folder.
post_single_item_invalid_folder() {
    smoke_log_section "POST /api/media/voiceover/generate (1 item: it-IT, INVALID folder_id=$SMOKE_DRIVE_FOLDER_ID_INVALID)"
    local payload
    payload=$(jq -n --arg rid "$REQ_ID" --arg fid "$SMOKE_DRIVE_FOLDER_ID_INVALID" '{
        request_id: $rid,
        items: [
            {text: "Test C3 drive folder invalid.", language: "it-IT", voice: "it-IT-DiegoNeural", filename: "vo_c3_invalid_folder.mp3", required: true}
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
    printf '  %senqueued parent job_id=%s (correlation_id=%s) — failure expected at Drive upload%s\n' \
        "$GREEN" "$JOB_ID" "$REQ_ID" "$RESET"
    return 0
}

# ── Poll parent to terminal (expect FAILED) ───────────────────
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
    # Note: SQLite LOWER() is ASCII-only — fine for the canonical Go status
    # values (SUCCEEDED / FAILED / PENDING / DEAD_LETTER / etc.).
    count=$(sqlite_q "SELECT COUNT(*) FROM jobs WHERE ${CORR_COL} = '${REQ_ID}' AND type = 'voiceover.generate_item' AND LOWER(status) = 'failed'")
    if [[ "$count" != "1" ]]; then
        fail "assert2_child_failed_count_${count}_expected_1"
        printf '  %sFAIL: %s FAILED child jobs found (expected 1)%s\n' "$RED" "$count" "$RESET" >&2
        local per_sibling
        per_sibling=$(sqlite_q "SELECT id || '|' || status || '|' || COALESCE(error, '') FROM jobs WHERE ${CORR_COL} = '${REQ_ID}' AND type = 'voiceover.generate_item' AND LOWER(status) != 'failed' ORDER BY created_at")
        if [[ -n "$per_sibling" ]]; then
            printf '  failed children (per-sibling diagnostic):\n' >&2
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

# ── Assertion 3: 0 voiceovers rows (Drive upload failed → no finalizer row)
assert_zero_voiceovers() {
    smoke_log_section "Assert 3: 0 voiceovers rows (Drive upload failed → no finalizer emission)"
    local count
    count=$(sqlite_q "SELECT COUNT(*) FROM voiceovers WHERE request_id = '${REQ_ID}'")
    if [[ "$count" != "0" ]]; then
        fail "assert3_voiceovers_count_${count}_expected_0"
        printf '  %sFAIL: %s voiceovers rows found (expected 0)%s\n' "$RED" "$count" "$RESET" >&2
        return 1
    fi
    printf '  %sOK: 0 voiceovers rows (Drive upload failed → finalizer did not run)%s\n' "$GREEN" "$RESET"
    return 0
}

# ── Assertion 4: 0 media_assets rows ──────────────────────────
assert_zero_media_assets() {
    smoke_log_section "Assert 4: 0 media_assets rows (linked via voiceovers — none exist)"
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
    smoke_log_section "Assert 5: 0 outbox events (Drive upload failed → no asset.index.requested emission)"
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
    smoke_log_section "Voiceover FASE C3 — Drive folder invalido (fake folder_id triggers Drive upload failure)"
    printf '  target:           %s\n  db:               %s\n  invalid_folder:   %s\n  real_folder:      %s\n  tag:              %s\n  run_id:           %s\n' \
        "$SMOKE_API_BASE" "$SMOKE_DB" "$SMOKE_DRIVE_FOLDER_ID_INVALID" \
        "${SMOKE_DRIVE_FOLDER_ID:-}" "$TAG_PREFIX" "$RUN_ID"

    precheck_go_server_up || { fail "precheck_go_server_up"; }
    precheck_voice_overrides || { fail "precheck_voice_overrides"; }

    if (( ${#FAILURES[@]} > 0 )); then
        printf '%sFAIL: setup failed, aborting%s\n' "$RED" "$RESET" >&2
        exit 1
    fi

    post_single_item_invalid_folder || { fail "post_single_item_invalid_folder"; exit 1; }
    poll_parent_to_terminal || true

    assert_one_parent || true
    assert_one_child_failed || true
    assert_zero_voiceovers || true
    assert_zero_media_assets || true
    assert_zero_outbox_events || true
    assert_parent_status_failed || true

    echo
    if (( ${#FAILURES[@]} == 0 )); then
        printf '%sOK: FASE C3 — Drive folder invalido PASS (child FAILED at Drive upload + parent FAILED + 0 downstream rows)%s\n' "$GREEN" "$RESET"
        printf '  parent terminal status: %s\n' "${SMOKE_LAST_STATUS:-?}"
        exit 0
    fi
    printf '%sFAIL: %d FASE C3 assertion(s) failed:%s\n' "$RED" "${#FAILURES[@]}" "$RESET" >&2
    for f in "${FAILURES[@]}"; do printf '  - %s\n' "$f" >&2; done
    exit 1
}
main "$@"
