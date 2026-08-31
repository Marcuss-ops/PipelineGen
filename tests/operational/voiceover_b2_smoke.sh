#!/usr/bin/env bash
#
# voiceover_b2_smoke.sh — black-box FASE B2 multi-item mixed text smoke
# for the voiceover pipeline.
#
# Test (single happy-path case per the Voiceover testing plan FASE B):
#   POST /api/media/voiceover/generate with 3 items (it-IT + en-US + pt-BR),
#   destination.kind=explicit + folder_id, options.parallelism=3.
#
# Atteso finale (6 assertions total: 5 count + 1 status):
#   - 1 parent job  (type='voiceover.generate',   correlation_id=<req_id>, status='SUCCEEDED')
#   - 3 child jobs  (type='voiceover.generate_item', correlation_id=<req_id>, status='SUCCEEDED')
#   - 3 voiceovers  rows   (request_id=<req_id>)
#   - 3 media_assets rows  (source='voiceover', drive_file_id in voiceovers set)
#   - 3 outbox events      (event_type LIKE 'asset.index.requested', recent)
#   - 1 status assertion (post-parent-poll): parent.status='SUCCEEDED'
#     godlike/07: surfaces 3-children-SUCCEEDED + parent-FAILED runs as FAIL
#     so a partial fan-out can't silently OK.
#
# Precheck (per user spec "PRIMA controllando le voci Microsoft Edge disponibili"):
#   The TTS persistent worker (scripts/bridges/tts_edge_server.py) does NOT
#   expose a runtime /voices endpoint. Voice resolution happens server-side
#   via the get_voice_for_lang() function using a hardcoded VOICE_OVERRIDES
#   map. The precheck therefore:
#     1. verifies the Go server is up (GET /health 200)
#     2. verifies the Python worker script exists at the canonical path
#     3. parses the VOICE_OVERRIDES dict from the script source to confirm
#        the 3 requested languages (it, en, pt) are listed (so the worker
#        WILL pick a known voice, not fall back to a "voices[0]" random pick
#        or the en-US-AriaNeural hardcoded fallback)
#
# Usage:
#   ./voiceover_b2_smoke.sh            # real probes against live server
#   ./voiceover_b2_smoke.sh --dry      # print the would-be probes, exit 0
#   VELOX_ADMIN_TOKEN=<token> \
#     DRIVE_FOLDER_ID=<id> \
#     ./voiceover_b2_smoke.sh
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
#   0   all 5 count assertions pass
#   1   one or more assertions failed
#   2   setup error (missing token, missing SMOKE_DB, missing folder_id,
#       Go server not up, Python script not found, voice not in VOICE_OVERRIDES)
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
    printf '  parse VOICE_OVERRIDES for it/en/pt (static source parse)\n'
    printf '  POST http://%s/api/media/voiceover/generate  (3 items: it+en+pt)\n' "$SMOKE_API_BASE"
    printf '  poll http://%s/api/jobs/<id>/full  (terminal)\n' "$SMOKE_API_BASE"
    printf '  sqlite3 %s   …   (5 count assertions)\n' "${SMOKE_DB:-data/media/media.db.sqlite}"
    exit 0
fi

# ── Configuration ──────────────────────────────────────────────────
SMOKE_DB="${SMOKE_DB:?SMOKE_DB must be explicitly set to an isolated or approved database}"
SMOKE_TTS_WORKER_PATH="${SMOKE_TTS_WORKER_PATH:-scripts/bridges/tts_edge_server.py}"
SMOKE_DRIVE_FOLDER_ID="${SMOKE_DRIVE_FOLDER_ID:-}"
ENDPOINT="/api/media/voiceover/generate"
HEALTH_ENDPOINT="/health"
TAG_PREFIX="vo_b2_$(date +%s)_$$"
RUN_ID="$(date -u +%Y-%m-%dT%H-%M-%SZ)"
REQ_ID="${TAG_PREFIX}_multi"
JOB_ID=""

# ── Setup guards ──────────────────────────────────────────────────
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
    printf '%ssetup error: SMOKE_TTS_WORKER_PATH=%s not found (the persistent worker source must exist so the fan-out child jobs can lazy-start it)%s\n' \
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

# Strict sqlite query (mirrors fase_b_clip_pipeline_smoke.sh::sqlite_q)
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

# ── Precheck 1: Go server is up (GET /health) ────────────────────
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

# ── Precheck 2: Python worker script presence ────────────────────
# Worker exists at canonical path (already guarded by setup check above).
# This explicit log line is for operator traceability.
precheck_worker_script_present() {
    smoke_log_section "Precheck 2: Python worker script presence"
    printf '  %sOK: %s exists (%s bytes)%s\n' \
        "$GREEN" "$SMOKE_TTS_WORKER_PATH" \
        "$(wc -c < "$SMOKE_TTS_WORKER_PATH")" "$RESET"
    return 0
}

# ── Precheck 3: VOICE_OVERRIDES contains it/en/pt ───────────────
# Static parse of the Python source to confirm the worker will pick a
# KNOWN voice for each requested language. Without this gate, a worker
# with a stripped VOICE_OVERRIDES dict would fall back to edge_tts's
# list_voices() at runtime (network-dependent) or to the
# 'en-US-AriaNeural' hardcoded fallback — both are silent degradation
# the test would NOT catch.
#
# Returns 0 if all 3 langs are present, 1 otherwise.
precheck_voice_overrides() {
    smoke_log_section "Precheck 3: VOICE_OVERRIDES contains it/en/pt"
    # Sanity: confirm the VOICE_OVERRIDES dict literal exists before
    # trusting the per-lang parse. A future refactor that renames or
    # relocates the dict would otherwise silently make the lang probes
    # pass on the wrong substring (e.g. a comment with the same
    # quote-lang-quote shape, or a copy of the dict in a test fixture).
    # Allow both `=` (canonical Python) and `:` (mypy / PEP 484 type-annotation
    # style `VOICE_OVERRIDES: dict[str, str] = {`) as the assignment operator.
    if ! grep -qE '^VOICE_OVERRIDES[[:space:]]*[:=][[:space:]]*\{' "$SMOKE_TTS_WORKER_PATH"; then
        printf '%sFAIL: VOICE_OVERRIDES dict literal not found at top-level in %s%s\n' \
            "$RED" "$SMOKE_TTS_WORKER_PATH" "$RESET" >&2
        return 1
    fi
    local missing=()
    for lang in it en pt; do
        # Grep for the exact line: "'<lang>':" (or '"<lang>":' for double-quote variants).
        if ! grep -Eq "['\"]${lang}['\"]:[[:space:]]*['\"][^'\"]+['\"]" "$SMOKE_TTS_WORKER_PATH"; then
            missing+=("$lang")
        fi
    done
    if (( ${#missing[@]} > 0 )); then
        printf '%sFAIL: VOICE_OVERRIDES missing entries for: %s%s\n' \
            "$RED" "${missing[*]}" "$RESET" >&2
        return 1
    fi
    # Surface the 3 voice names that WILL be picked (for the test report).
    for lang in it en pt; do
        local voice
        voice=$(grep -E "['\"]${lang}['\"]:[[:space:]]*['\"][^'\"]+['\"]" "$SMOKE_TTS_WORKER_PATH" |
            head -1 | sed -E "s/.*['\"]${lang}['\"]:[[:space:]]*['\"]([^'\"]+)['\"].*/\1/")
        printf '  %slang=%s → voice=%s%s\n' "$DIM" "$lang" "$voice" "$RESET"
    done
    return 0
}

# ── POST multi-item ──────────────────────────────────────────────
# 3 items: it-IT (DiegoNeural), en-US (GuyNeural), pt-BR (AntonioNeural).
# parallelism=3 enables 3-way sibling fanout via the broker's per-job-type
# Concurrency field. destination.kind=explicit so the test doesn't depend
# on the GroupsResolver being configured.
post_multi_item() {
    smoke_log_section "POST /api/media/voiceover/generate (3 items: it+en+pt)"
    local payload
    payload=$(jq -n --arg rid "$REQ_ID" --arg fid "$SMOKE_DRIVE_FOLDER_ID" '{
        request_id: $rid,
        items: [
            {text: "Prima frase in italiano.",      language: "it-IT", voice: "it-IT-DiegoNeural",   filename: "multi_it.mp3", required: true},
            {text: "Second sentence in English.",   language: "en-US", voice: "en-US-GuyNeural",     filename: "multi_en.mp3", required: true},
            {text: "Terceira frase em portugues.",  language: "pt-BR", voice: "pt-BR-AntonioNeural", filename: "multi_pt.mp3", required: true}
        ],
        destination: {kind: "explicit", folder_id: $fid},
        options: {remove_silence: false, strategy: "verify", parallelism: 3}
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

# ── Poll parent to terminal ─────────────────────────────────────
# Tolerance window: SMOKE_POLL_TIMEOUT_SECONDS (default 120). 3 child
# jobs in parallel with parallelism=3, each child does TTS (~1-3s) +
# Drive upload (~1-3s) + DB commit (~0.1s) = ~3-6s per child. With
# 3 siblings in parallel, total wall-clock is ~6-10s + 5s parent
# aggregator tick (PR-VO-PARENT-AGGREGATOR-SPLIT closure). 120s
# is comfortable; lower it for CI speed only if production is
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

# ── Assertion 2: 3 child jobs of type voiceover.generate_item ───
# Children share the parent's correlation_id (request_id) per
# FanoutVoiceoversUseCase.Execute (fanout.go::requestID propagation).
assert_three_children() {
    smoke_log_section "Assert 2: 3 child jobs (type=voiceover.generate_item)"
    local count
    # Filter to status='SUCCEEDED' so a 2 SUCCEEDED + 1 FAILED partial fan-out
    # surfaces as 2/3 (godlike/07 no-fake-availability). An existence-only
    # count would silently pass on partial success.
    # Note: SQLite LOWER() is ASCII-only — fine for the canonical Go status
    # values (SUCCEEDED / FAILED / PENDING / DEAD_LETTER / etc.) which are
    # pure ASCII. A future unicode status would silently miss this match.
    count=$(sqlite_q "SELECT COUNT(*) FROM jobs WHERE ${CORR_COL} = '${REQ_ID}' AND type = 'voiceover.generate_item' AND LOWER(status) = 'succeeded'")
    if [[ "$count" != "3" ]]; then
        fail "assert2_children_count_${count}_expected_3"
        printf '  %sFAIL: %s SUCCEEDED child jobs found (expected 3)%s\n' "$RED" "$count" "$RESET" >&2
        # Per-sibling diagnostic: surface which language + status + error so
        # operators don't have to open the DB to debug partial fan-out.
        local per_sibling
        per_sibling=$(sqlite_q "SELECT id || '|' || status || '|' || COALESCE(error, '') FROM jobs WHERE ${CORR_COL} = '${REQ_ID}' AND type = 'voiceover.generate_item' AND LOWER(status) != 'succeeded' ORDER BY created_at")
        printf '  failed children (per-sibling diagnostic):\n' >&2
        while IFS='|' read -r sid sstatus serror; do
            [[ -z "$sid" ]] && continue
            printf '    child %s status=%s error=%s\n' \
                "$sid" "$sstatus" "${serror:0:120}" >&2
        done <<< "$per_sibling"
        return 1
    fi
    printf '  %sOK: 3 SUCCEEDED child jobs%s\n' "$GREEN" "$RESET"
    return 0
}

# ── Assertion 3: 3 voiceovers rows ──────────────────────────────
# voiceovers.request_id is the canonical link (per the voiceovers table
# schema — see the user's pasted action plan SQL probe).
assert_three_voiceovers() {
    smoke_log_section "Assert 3: 3 voiceovers rows (request_id=$REQ_ID)"
    local count
    count=$(sqlite_q "SELECT COUNT(*) FROM voiceovers WHERE request_id = '${REQ_ID}'")
    if [[ "$count" != "3" ]]; then
        fail "assert3_voiceovers_count_${count}_expected_3"
        printf '  %sFAIL: %s voiceovers rows found%s\n' "$RED" "$count" "$RESET" >&2
        return 1
    fi
    printf '  %sOK: 3 voiceovers rows%s\n' "$GREEN" "$RESET"
    return 0
}

# ── Assertion 4: 3 media_assets rows for voiceovers ─────────────
# media_assets has no direct request_id column; the canonical link is
# via drive_file_id (set in the finalizer's UpsertVoiceoverProjectionTx).
# We join media_assets against the 3 voiceovers rows from Assert 3.
assert_three_media_assets() {
    smoke_log_section "Assert 4: 3 media_assets rows (linked via drive_file_id)"
    local count
    count=$(sqlite_q "SELECT COUNT(*) FROM media_assets WHERE source = 'voiceover' AND drive_file_id IN (SELECT drive_file_id FROM voiceovers WHERE request_id = '${REQ_ID}' AND drive_file_id != '')")
    if [[ "$count" != "3" ]]; then
        fail "assert4_media_assets_count_${count}_expected_3"
        printf '  %sFAIL: %s media_assets rows found%s\n' "$RED" "$count" "$RESET" >&2
        return 1
    fi
    printf '  %sOK: 3 media_assets rows%s\n' "$GREEN" "$RESET"
    return 0
}

# ── Assertion 5: 3 outbox events for Qdrant ─────────────────────
# Voiceover finalizer writes asset.index.requested events in-TX with
# the voiceover aggregate_id. We scope by time window (last 5 min) to
# tolerate long-running suites + zero in on this test's traffic.
assert_three_outbox_events() {
    smoke_log_section "Assert 5: 3 outbox events (event_type=asset.index.requested)"
    local count
    count=$(sqlite_q "SELECT COUNT(*) FROM outbox_events WHERE event_type = 'asset.index.requested' AND created_at > datetime('now','-5 minutes') AND aggregate_id IN (SELECT id FROM voiceovers WHERE request_id = '${REQ_ID}')")
    if [[ "$count" != "3" ]]; then
        fail "assert5_outbox_count_${count}_expected_3"
        printf '  %sFAIL: %s outbox events found%s\n' "$RED" "$count" "$RESET" >&2
        return 1
    fi
    printf '  %sOK: 3 outbox events%s\n' "$GREEN" "$RESET"
    return 0
}

# ── Assertion 6: parent status='SUCCEEDED' ───────────────────
# godlike/07 no-fake-availability: a 3-children-SUCCEEDED + parent-FAILED
# run (e.g. required-true sibling failed mid-fan-out) MUST surface as FAIL
# even though the count assertions pass. Without this gate the script
# would silently OK on a partial-success run that left the user without
# an aggregated SUCCEEDED state. The poll_parent_to_terminal already
# captures SMOKE_LAST_STATUS (printed in the final report below).
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
    smoke_log_section "Voiceover FASE B2 — multi-item mixed text (it/en/pt)"
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
    post_multi_item || { fail "post_multi_item"; exit 1; }
    poll_parent_to_terminal || { fail "poll_parent_to_terminal"; }

    # 5 count assertions
    assert_one_parent || true
    assert_three_children || true
    assert_three_voiceovers || true
    assert_three_media_assets || true
    assert_three_outbox_events || true
    assert_parent_status_succeeded || true

    echo
    if (( ${#FAILURES[@]} == 0 )); then
        printf '%sOK: FASE B2 — multi-item mixed text PASS (1 parent + 3 child + 3 voiceovers + 3 media_assets + 3 outbox events, parent status=SUCCEEDED)%s\n' \
            "$GREEN" "$RESET"
        printf '  parent terminal status: %s\n' "${SMOKE_LAST_STATUS:-?}"
        exit 0
    fi
    printf '%sFAIL: %d FASE B2 assertion(s) failed:%s\n' \
        "$RED" "${#FAILURES[@]}" "$RESET" >&2
    for f in "${FAILURES[@]}"; do
        printf '  - %s\n' "$f" >&2
    done
    exit 1
}
main "$@"
