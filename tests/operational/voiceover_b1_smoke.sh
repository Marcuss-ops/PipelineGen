#!/usr/bin/env bash
#
# voiceover_b1_smoke.sh — black-box FASE B1 happy path single-item smoke
# for the voiceover pipeline.
#
# Test (single happy-path case per the Voiceover testing plan FASE B — B1):
#   POST /api/media/voiceover/generate with 1 item (it-IT + DiegoNeural),
#   destination.kind=explicit + folder_id, options.strategy=verify, parallelism=1.
#
# Atteso finale (6 assertions total: 5 count + 1 status, mirroring FASE B2):
#   - 1 parent job  (type='voiceover.generate',   correlation_id=<req_id>, status='SUCCEEDED')
#   - 1 child job   (type='voiceover.generate_item', correlation_id=<req_id>, status='SUCCEEDED')
#   - 1 voiceover   row   (request_id=<req_id>, drive_file_id != '', drive_link != '')
#   - 1 media_asset row   (source='voiceover', drive_file_id != '', drive_link != '')
#   - 1 outbox event      (event_type='asset.index.requested', aggregate_id in voiceovers set)
#   - 1 status assertion  (post-parent-poll): parent.status='SUCCEEDED'
#     godlike/07: surfaces 1-child-SUCCEEDED + parent-FAILED runs as FAIL
#     so a partial fan-out can't silently OK.
#
# "Drive file presente" check (per user spec) = drive_file_id AND drive_link
# are non-empty on the voiceovers + media_assets rows. True file-existence
# verification (HTTP GET on the drive_link) is OUT OF SCOPE for this smoke
# — it would need a Drive API client + auth. The drive_file_id/drive_link
# columns are populated by the finalizer's UpsertVoiceoverProjectionTx,
# so non-empty is a strong indicator of Drive upload success.
#
# Precheck: Go server up (GET /health) + Python worker script presence +
# VOICE_OVERRIDES dict contains 'it' (since it-IT is the requested language).
#
# Usage:
#   ./voiceover_happy_smoke.sh            # real probes against live server
#   ./voiceover_happy_smoke.sh --dry      # print the would-be probes, exit 0
#   VELOX_ADMIN_TOKEN=<token> \
#     DRIVE_FOLDER_ID=<id> \
#     ./voiceover_happy_smoke.sh
#
# Environment variables (all overridable; defaults shown):
#   VELOX_ADMIN_TOKEN       bearer token (mandatory if not --dry)
#   API_BASE                host:port (default 127.0.0.1:${VELOX_PORT:-8080})
#   SMOKE_DB                path to media.db.sqlite
#                            (default data/media/media.db.sqlite)
#   SMOKE_DRIVE_FOLDER_ID   Drive folder_id for destination.kind=explicit
#                            (mandatory unless --dry)
#   SMOKE_TTS_WORKER_PATH   path to tts_edge_server.py
#                            (default scripts/bridges/tts_edge_server.py)
#   SMOKE_TIMEOUT_SECONDS   per-script overall wall clock (default 180)
#   SMOKE_POLL_TIMEOUT_SECONDS  poll loop ceiling (default 120)
#   SMOKE_POLL_INTERVAL_SECONDS poll sleep (default 2)
#
# Exit codes:
#   0   all 6 assertions pass
#   1   one or more assertions failed
#   2   setup error (missing token, missing SMOKE_DB, missing folder_id,
#       Go server not up, Python script not found, 'it' not in VOICE_OVERRIDES)
#   124 poll loop or overall wall-clock timeout exceeded

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
    printf '  GET  http://%s/health  (Go server up check)\n' "$SMOKE_API_BASE"
    printf '  fs   %s  (Python worker script presence)\n' "${SMOKE_TTS_WORKER_PATH:-scripts/bridges/tts_edge_server.py}"
    printf '  parse VOICE_OVERRIDES for it (static source parse)\n'
    printf '  POST http://%s/api/media/voiceover/generate  (1 item: it-IT)\n' "$SMOKE_API_BASE"
    printf '  poll http://%s/api/jobs/<id>/full  (terminal)\n' "$SMOKE_API_BASE"
    printf '  sqlite3 %s   …   (6 count + status assertions + Drive file presence)\n' "${SMOKE_DB:-data/media/media.db.sqlite}"
    exit 0
fi

# ── Configuration ────────────────────────────────────────────────
SMOKE_DB="${SMOKE_DB:?SMOKE_DB must be explicitly set to an isolated or approved database}"
SMOKE_TTS_WORKER_PATH="${SMOKE_TTS_WORKER_PATH:-scripts/bridges/tts_edge_server.py}"
SMOKE_DRIVE_FOLDER_ID="${SMOKE_DRIVE_FOLDER_ID:-}"
ENDPOINT="/api/media/voiceover/generate"
HEALTH_ENDPOINT="/health"
TAG_PREFIX="vo_b1_$(date +%s)_$$"
RUN_ID="$(date -u +%Y-%m-%dT%H-%M-%SZ)"
REQ_ID="${TAG_PREFIX}_single"
JOB_ID=""

# ── Setup guards ────────────────────────────────────────────────
if [[ ! -f "$SMOKE_DB" ]]; then
    printf '%ssetup error: SMOKE_DB=%s does not exist%s\n' \
        "$RED" "$SMOKE_DB" "$RESET" >&2
    exit 2
fi
if [[ -z "$SMOKE_DRIVE_FOLDER_ID" ]]; then
    printf '%ssetup error: SMOKE_DRIVE_FOLDER_ID env var unset (the test needs a real Drive folder_id for destination.kind=explicit)%s\n' \
        "$RED" "$RESET" >&2
    exit 2
fi
if [[ ! -f "$SMOKE_TTS_WORKER_PATH" ]]; then
    printf '%ssetup error: SMOKE_TTS_WORKER_PATH=%s not found (the persistent worker source must exist so the child job can lazy-start it)%s\n' \
        "$RED" "$SMOKE_TTS_WORKER_PATH" "$RESET" >&2
    exit 2
fi

# Detect canonical correlation column. The Go EnqueueRequest.CorrelationID
# maps to SQLite column "correlation_id" in the canonical jobs schema; some
# legacy schema variants used "request_id" (matching the JSON wire field).
# Auto-detect at smoke startup so the script survives schema drift.
# Mirror the strict-stderr pattern from sqlite_q() below so DB-locked /
# corrupted / unwritable errors surface loud instead of being misread
# as "schema mismatch".
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

# Strict sqlite query (mirrors fase_b_clip_pipeline_smoke.sh::sqlite_q + B2 script)
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

# ── Precheck 1: Go server is up (GET /health) ───────────────────
precheck_go_server_up() {
    smoke_log_section "Precheck 1: Go server up (GET /health)"
    local code
    code=$(smoke_curl GET "$HEALTH_ENDPOINT")
    if [[ ! "$code" =~ ^2[0-9][0-9]$ ]]; then
        printf '%sFAIL: GET /health returned HTTP %s (expected 2xx)%s\n' \
            "$RED" "$code" "$RESET" >&2
        return 1
    fi
    printf '  %sOK: GET /health → HTTP %s%s\n' "$GREEN" "$code" "$RESET"
    return 0
}

# ── Precheck 2: Python worker script presence ──────────────────
# Worker exists at canonical path (already guarded by setup check above).
# This explicit log line is for operator traceability.
precheck_worker_script_present() {
    smoke_log_section "Precheck 2: Python worker script presence"
    printf '  %sOK: %s exists (%s bytes)%s\n' \
        "$GREEN" "$SMOKE_TTS_WORKER_PATH" \
        "$(wc -c < "$SMOKE_TTS_WORKER_PATH")" "$RESET"
    return 0
}

# ── Precheck 3: VOICE_OVERRIDES contains 'it' ──────────────────
# Static parse of the Python source to confirm the worker will pick a
# KNOWN voice for it-IT. Without this gate, a worker with a stripped
# VOICE_OVERRIDES dict would fall back to edge_tts's list_voices()
# at runtime (network-dependent) or to the 'en-US-AriaNeural'
# hardcoded fallback — both are silent degradation the test would
# NOT catch.
#
# Returns 0 if 'it' is present, 1 otherwise.
precheck_voice_overrides() {
    smoke_log_section "Precheck 3: VOICE_OVERRIDES contains 'it'"
    # Allow both `=` (canonical Python) and `:` (mypy / PEP 484 type-annotation
    # style `VOICE_OVERRIDES: dict[str, str] = {`) as the assignment operator.
    if ! grep -qE '^VOICE_OVERRIDES[[:space:]]*[:=][[:space:]]*\{' "$SMOKE_TTS_WORKER_PATH"; then
        printf '%sFAIL: VOICE_OVERRIDES dict literal not found at top-level in %s%s\n' \
            "$RED" "$SMOKE_TTS_WORKER_PATH" "$RESET" >&2
        return 1
    fi
    if ! grep -Eq "['\"]it['\"]:[[:space:]]*['\"][^'\"]+['\"]" "$SMOKE_TTS_WORKER_PATH"; then
        printf '%sFAIL: VOICE_OVERRIDES missing entry for 'it'%s\n' \
            "$RED" "$RESET" >&2
        return 1
    fi
    # Surface the voice name that WILL be picked (for the test report).
    local voice
    voice=$(grep -E "['\"]it['\"]:[[:space:]]*['\"][^'\"]+['\"]" "$SMOKE_TTS_WORKER_PATH" |
        head -1 | sed -E "s/.*['\"]it['\"]:[[:space:]]*['\"]([^'\"]+)['\"].*/\1/")
    printf '  %slang=it → voice=%s%s\n' "$DIM" "$voice" "$RESET"
    return 0
}

# ── POST single item ───────────────────────────────────────────
# 1 item: it-IT (DiegoNeural), destination.kind=explicit, required:true.
# parallelism defaults to 1 (handler canonical default when omitted).
post_single_item() {
    smoke_log_section "POST /api/media/voiceover/generate (1 item: it-IT DiegoNeural)"
    local payload
    payload=$(jq -n --arg rid "$REQ_ID" --arg fid "$SMOKE_DRIVE_FOLDER_ID" '{
        request_id: $rid,
        items: [
            {text: "Questo e un test della pipeline voiceover di PipelineGen.", language: "it-IT", voice: "it-IT-DiegoNeural", filename: "vo_b1_it.mp3", required: true}
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

    # Extract job_id from canonical 202 Accepted body
    JOB_ID=$(jq -r '.job_id // .id // empty' "$SMOKE_LAST_BODY" 2>/dev/null || echo "")
    if [[ -z "$JOB_ID" ]]; then
        printf '%sFAIL: POST returned no job_id in body%s\n' "$RED" "$RESET" >&2
        smoke_echo_safe "  body: $(head -c 300 "$SMOKE_LAST_BODY" 2>/dev/null || true)" >&2
        return 1
    fi
    printf '  %senqueued parent job_id=%s (correlation_id=%s)%s\n' \
        "$GREEN" "$JOB_ID" "$REQ_ID" "$RESET"
    return 0
}

# ── Poll parent to terminal ───────────────────────────────────
# Tolerance window: SMOKE_POLL_TIMEOUT_SECONDS (default 120). 1 child
# job does TTS (~1-3s) + Drive upload (~1-3s) + DB commit (~0.1s) =
# ~3-6s. With 1 sibling, total wall-clock is ~3-6s + 5s parent
# aggregator tick (PR-VO-PARENT-AGGREGATOR-SPLIT closure). 120s is
# comfortable; lower it for CI speed only if production is
# consistently faster.
poll_parent_to_terminal() {
    smoke_log_section "Poll parent to terminal (timeout ${SMOKE_POLL_TIMEOUT_SECONDS}s)"
    if ! smoke_poll_terminal "$JOB_ID"; then
        printf '%sFAIL: parent job %s did not reach terminal in %ss (last status=%s)%s\n' \
            "$RED" "$JOB_ID" "$SMOKE_POLL_TIMEOUT_SECONDS" "${SMOKE_LAST_STATUS:-?}" "$RESET" >&2
        return 1
    fi
    printf '  %sOK: parent reached terminal status=%s%s\n' \
        "$GREEN" "$SMOKE_LAST_STATUS" "$RESET"
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

# ── Assertion 2: 1 child job of type voiceover.generate_item ───
# Children share the parent's correlation_id (request_id) per
# FanoutVoiceoversUseCase.Execute (fanout.go::requestID propagation).
assert_one_child() {
    smoke_log_section "Assert 2: 1 child job (type=voiceover.generate_item, status=SUCCEEDED)"
    local count
    # Note: SQLite LOWER() is ASCII-only — fine for the canonical Go status
    # values (SUCCEEDED / FAILED / PENDING / DEAD_LETTER / etc.) which are
    # pure ASCII. A future unicode status would silently miss this match.
    count=$(sqlite_q "SELECT COUNT(*) FROM jobs WHERE ${CORR_COL} = '${REQ_ID}' AND type = 'voiceover.generate_item' AND LOWER(status) = 'succeeded'")
    if [[ "$count" != "1" ]]; then
        fail "assert2_child_count_${count}_expected_1"
        printf '  %sFAIL: %s SUCCEEDED child jobs found (expected 1)%s\n' "$RED" "$count" "$RESET" >&2
        # Per-sibling diagnostic: surface id+status+error so operators can
        # debug partial fan-out without opening the DB.
        local per_sibling
        per_sibling=$(sqlite_q "SELECT id || '|' || status || '|' || COALESCE(error, '') FROM jobs WHERE ${CORR_COL} = '${REQ_ID}' AND type = 'voiceover.generate_item' AND LOWER(status) != 'succeeded' ORDER BY created_at")
        if [[ -n "$per_sibling" ]]; then
            printf '  failed children (per-sibling diagnostic):\n' >&2
            while IFS='|' read -r sid sstatus serror; do
                [[ -z "$sid" ]] && continue
                printf '    child %s status=%s error=%s\n' \
                    "$sid" "$sstatus" "${serror:0:120}" >&2
            done <<< "$per_sibling"
        fi
        return 1
    fi
    printf '  %sOK: 1 SUCCEEDED child job%s\n' "$GREEN" "$RESET"
    return 0
}

# ── Assertion 3: 1 voiceover row + Drive file presence ────────
# voiceovers.request_id is the canonical link (per the voiceovers table
# schema — see the user's pasted action plan SQL probe).
# "Drive file presente" = drive_file_id != '' AND drive_link != ''
# (populated by the finalizer's UpsertVoiceoverProjectionTx).
assert_one_voiceover() {
    smoke_log_section "Assert 3: 1 voiceover row with Drive file (request_id=$REQ_ID)"
    local count
    count=$(sqlite_q "SELECT COUNT(*) FROM voiceovers WHERE request_id = '${REQ_ID}' AND drive_file_id != '' AND drive_link != ''")
    if [[ "$count" != "1" ]]; then
        fail "assert3_voiceovers_count_${count}_expected_1"
        printf '  %sFAIL: %s voiceovers rows with Drive file found (expected 1)%s\n' "$RED" "$count" "$RESET" >&2
        # Diagnostic: surface all voiceovers rows for this request_id
        # (even those with empty Drive IDs) so operators see the gap.
        local all_vo
        all_vo=$(sqlite_q "SELECT id || '|drive_file_id=' || COALESCE(drive_file_id, '(null)') || '|drive_link=' || COALESCE(drive_link, '(null)') FROM voiceovers WHERE request_id = '${REQ_ID}'")
        if [[ -n "$all_vo" ]]; then
            printf '  voiceovers rows for request_id:\n' >&2
            while IFS='|' read -r vid vfields; do
                [[ -z "$vid" ]] && continue
                printf '    %s: %s\n' "$vid" "$vfields" >&2
            done <<< "$all_vo"
        fi
        return 1
    fi
    # Surface the actual Drive IDs for operator verification.
    local drive_id drive_link
    drive_id=$(sqlite_q "SELECT drive_file_id FROM voiceovers WHERE request_id = '${REQ_ID}' LIMIT 1")
    drive_link=$(sqlite_q "SELECT drive_link FROM voiceovers WHERE request_id = '${REQ_ID}' LIMIT 1")
    printf '  %sOK: 1 voiceover with Drive file (drive_file_id=%s, drive_link=%s)%s\n' \
        "$GREEN" "$drive_id" "$drive_link" "$RESET"
    return 0
}

# ── Assertion 4: 1 media_asset row + Drive file presence ──────
# media_assets has no direct request_id column; the canonical link is
# via drive_file_id (set in the finalizer's UpsertVoiceoverProjectionTx).
# We join media_assets against the voiceovers row from Assert 3.
assert_one_media_asset() {
    smoke_log_section "Assert 4: 1 media_asset row with Drive file (linked via drive_file_id)"
    local count
    count=$(sqlite_q "SELECT COUNT(*) FROM media_assets WHERE source = 'voiceover' AND drive_file_id != '' AND drive_link != '' AND drive_file_id IN (SELECT drive_file_id FROM voiceovers WHERE request_id = '${REQ_ID}' AND drive_file_id != '')")
    if [[ "$count" != "1" ]]; then
        fail "assert4_media_assets_count_${count}_expected_1"
        printf '  %sFAIL: %s media_assets rows with Drive file found (expected 1)%s\n' "$RED" "$count" "$RESET" >&2
        # Diagnostic: surface all media_assets rows for this request_id's voiceover
        local all_ma
        all_ma=$(sqlite_q "SELECT id || '|source=' || COALESCE(source, '(null)') || '|drive_file_id=' || COALESCE(drive_file_id, '(null)') || '|drive_link=' || COALESCE(drive_link, '(null)') FROM media_assets WHERE source = 'voiceover' AND drive_file_id IN (SELECT drive_file_id FROM voiceovers WHERE request_id = '${REQ_ID}')")
        if [[ -n "$all_ma" ]]; then
            printf '  media_assets rows for voiceover request_id:\n' >&2
            while IFS='|' read -r mid mfields; do
                [[ -z "$mid" ]] && continue
                printf '    %s: %s\n' "$mid" "$mfields" >&2
            done <<< "$all_ma"
        fi
        return 1
    fi
    local ma_drive_id ma_drive_link
    ma_drive_id=$(sqlite_q "SELECT drive_file_id FROM media_assets WHERE source = 'voiceover' AND drive_file_id IN (SELECT drive_file_id FROM voiceovers WHERE request_id = '${REQ_ID}') LIMIT 1")
    ma_drive_link=$(sqlite_q "SELECT drive_link FROM media_assets WHERE source = 'voiceover' AND drive_file_id IN (SELECT drive_file_id FROM voiceovers WHERE request_id = '${REQ_ID}') LIMIT 1")
    printf '  %sOK: 1 media_asset with Drive file (drive_file_id=%s, drive_link=%s)%s\n' \
        "$GREEN" "$ma_drive_id" "$ma_drive_link" "$RESET"
    return 0
}

# ── Assertion 5: 1 outbox event for Qdrant ─────────────────────
# Voiceover finalizer writes asset.index.requested events in-TX with
# the voiceover aggregate_id. We scope by time window (last 5 min) to
# tolerate long-running suites + zero in on this test's traffic.
assert_one_outbox_event() {
    smoke_log_section "Assert 5: 1 outbox event (event_type=asset.index.requested)"
    local count
    # No created_at time-window filter: the aggregate_id subquery already
    # scopes to THIS test's voiceover. A 5-min window was cargo-culted
    # from B2's 3-sibling burst; for 1 child the event lands in <1s and
    # the time filter would only mask a real "event from a previous run"
    # bug if correlation_id leaked.
    count=$(sqlite_q "SELECT COUNT(*) FROM outbox_events WHERE event_type = 'asset.index.requested' AND aggregate_id IN (SELECT id FROM voiceovers WHERE request_id = '${REQ_ID}')")
    if [[ "$count" != "1" ]]; then
        fail "assert5_outbox_count_${count}_expected_1"
        printf '  %sFAIL: %s outbox events found (expected 1)%s\n' "$RED" "$count" "$RESET" >&2
        return 1
    fi
    printf '  %sOK: 1 outbox event%s\n' "$GREEN" "$RESET"
    return 0
}

# ── Assertion 6: parent status='SUCCEEDED' ───────────────────
# godlike/07 no-fake-availability: a 1-child-SUCCEEDED + parent-FAILED
# run (e.g. required-true child failed mid-execution) MUST surface as
# FAIL even though the count assertions pass. Without this gate the
# script would silently OK on a partial-success run that left the
# user without an aggregated SUCCEEDED state. The poll_parent_to_terminal
# already captures SMOKE_LAST_STATUS (printed in the final report).
assert_parent_status_succeeded() {
    smoke_log_section "Assert 6: parent job status='SUCCEEDED'"
    local count
    count=$(sqlite_q "SELECT COUNT(*) FROM jobs WHERE ${CORR_COL} = '${REQ_ID}' AND type = 'voiceover.generate' AND LOWER(status) = 'succeeded'")
    if [[ "$count" != "1" ]]; then
        local actual_status
        actual_status=$(sqlite_q "SELECT COALESCE(status, '(null)') FROM jobs WHERE ${CORR_COL} = '${REQ_ID}' AND type = 'voiceover.generate' LIMIT 1")
        fail "assert6_parent_status_${actual_status}_expected_SUCCEEDED"
        printf '  %sFAIL: parent status=%s (expected SUCCEEDED)%s\n' \
            "$RED" "$actual_status" "$RESET" >&2
        return 1
    fi
    printf '  %sOK: parent status=SUCCEEDED%s\n' "$GREEN" "$RESET"
    return 0
}

main() {
    smoke_log_section "Voiceover FASE B1 — happy path single item (it-IT DiegoNeural)"
    printf '  target:   %s\n  db:       %s\n  worker:   %s\n  folder:   %s\n  tag:      %s\n  run_id:   %s\n' \
        "$SMOKE_API_BASE" "$SMOKE_DB" "$SMOKE_TTS_WORKER_PATH" \
        "$SMOKE_DRIVE_FOLDER_ID" "$TAG_PREFIX" "$RUN_ID"

    # Prechecks (fail-fast before any state-mutating call)
    precheck_go_server_up || { fail "precheck_go_server_up"; }
    precheck_worker_script_present || { fail "precheck_worker_script_present"; }
    precheck_voice_overrides || { fail "precheck_voice_overrides"; }

    if (( ${#FAILURES[@]} > 0 )); then
        printf '%sFAIL: precheck(s) failed, aborting before POST%s\n' "$RED" "$RESET" >&2
        exit 1
    fi

    # Happy path
    post_single_item || { fail "post_single_item"; exit 1; }
    poll_parent_to_terminal || { fail "poll_parent_to_terminal"; }

    # 5 count assertions + 1 status assertion
    assert_one_parent || true
    assert_one_child || true
    assert_one_voiceover || true
    assert_one_media_asset || true
    assert_one_outbox_event || true
    assert_parent_status_succeeded || true

    echo
    if (( ${#FAILURES[@]} == 0 )); then
        printf '%sOK: FASE B1 — happy path single item PASS (1 parent + 1 child + 1 voiceover + 1 media_asset + 1 outbox event, parent status=SUCCEEDED, Drive file present)%s\n' \
            "$GREEN" "$RESET"
        printf '  parent terminal status: %s\n' "${SMOKE_LAST_STATUS:-?}"
        exit 0
    fi
    printf '%sFAIL: %d FASE B1 assertion(s) failed:%s\n' \
        "$RED" "${#FAILURES[@]}" "$RESET" >&2
    for f in "${FAILURES[@]}"; do
        printf '  - %s\n' "$f" >&2
    done
    exit 1
}
main "$@"
