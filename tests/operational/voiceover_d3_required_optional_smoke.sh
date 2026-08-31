#!/usr/bin/env bash
#
# voiceover_d3_required_optional_smoke.sh — black-box FASE D3
# Required/Optional failure-path smoke for the voiceover pipeline.
#
# Test: Required vs Optional failure semantics — 2 sub-cases run sequentially:
#
#   D3a — required-fail:
#     Setup: 1 required item with a FAKE voice name (e.g.
#            "FAKE-VOICE-DOES-NOT-EXIST"). The TTS server (edge_tts)
#            rejects the invalid voice → child FAILED.
#     Expected: parent_state=waiting_children → parent_state=failed
#               + parent broker status=FAILED
#     Rationale: a REQUIRED-failed child short-circuits the domain
#                state machine to FailedTerminal in Transition()
#                rule ① (per parent_aggregator.go::aggregateOne).
#                This is the godlike/07 "fail-closed at the gate" path.
#
#   D3b — optional-fail:
#     Setup: 2 items: first required=true with valid voice (it-IT
#            DiegoNeural) + second required=false with FAKE voice.
#     Expected: parent_state=waiting_children → parent_state=partial_success
#               + parent broker status=SUCCEEDED
#     Rationale: an OPTIONAL-failed child does NOT short-circuit the
#                domain state machine. The parent's StateMachine.Compute()
#                classifies as ParentStateSucceeded (because the required
#                child succeeded) + ≥1 failure → voiceover.ParentPartialSuccess
#                (per parent_state_machine.go::domainToVoiceoverParentState).
#                The parent broker is still SUCCEEDED — the optional
#                failure is tolerated. This is the godlike/06 "optional
#                items are best-effort" path.
#
# Each sub-case has its own precheck + POST + poll + assertions. The 2
# sub-cases share the same TTS worker + Drive folder (no setup/cleanup
# needed between them). Each uses a unique REQ_ID tag for log + DB
# correlation.
#
# godlike/06 SSOT: parent state is read from COALESCE(json_extract(...),
# parent_state_typed, '') — same pattern as D2. The typed column is
# the canonical post-P1.2 source; the JSON key is the pre-P1.2 fallback.
#
# Honest-limitation: the FAKE voice name relies on edge_tts rejecting
# the voice (returns "no audio was received" or similar). If the
# TTS server falls back to a default voice on invalid input, the
# child would SUCCEED and the assertions would fail. This is a
# tight contract with the TTS server; the dry-run mode prints
# the assumption so operators can verify on their worker version.
#
# Per code-reviewer round 1: a runtime TTS-fake-voice precheck was
# considered but not added — the TTS worker is a Python subprocess
# (port private to the Go process per AGENTS.md), so direct probing
# requires re-running the worker standalone. Operators should verify
# their edge_tts version's invalid-voice behaviour against this
# FAKE_VOICE constant before relying on D3 in CI. If the worker
# version tolerates invalid voices, D3a + D3b both FAIL with
# "child status=SUCCEEDED, expected FAILED" — the diagnostic points
# to the edge_tts version as the most likely root cause.
#
# Usage:
#   ./voiceover_d3_required_optional_smoke.sh            # real probe
#   ./voiceover_d3_required_optional_smoke.sh --dry      # print would-be, exit 0
#   VELOX_ADMIN_TOKEN=<token> \
#     DRIVE_FOLDER_ID=<id> \
#     ./voiceover_d3_required_optional_smoke.sh
#
# Exit codes:
#   0   all assertions pass (both D3a + D3b sub-cases)
#   1   one or more assertions failed
#   2   setup error
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
    printf '  precheck: VOICE_OVERRIDES contains "it" (valid TTS path for D3b required item)\n'
    printf '  D3a — required-fail: POST 1 required item with FAKE voice → expect child FAILED + parent state=failed + broker=FAILED\n'
    printf '  D3b — optional-fail: POST 2 items (1 required valid + 1 optional FAKE) → expect 1 SUCCEEDED + 1 FAILED child + parent state=partial_success + broker=SUCCEEDED\n'
    printf '  sqlite3 %s   …   (per-sub-case: 1 parent + child/children + initial/final state + transition + typed column + broker status)\n' "${SMOKE_DB:-data/media/media.db.sqlite}"
    printf '  HONEST LIMITATION: FAKE voice name relies on edge_tts rejecting invalid voices. If your TTS server falls back, D3a + D3b both fail. Verify on your worker version.\n'
    exit 0
fi

# ── Configuration ────────────────────────────────────────────────
SMOKE_DB="${SMOKE_DB:?SMOKE_DB must be explicitly set to an isolated or approved database}"
SMOKE_TTS_WORKER_PATH="${SMOKE_TTS_WORKER_PATH:-scripts/bridges/tts_edge_server.py}"
SMOKE_DRIVE_FOLDER_ID="${SMOKE_DRIVE_FOLDER_ID:-}"
ENDPOINT="/api/media/voiceover/generate"
HEALTH_ENDPOINT="/health"
TAG_PREFIX="vo_d3_$(date +%s)_$$"
RUN_ID="$(date -u +%Y-%m-%dT%H-%M-%SZ)"
# P1.2 SQL dual-write fallback (set at setup time by the schema check
# block). When 0, typed-column assertions skip themselves; the JSON
# key reading in get_parent_state is the source of truth.
P1_2_TYPED_AVAILABLE=1

# FAKE voice name — used to force TTS failure. The exact name doesn't
# matter as long as edge_tts rejects it (returns "no audio was
# received" or similar). The string includes spaces + special chars
# to maximise the chance of rejection across edge_tts versions.
FAKE_VOICE="FAKE-VOICE-DOES-NOT-EXIST-X9K2"

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
    printf '%ssetup error: SMOKE_TTS_WORKER_PATH=%s not found%s\n' \
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

# Verify parent_state_typed column exists (P1.2 migration applied)
SCHEMA_TYPED_COL=$(sqlite3 "$SMOKE_DB" \
    "SELECT COUNT(*) FROM pragma_table_info('jobs') WHERE name = 'parent_state_typed'" \
    2>/tmp/smoke_typed_err)
SCHEMA_TYPED_RC=$?
if (( SCHEMA_TYPED_RC != 0 )); then
    printf '%ssetup error: pragma_table_info(parent_state_typed) failed (exit %d):%s %s\n' \
        "$RED" "$SCHEMA_TYPED_RC" "$RESET" \
        "$(cat /tmp/smoke_typed_err 2>/dev/null || true)" >&2
    rm -f /tmp/smoke_typed_err
    exit 2
fi
rm -f /tmp/smoke_typed_err
if [[ "$SCHEMA_TYPED_COL" != "1" ]]; then
    # P1.2 migration NOT applied: graceful fallback (mirrors D2).
    # The JSON key is the source of truth; typed column assertions
    # skip themselves with a yellow note.
    printf '%snote: parent_state_typed column missing (P1.2 migration 129 not applied). Running in pre-P1.2 fallback mode.%s\n' \
        "$YELLOW" "$RESET" >&2
    P1_2_TYPED_AVAILABLE=0
else
    P1_2_TYPED_AVAILABLE=1
fi

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

# godlike/06 SSOT: read parent state from canonical surface (D2 pattern).
get_parent_state() {
    local parent_id="$1"
    sqlite_q "SELECT COALESCE(json_extract(result_json, '\$.parent_state'), parent_state_typed, '') FROM jobs WHERE id = '${parent_id}'"
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

# ── Precheck: VOICE_OVERRIDES contains 'it' (D3b required item needs valid TTS) ─
precheck_voice_overrides() {
    smoke_log_section "Precheck: VOICE_OVERRIDES contains 'it' (D3b required item needs valid TTS path)"
    if ! grep -qE '^VOICE_OVERRIDES[[:space:]]*[:=][[:space:]]*\{' "$SMOKE_TTS_WORKER_PATH"; then
        printf '%sFAIL: VOICE_OVERRIDES dict literal not found at top-level in %s%s\n' \
            "$RED" "$SMOKE_TTS_WORKER_PATH" "$RESET" >&2
        return 1
    fi
    if ! grep -Eq "['\"]it['\"]:[[:space:]]*['\"][^'\"]+['\"]" "$SMOKE_TTS_WORKER_PATH"; then
        printf '%sFAIL: VOICE_OVERRIDES missing entry for 'it'%s\n' "$RED" "$RESET" >&2
        return 1
    fi
    local voice
    voice=$(grep -E "['\"]it['\"]:[[:space:]]*['\"][^'\"]+['\"]" "$SMOKE_TTS_WORKER_PATH" |
        head -1 | sed -E "s/.*['\"]it['\"]:[[:space:]]*['\"]([^'\"]+)['\"].*/\1/")
    printf '  %slang=it → voice=%s (D3b required item will use this voice; D3a + D3b optional item use FAKE voice)%s\n' \
        "$DIM" "$voice" "$RESET"
    return 0
}

# ── D3a — POST 1 required item with FAKE voice ─────────────────
# Required=true + fake voice → child FAILED → parent state=failed
# + parent broker=FAILED. Aggregator short-circuits to FailedTerminal
# in Transition() rule ①.
post_d3a_required_fail() {
    local rid="$1"
    smoke_log_section "D3a — POST /api/media/voiceover/generate (1 required item with FAKE voice → expect child FAILED + parent state=failed)"
    local payload
    payload=$(jq -n --arg rid "$rid" --arg fid "$SMOKE_DRIVE_FOLDER_ID" --arg fv "$FAKE_VOICE" '{
        request_id: $rid,
        items: [
            {text: "Test D3a required-fail.", language: "it-IT", voice: $fv, filename: "vo_d3a_required_fail.mp3", required: true}
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
    local job_id
    job_id=$(jq -r '.job_id // .id // empty' "$SMOKE_LAST_BODY" 2>/dev/null || echo "")
    if [[ -z "$job_id" ]]; then
        printf '%sFAIL: POST returned no job_id in body%s\n' "$RED" "$RESET" >&2
        smoke_echo_safe "  body: $(head -c 300 "$SMOKE_LAST_BODY" 2>/dev/null || true)" >&2
        return 1
    fi
    printf '  %senqueued D3a parent job_id=%s (correlation_id=%s, voice=%s)%s\n' \
        "$GREEN" "$job_id" "$rid" "$FAKE_VOICE" "$RESET"
    D3A_PARENT_ID="$job_id"
    return 0
}

# ── D3b — POST 2 items: 1 required valid + 1 optional FAKE ─────
# Required item SUCCEEDS + optional item FAILS → parent state=partial_success
# + parent broker=SUCCEEDED. Optional failure is tolerated.
post_d3b_optional_fail() {
    local rid="$1"
    smoke_log_section "D3b — POST /api/media/voiceover/generate (1 required valid + 1 optional FAKE → expect child SUCCEEDED + child FAILED + parent state=partial_success + broker=SUCCEEDED)"
    local payload
    payload=$(jq -n --arg rid "$rid" --arg fid "$SMOKE_DRIVE_FOLDER_ID" --arg fv "$FAKE_VOICE" '{
        request_id: $rid,
        items: [
            {text: "Test D3b required valid item.", language: "it-IT", voice: "it-IT-DiegoNeural", filename: "vo_d3b_required.mp3", required: true},
            {text: "Test D3b optional fail item.", language: "it-IT", voice: $fv, filename: "vo_d3b_optional_fail.mp3", required: false}
        ],
        destination: {kind: "explicit", folder_id: $fid},
        options: {remove_silence: false, strategy: "verify", parallelism: 2}
    }')
    local code
    code=$(smoke_curl POST "$ENDPOINT" --data "$payload")
    if ! smoke_assert_http_2xx "POST $ENDPOINT"; then
        smoke_echo_safe "  body: $(head -c 300 "$SMOKE_LAST_BODY" 2>/dev/null || true)" >&2
        return 1
    fi
    local job_id
    job_id=$(jq -r '.job_id // .id // empty' "$SMOKE_LAST_BODY" 2>/dev/null || echo "")
    if [[ -z "$job_id" ]]; then
        printf '%sFAIL: POST returned no job_id in body%s\n' "$RED" "$RESET" >&2
        smoke_echo_safe "  body: $(head -c 300 "$SMOKE_LAST_BODY" 2>/dev/null || true)" >&2
        return 1
    fi
    printf '  %senqueued D3b parent job_id=%s (correlation_id=%s, 2 items: required=DiegoNeural + optional=%s)%s\n' \
        "$GREEN" "$job_id" "$rid" "$FAKE_VOICE" "$RESET"
    D3B_PARENT_ID="$job_id"
    return 0
}

# ── Capture initial parent state (immediately after POST) ─────
capture_initial_parent_state() {
    local parent_id="$1"
    local label="$2"
    local state
    state=$(get_parent_state "$parent_id")
    printf '  %s initial parent_state=%q\n' "$label" "$state"
    printf '%s' "$state"
}

# ── Poll parent_state to terminal value (any of 3 terminals) ──
poll_parent_state_to_terminal() {
    local parent_id="$1"
    local label="$2"
    local deadline=$(( $(date +%s) + SMOKE_POLL_TIMEOUT_SECONDS ))
    local state=""
    while (( $(date +%s) < deadline )); do
        smoke_wallclock_check
        state=$(get_parent_state "$parent_id")
        if [[ "$state" == "succeeded" || "$state" == "failed" || "$state" == "partial_success" ]]; then
            printf '  %sOK (%s): parent_state reached terminal value=%s%s\n' "$GREEN" "$label" "$state" "$RESET"
            printf '%s' "$state"
            return 0
        fi
        sleep "$SMOKE_POLL_INTERVAL_SECONDS"
    done
    printf '%sFAIL (%s): parent_state did not reach terminal value in %ss (last value=%q)%s\n' \
        "$RED" "$label" "$SMOKE_POLL_TIMEOUT_SECONDS" "$state" "$RESET" >&2
    printf '%s' "$state"
    return 1
}

# ── Assertion 1: D3a — 1 parent + 1 child (status=FAILED) + state=failed + broker=FAILED ─
assert_d3a_results() {
    local rid="$1"
    local parent_id="$2"
    local initial="$3"
    local final="$4"
    smoke_log_section "D3a — Assert 1: 1 child job with status=FAILED (TTS rejected FAKE voice)"
    local count
    count=$(sqlite_q "SELECT COUNT(*) FROM jobs WHERE ${CORR_COL} = '${rid}' AND type = 'voiceover.generate_item' AND LOWER(status) = 'failed'")
    if [[ "$count" != "1" ]]; then
        fail "d3a_assert1_child_failed_count_${count}_expected_1"
        printf '  %sFAIL: %s FAILED child jobs found (expected 1)%s\n' "$RED" "$count" "$RESET" >&2
        local per_sibling
        per_sibling=$(sqlite_q "SELECT id || '|' || status || '|' || COALESCE(error, '') FROM jobs WHERE ${CORR_COL} = '${rid}' AND type = 'voiceover.generate_item' AND LOWER(status) != 'failed' ORDER BY created_at")
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
    child_error=$(sqlite_q "SELECT COALESCE(error, '(null)') FROM jobs WHERE ${CORR_COL} = '${rid}' AND type = 'voiceover.generate_item' LIMIT 1")
    printf '  %sOK: 1 FAILED child (error=%s)%s\n' "$GREEN" "${child_error:0:100}" "$RESET"
    echo
    smoke_log_section "D3a — Assert 2: final parent_state = 'failed' (required-fail short-circuit)"
    if [[ "$final" != "failed" ]]; then
        fail "d3a_assert2_final_state_${final}_expected_failed"
        printf '  %sFAIL: final parent_state=%s (expected failed)%s\n' "$RED" "$final" "$RESET" >&2
        return 1
    fi
    printf '  %sOK: final parent_state=failed (Transition() rule ① short-circuited the domain state machine)%s\n' "$GREEN" "$RESET"
    echo
    smoke_log_section "D3a — Assert 3: parent_state_typed = 'failed' (P1.2 SQL dual-write; pre-P1.2 fallback if column missing)"
    if (( P1_2_TYPED_AVAILABLE == 0 )); then
        printf '  %sskip (pre-P1.2 fallback): parent_state_typed column does not exist. The JSON key reading in get_parent_state is the source of truth.%s\n' \
            "$YELLOW" "$RESET"
    else
        local typed
        typed=$(sqlite_q "SELECT COALESCE(parent_state_typed, '') FROM jobs WHERE id = '${parent_id}'")
        if [[ "$typed" != "failed" ]]; then
            fail "d3a_assert3_typed_${typed}_expected_failed"
            printf '  %sFAIL: parent_state_typed=%s (expected failed)%s\n' "$RED" "$typed" "$RESET" >&2
            return 1
        fi
        printf '  %sOK: parent_state_typed=failed%s\n' "$GREEN" "$RESET"
    fi
    echo
    smoke_log_section "D3a — Assert 4: parent broker status = 'FAILED'"
    local bcount
    bcount=$(sqlite_q "SELECT COUNT(*) FROM jobs WHERE ${CORR_COL} = '${rid}' AND type = 'voiceover.generate' AND LOWER(status) = 'failed'")
    if [[ "$bcount" != "1" ]]; then
        local actual
        actual=$(sqlite_q "SELECT COALESCE(status, '(null)') FROM jobs WHERE ${CORR_COL} = '${rid}' AND type = 'voiceover.generate' LIMIT 1")
        fail "d3a_assert4_broker_${actual}_expected_FAILED"
        printf '  %sFAIL: parent broker status=%s (expected FAILED)%s\n' "$RED" "$actual" "$RESET" >&2
        return 1
    fi
    printf '  %sOK: parent broker status=FAILED%s\n' "$GREEN" "$RESET"
    return 0
}

# ── Assertion 5-8: D3b — 2 children (1 SUCCEEDED + 1 FAILED) + state=partial_success + broker=SUCCEEDED ─
assert_d3b_results() {
    local rid="$1"
    local parent_id="$2"
    local initial="$3"
    local final="$4"
    smoke_log_section "D3b — Assert 5: 2 children (1 SUCCEEDED + 1 FAILED — required valid + optional FAKE)"
    local succ_count fail_count
    succ_count=$(sqlite_q "SELECT COUNT(*) FROM jobs WHERE ${CORR_COL} = '${rid}' AND type = 'voiceover.generate_item' AND LOWER(status) = 'succeeded'")
    fail_count=$(sqlite_q "SELECT COUNT(*) FROM jobs WHERE ${CORR_COL} = '${rid}' AND type = 'voiceover.generate_item' AND LOWER(status) = 'failed'")
    if [[ "$succ_count" != "1" || "$fail_count" != "1" ]]; then
        fail "d3b_assert5_children_${succ_count}_succ_${fail_count}_fail_expected_1_1"
        printf '  %sFAIL: %s SUCCEEDED + %s FAILED (expected 1+1)%s\n' "$RED" "$succ_count" "$fail_count" "$RESET" >&2
        local per_sibling
        per_sibling=$(sqlite_q "SELECT id || '|' || status || '|' || COALESCE(error, '') FROM jobs WHERE ${CORR_COL} = '${rid}' AND type = 'voiceover.generate_item' ORDER BY created_at")
        if [[ -n "$per_sibling" ]]; then
            printf '  children (per-sibling diagnostic):\n' >&2
            while IFS='|' read -r sid sstatus serror; do
                [[ -z "$sid" ]] && continue
                printf '    child %s status=%s error=%s\n' "$sid" "$sstatus" "${serror:0:120}" >&2
            done <<< "$per_sibling"
        fi
        return 1
    fi
    printf '  %sOK: 1 SUCCEEDED (required) + 1 FAILED (optional) child%s\n' "$GREEN" "$RESET"
    echo
    smoke_log_section "D3b — Assert 6: final parent_state = 'partial_success' (optional-fail tolerated, domain state machine → Succeeded+≥1 failure → partial_success)"
    if [[ "$final" != "partial_success" ]]; then
        fail "d3b_assert6_final_state_${final}_expected_partial_success"
        printf '  %sFAIL: final parent_state=%s (expected partial_success)%s\n' "$RED" "$final" "$RESET" >&2
        return 1
    fi
    printf '  %sOK: final parent_state=partial_success (domainToVoiceoverParentState mapping: Succeeded+≥1 failure → partial_success)%s\n' "$GREEN" "$RESET"
    echo
    smoke_log_section "D3b — Assert 7: parent_state_typed = 'partial_success' (P1.2 SQL dual-write; pre-P1.2 fallback if column missing)"
    if (( P1_2_TYPED_AVAILABLE == 0 )); then
        printf '  %sskip (pre-P1.2 fallback): parent_state_typed column does not exist. The JSON key reading in get_parent_state is the source of truth.%s\n' \
            "$YELLOW" "$RESET"
    else
        local typed
        typed=$(sqlite_q "SELECT COALESCE(parent_state_typed, '') FROM jobs WHERE id = '${parent_id}'")
        if [[ "$typed" != "partial_success" ]]; then
            fail "d3b_assert7_typed_${typed}_expected_partial_success"
            printf '  %sFAIL: parent_state_typed=%s (expected partial_success)%s\n' "$RED" "$typed" "$RESET" >&2
            return 1
        fi
        printf '  %sOK: parent_state_typed=partial_success%s\n' "$GREEN" "$RESET"
    fi
    echo
    smoke_log_section "D3b — Assert 8: parent broker status = 'SUCCEEDED' (optional failure is tolerated)"
    local bcount
    bcount=$(sqlite_q "SELECT COUNT(*) FROM jobs WHERE ${CORR_COL} = '${rid}' AND type = 'voiceover.generate' AND LOWER(status) = 'succeeded'")
    if [[ "$bcount" != "1" ]]; then
        local actual
        actual=$(sqlite_q "SELECT COALESCE(status, '(null)') FROM jobs WHERE ${CORR_COL} = '${rid}' AND type = 'voiceover.generate' LIMIT 1")
        fail "d3b_assert8_broker_${actual}_expected_SUCCEEDED"
        printf '  %sFAIL: parent broker status=%s (expected SUCCEEDED — optional failure is tolerated)%s\n' "$RED" "$actual" "$RESET" >&2
        return 1
    fi
    printf '  %sOK: parent broker status=SUCCEEDED (optional failure tolerated — the godlike/06 "best-effort" path)%s\n' "$GREEN" "$RESET"
    return 0
}

# ── Per-sub-case runner ───────────────────────────────────────
# Captures initial state, polls broker terminal, polls parent_state to
# terminal, then runs the sub-case-specific assertions. Returns 0 on
# full success, 1 on any failure.
run_subcase() {
    local label="$1"        # "D3a" or "D3b"
    local rid="$2"
    local parent_id="$3"
    shift 3
    # The remaining args are the assertion-function name to call.
    local assertion_fn="$1"

    # Capture initial state in tight window.
    local initial
    initial=$(capture_initial_parent_state "$parent_id" "$label")
    echo
    # Poll broker to terminal (needed for the parent aggregator to
    # start computing).
    if ! smoke_poll_terminal "$parent_id"; then
        fail "${label,,}_poll_terminal"
        printf '%sFAIL (%s): parent job %s did not reach terminal in %ss%s\n' \
            "$RED" "$label" "$parent_id" "$SMOKE_POLL_TIMEOUT_SECONDS" "$RESET" >&2
        return 1
    fi
    printf '  %sOK (%s): parent broker status reached terminal status=%s%s\n' "$GREEN" "$label" "$SMOKE_LAST_STATUS" "$RESET"
    echo
    # Poll parent_state to terminal value.
    local final
    final=$(poll_parent_state_to_terminal "$parent_id" "$label")
    if [[ $? -ne 0 ]]; then
        fail "${label,,}_poll_parent_state"
        return 1
    fi
    echo
    # Sub-case-specific assertions.
    "$assertion_fn" "$rid" "$parent_id" "$initial" "$final" || true
    return 0
}

main() {
    smoke_log_section "Voiceover FASE D3 — Required vs Optional failure semantics (2 sub-cases: D3a required-fail + D3b optional-fail)"
    printf '  target:   %s\n  db:       %s\n  worker:   %s\n  folder:   %s\n  fake_voice: %s\n  tag:      %s\n  run_id:   %s\n' \
        "$SMOKE_API_BASE" "$SMOKE_DB" "$SMOKE_TTS_WORKER_PATH" \
        "$SMOKE_DRIVE_FOLDER_ID" "$FAKE_VOICE" "$TAG_PREFIX" "$RUN_ID"

    precheck_go_server_up || { fail "precheck_go_server_up"; exit 1; }
    precheck_voice_overrides || { fail "precheck_voice_overrides"; exit 1; }

    # ── D3a — required-fail ──────────────────────────────
    D3A_REQ_ID="${TAG_PREFIX}_a_required_fail"
    D3A_PARENT_ID=""
    post_d3a_required_fail "$D3A_REQ_ID" || { fail "post_d3a_required_fail"; exit 1; }
    run_subcase "D3a" "$D3A_REQ_ID" "$D3A_PARENT_ID" assert_d3a_results || true

    echo
    echo

    # ── D3b — optional-fail ─────────────────────────────
    D3B_REQ_ID="${TAG_PREFIX}_b_optional_fail"
    D3B_PARENT_ID=""
    post_d3b_optional_fail "$D3B_REQ_ID" || { fail "post_d3b_optional_fail"; exit 1; }
    run_subcase "D3b" "$D3B_REQ_ID" "$D3B_PARENT_ID" assert_d3b_results || true

    echo
    if (( ${#FAILURES[@]} == 0 )); then
        printf '%sOK: FASE D3 — Required/Optional failure semantics PASS (8 assertions: 4 D3a + 4 D3b)%s\n' "$GREEN" "$RESET"
        printf '  D3a parent: %s (state=failed, broker=FAILED)\n' "$D3A_PARENT_ID"
        printf '  D3b parent: %s (state=partial_success, broker=SUCCEEDED)\n' "$D3B_PARENT_ID"
        exit 0
    fi
    printf '%sFAIL: %d FASE D3 assertion(s) failed:%s\n' "$RED" "${#FAILURES[@]}" "$RESET" >&2
    for f in "${FAILURES[@]}"; do printf '  - %s\n' "$f" >&2; done
    exit 1
}
main "$@"
