#!/usr/bin/env bash
#
# voiceover_d2_parent_aggregator_state_smoke.sh — black-box FASE D2
# ParentAggregator state-machine smoke for the voiceover pipeline.
#
# Test: ParentAggregator state transition (waiting_children → succeeded).
#   Setup: 1 required item (it-IT + DiegoNeural) — canonical happy path
#          for the ParentAggregator state machine.
#   The aggregator's finalizeParent (internal/application/voiceover/jobs/parent_aggregator.go)
#   transitions the parent through the 4-state voiceover.ParentState wire enum:
#
#     waiting_children → aggregating → succeeded
#                       └─→ partial_success (optional-fail tolerated)
#                       └─→ failed       (required-fail short-circuit)
#
#   The aggregator polls ListAwaitingAggregation every PollInterval
#   (30s production, 5s dev). Once all children are terminal + the
#   StateMachine.Compute() has classified the aggregate, finalizeParent
#   calls jobsSvc.FinalizeAggregateParent with the new (status, parent_state).
#
#   This smoke verifies the FULL transition:
#     1. After POST: parent_state = "waiting_children" (initial; written by
#        toFanoutResultMap at enqueue time).
#     2. After child SUCCEEDS + aggregator tick: parent_state = "succeeded"
#        (terminal; computed by domain state machine when all required
#        children succeed).
#     3. The transition waiting_children → succeeded is observed at least
#        once (initial + final states differ).
#
# godlike/06 SSOT: the parent state is read from the canonical surface —
#   COALESCE(json_extract(result_json,'$.parent_state'), parent_state_typed, '')
# The JSON key is the pre-P1.2 source of truth; the typed column is the
# post-P1.2 SQL dual-write target. COALESCE handles both.
#
# Pre-existing build issues (out of scope per CHANGELOG convention):
#   the same 5-item carry-forward unchanged. The bash is pure-stdlib +
#   sqlite3 (no internal/* imports).
#
# Usage:
#   ./voiceover_d2_parent_aggregator_state_smoke.sh            # real probe
#   ./voiceover_d2_parent_aggregator_state_smoke.sh --dry      # print would-be, exit 0
#   VELOX_ADMIN_TOKEN=<token> \
#     DRIVE_FOLDER_ID=<id> \
#     ./voiceover_d2_parent_aggregator_state_smoke.sh
#
# Exit codes:
#   0   all assertions pass
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
    printf '  precheck: VOICE_OVERRIDES contains "it" (TTS path must work)\n'
    printf '  POST http://%s/api/media/voiceover/generate  (1 item: it-IT, required=true)\n' "$SMOKE_API_BASE"
    printf '  IMMEDIATELY query parent_state: expect waiting_children\n'
    printf '  poll http://%s/api/jobs/<id>/full  (terminal)\n' "$SMOKE_API_BASE"
    printf '  poll parent_state: expect waiting_children → succeeded transition\n'
    printf '  sqlite3 %s   …   (7 assertions: 1 setup + 3 state + 3 child/parent)\n' "${SMOKE_DB:-data/media/media.db.sqlite}"
    exit 0
fi

# ── Configuration ────────────────────────────────────────────────
SMOKE_DB="${SMOKE_DB:?SMOKE_DB must be explicitly set to an isolated or approved database}"
SMOKE_TTS_WORKER_PATH="${SMOKE_TTS_WORKER_PATH:-scripts/bridges/tts_edge_server.py}"
SMOKE_DRIVE_FOLDER_ID="${SMOKE_DRIVE_FOLDER_ID:-}"
ENDPOINT="/api/media/voiceover/generate"
HEALTH_ENDPOINT="/health"
TAG_PREFIX="vo_d2_$(date +%s)_$$"
RUN_ID="$(date -u +%Y-%m-%dT%H-%M-%SZ)"
REQ_ID="${TAG_PREFIX}_parent_aggregator"
JOB_ID=""
PARENT_JOB_ID=""
INITIAL_PARENT_STATE=""
FINAL_PARENT_STATE=""
SETUP_DONE=0
# Tracks whether waiting_children was observed at any point during
# the polling loop. Used by assert_state_transition to tighten the
# "explicit transition" requirement (no more "implicit transition"
# tolerance that papers over the race window).
P1_2_TYPED_AVAILABLE=1
OBSERVED_WAITING_CHILDREN=0

# ── Setup guards ────────────────────────────────────────────────
if [[ ! -f "$SMOKE_DB" ]]; then
    printf '%ssetup error: SMOKE_DB=%s does not exist%s\n' "$RED" "$SMOKE_DB" "$RESET" >&2
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
    # P1.2 migration NOT applied: the typed column doesn't exist yet.
    # Degrade gracefully to the pre-P1.2 surface (JSON key only) so
    # the smoke is runnable on older environments. The transition
    # + final-state assertions still work via get_parent_state's
    # COALESCE (which prefers the JSON key when the typed column is
    # absent). The assert_typed_column_written assertion will skip
    # itself (P1.2_TYPED_AVAILABLE=0).
    printf '%snote: parent_state_typed column missing (P1.2 migration 129 not applied). Running in pre-P1.2 fallback mode — typed column assertions will be skipped, JSON key reading is the source of truth.%s\n' \
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

# godlike/06 SSOT: read the parent state from the canonical surface.
# COALESCE handles both the pre-P1.2 source (JSON key) and the post-P1.2
# source (typed column) — a future CUTOVER wave that drops the JSON key
# will only need to update this COALESCE, not the assertion logic.
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

# ── Precheck: VOICE_OVERRIDES contains 'it' ──────────────────
precheck_voice_overrides() {
    smoke_log_section "Precheck: VOICE_OVERRIDES contains 'it' (TTS path must work for D2 happy path)"
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
    local voice
    voice=$(grep -E "['\"]it['\"]:[[:space:]]*['\"][^'\"]+['\"]" "$SMOKE_TTS_WORKER_PATH" |
        head -1 | sed -E "s/.*['\"]it['\"]:[[:space:]]*['\"]([^'\"]+)['\"].*/\1/")
    printf '  %slang=it → voice=%s (TTS path will succeed → ParentAggregator should reach succeeded)%s\n' \
        "$DIM" "$voice" "$RESET"
    return 0
}

# ── POST single required item ─────────────────────────────────
post_single_required_item() {
    smoke_log_section "POST /api/media/voiceover/generate (1 item: it-IT, required=true)"
    local payload
    payload=$(jq -n --arg rid "$REQ_ID" --arg fid "$SMOKE_DRIVE_FOLDER_ID" '{
        request_id: $rid,
        items: [
            {text: "Test D2 ParentAggregator state machine transition.", language: "it-IT", voice: "it-IT-DiegoNeural", filename: "vo_d2_state.mp3", required: true}
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
    PARENT_JOB_ID=$(jq -r '.job_id // .id // empty' "$SMOKE_LAST_BODY" 2>/dev/null || echo "")
    if [[ -z "$PARENT_JOB_ID" ]]; then
        printf '%sFAIL: POST returned no job_id in body%s\n' "$RED" "$RESET" >&2
        smoke_echo_safe "  body: $(head -c 300 "$SMOKE_LAST_BODY" 2>/dev/null || true)" >&2
        return 1
    fi
    printf '  %senqueued parent job_id=%s (correlation_id=%s)%s\n' \
        "$GREEN" "$PARENT_JOB_ID" "$REQ_ID" "$RESET"
    return 0
}

# ── Capture initial parent state (IMMEDIATELY after POST) ─────
# The parent enters the waiting_children state at enqueue time
# (toFanoutResultMap writes result.parent_state = "waiting_children"
# synchronously in HandleJob). The aggregator tick that flips it
# to a terminal state runs on PollInterval (30s prod / 5s dev) —
# so the initial capture SHOULD observe waiting_children if
# the query runs before the first aggregator tick.
capture_initial_parent_state() {
    smoke_log_section "Capture initial parent_state (before any aggregator tick)"
    # Tight race window: aggregator may have already ticked for very
    # fast child runs. The test tolerates either initial state
    # (waiting_children OR a terminal value) but requires the state
    # to be NON-EMPTY (i.e. the column was populated by some path).
    INITIAL_PARENT_STATE=$(get_parent_state "$PARENT_JOB_ID")
    printf '  initial parent_state=%q (captured %s after POST)\n' \
        "$INITIAL_PARENT_STATE" "$(date +%H:%M:%S.%N 2>/dev/null | head -c 12 || date +%H:%M:%S)"
    if [[ -z "$INITIAL_PARENT_STATE" ]]; then
        printf '  %snote: parent_state is empty — toFanoutResultMap did not write the JSON key. Verify generate_handler.go::toFanoutResultMap emits parent_state=waiting_children.%s\n' \
            "$YELLOW" "$RESET" >&2
        return 1
    fi
    return 0
}

# ── Poll parent to terminal ───────────────────────────────────
# Polls /api/jobs/{id}/full (broker status) until SUCCEEDED or FAILED.
# 1 child does TTS (~1-3s) + Drive upload (~1-3s) + DB commit (~0.1s)
# = ~3-6s. Plus 30s aggregator tick (production default) = ~33-36s.
# Plus optional retry ticks = ~60-90s worst case. 120s default is
# comfortable; lower it for CI only if production is consistently
# faster.
poll_parent_to_terminal() {
    smoke_log_section "Poll parent broker status to terminal (timeout ${SMOKE_POLL_TIMEOUT_SECONDS}s)"
    if ! smoke_poll_terminal "$PARENT_JOB_ID"; then
        printf '%sFAIL: parent job %s did not reach terminal in %ss (last status=%s)%s\n' \
            "$RED" "$PARENT_JOB_ID" "$SMOKE_POLL_TIMEOUT_SECONDS" "${SMOKE_LAST_STATUS:-?}" "$RESET" >&2
        return 1
    fi
    printf '  %sOK: parent broker status reached terminal status=%s%s\n' "$GREEN" "$SMOKE_LAST_STATUS" "$RESET"
    return 0
}

# ── Poll parent_state to terminal value (waiting_children → succeeded) ─
# Aggregator tick runs on PollInterval (30s production). We poll the
# parent_state column directly (more reliable than the /full endpoint)
# until it matches "succeeded" (or any non-waiting_children value).
poll_parent_state_to_terminal() {
    smoke_log_section "Poll parent_state to terminal value (expect 'succeeded' after aggregator tick)"
    local deadline=$(( $(date +%s) + SMOKE_POLL_TIMEOUT_SECONDS ))
    local state=""
    while (( $(date +%s) < deadline )); do
        smoke_wallclock_check
        state=$(get_parent_state "$PARENT_JOB_ID")
        # Track whether waiting_children was observed at any point.
        if [[ "$state" == "waiting_children" ]]; then
            OBSERVED_WAITING_CHILDREN=1
        fi
        if [[ "$state" == "succeeded" || "$state" == "failed" || "$state" == "partial_success" ]]; then
            FINAL_PARENT_STATE="$state"
            printf '  %sOK: parent_state reached terminal value=%s%s\n' "$GREEN" "$FINAL_PARENT_STATE" "$RESET"
            return 0
        fi
        sleep "$SMOKE_POLL_INTERVAL_SECONDS"
    done
    FINAL_PARENT_STATE="$state"
    printf '%sFAIL: parent_state did not reach terminal value in %ss (last value=%q)%s\n' \
        "$RED" "$SMOKE_POLL_TIMEOUT_SECONDS" "$state" "$RESET" >&2
    return 1
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
    printf '  %sOK: 1 parent job (id=%s)%s\n' "$GREEN" "$PARENT_JOB_ID" "$RESET"
    return 0
}

# ── Assertion 2: 1 child job with status=SUCCEEDED ─────────────
assert_one_child_succeeded() {
    smoke_log_section "Assert 2: 1 child job (type=voiceover.generate_item, status=SUCCEEDED)"
    local count
    # Note: SQLite LOWER() is ASCII-only — fine for the canonical Go status
    # values (SUCCEEDED / FAILED / PENDING / DEAD_LETTER / etc.) which are
    # pure ASCII. A future unicode status would silently miss this match.
    count=$(sqlite_q "SELECT COUNT(*) FROM jobs WHERE ${CORR_COL} = '${REQ_ID}' AND type = 'voiceover.generate_item' AND LOWER(status) = 'succeeded'")
    if [[ "$count" != "1" ]]; then
        fail "assert2_child_count_${count}_expected_1"
        printf '  %sFAIL: %s SUCCEEDED child jobs found (expected 1)%s\n' "$RED" "$count" "$RESET" >&2
        local per_sibling
        per_sibling=$(sqlite_q "SELECT id || '|' || status || '|' || COALESCE(error, '') FROM jobs WHERE ${CORR_COL} = '${REQ_ID}' AND type = 'voiceover.generate_item' AND LOWER(status) != 'succeeded' ORDER BY created_at")
        if [[ -n "$per_sibling" ]]; then
            printf '  failed children (per-sibling diagnostic):\n' >&2
            while IFS='|' read -r sid sstatus serror; do
                [[ -z "$sid" ]] && continue
                printf '    child %s status=%s error=%s\n' "$sid" "$sstatus" "${serror:0:120}" >&2
            done <<< "$per_sibling"
        fi
        return 1
    fi
    printf '  %sOK: 1 SUCCEEDED child job%s\n' "$GREEN" "$RESET"
    return 0
}

# ── Assertion 3: initial parent_state = 'waiting_children' ──────
# The parent enters the waiting_children state at enqueue time
# (toFanoutResultMap writes it synchronously). For a single-child
# test, the aggregator MIGHT have already ticked by the time we
# query — but the transition must be observed. If the initial
# capture is already 'succeeded', the transition is implicit
# (waiting_children was set then immediately transitioned); we
# accept that for the happy path. If initial is 'waiting_children',
# the transition is explicit.
assert_initial_parent_state() {
    smoke_log_section "Assert 3: initial parent_state observed (waiting_children OR succeeded — fast-tick tolerant)"
    if [[ -z "$INITIAL_PARENT_STATE" ]]; then
        fail "assert3_initial_state_empty"
        printf '  %sFAIL: initial parent_state is empty (toFanoutResultMap did not write the JSON key)%s\n' "$RED" "$RESET" >&2
        return 1
    fi
    if [[ "$INITIAL_PARENT_STATE" != "waiting_children" && "$INITIAL_PARENT_STATE" != "succeeded" ]]; then
        fail "assert3_initial_state_${INITIAL_PARENT_STATE}_unexpected"
        printf '  %sFAIL: initial parent_state=%s (expected waiting_children or succeeded)%s\n' \
            "$RED" "$INITIAL_PARENT_STATE" "$RESET" >&2
        return 1
    fi
    printf '  %sOK: initial parent_state=%s%s\n' "$GREEN" "$INITIAL_PARENT_STATE" "$RESET"
    return 0
}

# ── Assertion 4: final parent_state = 'succeeded' ─────────────
# After the aggregator tick + FinalizeAggregateParent, the
# parent_state MUST be "succeeded" (the canonical happy-path
# terminal value for all-required-children-succeeded).
assert_final_parent_state() {
    smoke_log_section "Assert 4: final parent_state = 'succeeded' (after aggregator tick)"
    if [[ -z "$FINAL_PARENT_STATE" ]]; then
        fail "assert4_final_state_empty"
        printf '  %sFAIL: final parent_state is empty (poll_parent_state_to_terminal did not run or aggregator did not tick)%s\n' "$RED" "$RESET" >&2
        return 1
    fi
    if [[ "$FINAL_PARENT_STATE" != "succeeded" ]]; then
        fail "assert4_final_state_${FINAL_PARENT_STATE}_expected_succeeded"
        printf '  %sFAIL: final parent_state=%s (expected succeeded)%s\n' \
            "$RED" "$FINAL_PARENT_STATE" "$RESET" >&2
        return 1
    fi
    printf '  %sOK: final parent_state=succeeded (ParentAggregator flipped it from waiting_children → succeeded)%s\n' \
        "$GREEN" "$RESET"
    return 0
}

# ── Assertion 5: state transition observed (waiting_children → succeeded) ──
# The transition is the load-bearing evidence that the ParentAggregator
# state machine is wired correctly. We require AT LEAST ONE observation
# of `waiting_children` at any point during the polling loop (initial
# capture OR a poll tick) — if we never observed it, the state machine
# skipped the in-flight phase (a real observability bug, not a fast-tick
# artifact). Tightening this per code-reviewer round 1: the previous
# "implicit transition" tolerance papered over the race window.
assert_state_transition() {
    smoke_log_section "Assert 5: state transition observed (waiting_children → succeeded, tightened: requires OBSERVED_WAITING_CHILDREN=1)"
    if (( OBSERVED_WAITING_CHILDREN == 0 )); then
        fail "assert5_no_waiting_children_observed"
        printf '  %sFAIL: waiting_children was NEVER observed during the polling loop (initial=%s, final=%s). This indicates the aggregator ticked BEFORE the smoke could observe the in-flight phase — bump the aggregator PollInterval to ≥10s or run the smoke against a slower pipeline.%s\n' \
            "$RED" "$INITIAL_PARENT_STATE" "$FINAL_PARENT_STATE" "$RESET" >&2
        return 1
    fi
    if [[ "$INITIAL_PARENT_STATE" == "waiting_children" && "$FINAL_PARENT_STATE" == "succeeded" ]]; then
        printf '  %sOK: explicit transition observed (initial=waiting_children → final=succeeded)%s\n' "$GREEN" "$RESET"
        return 0
    fi
    if [[ "$INITIAL_PARENT_STATE" == "succeeded" && "$FINAL_PARENT_STATE" == "succeeded" ]]; then
        printf '  %sOK: mid-poll observation of waiting_children + terminal=succeeded (aggregator ticked between POST and initial capture, but the in-flight phase was observed during polling)%s\n' "$GREEN" "$RESET"
        return 0
    fi
    fail "assert5_transition_${INITIAL_PARENT_STATE}_to_${FINAL_PARENT_STATE}"
    printf '  %sFAIL: unexpected transition %s → %s%s\n' \
        "$RED" "$INITIAL_PARENT_STATE" "$FINAL_PARENT_STATE" "$RESET" >&2
    return 1
}

# ── Assertion 6: typed column written (P1.2 SQL dual-write) ─
# P1.2 migration 129 added parent_state_typed. The SQL layer
# (repository_lifecycle.go:285-310) writes the typed column in
# the SAME transaction as the JSON key. After the aggregator tick,
# the typed column MUST be non-empty (canonical post-P1.2).
#
# Pre-P1.2 fallback (per code-reviewer round 1): if the typed
# column doesn't exist (P1_2_TYPED_AVAILABLE=0, set at setup),
# the assertion SKIPs itself with a yellow note instead of FAILing.
# This makes the smoke runnable on environments where migration 129
# has not yet been applied.
assert_typed_column_written() {
    smoke_log_section "Assert 6: parent_state_typed column written (P1.2 SQL dual-write; pre-P1.2 fallback if column missing)"
    if (( P1_2_TYPED_AVAILABLE == 0 )); then
        printf '  %sskip (pre-P1.2 fallback): parent_state_typed column does not exist on this DB. The JSON key is the source of truth for this assertion.%s\n' \
            "$YELLOW" "$RESET"
        return 0
    fi
    local typed_value
    typed_value=$(sqlite_q "SELECT COALESCE(parent_state_typed, '') FROM jobs WHERE id = '${PARENT_JOB_ID}'")
    if [[ -z "$typed_value" ]]; then
        fail "assert6_typed_column_empty"
        printf '  %sFAIL: parent_state_typed is empty (P1.2 SQL dual-write not yet implemented; pre-P1.2 JSON key reading is the fallback)%s\n' \
            "$RED" "$RESET" >&2
        return 1
    fi
    if [[ "$typed_value" != "succeeded" ]]; then
        fail "assert6_typed_value_${typed_value}_expected_succeeded"
        printf '  %sFAIL: parent_state_typed=%s (expected succeeded)%s\n' \
            "$RED" "$typed_value" "$RESET" >&2
        return 1
    fi
    printf '  %sOK: parent_state_typed=succeeded (P1.2 SQL dual-write confirmed)%s\n' "$GREEN" "$RESET"
    return 0
}

# ── Assertion 7: parent broker status = 'SUCCEEDED' ───────────
assert_parent_broker_status() {
    smoke_log_section "Assert 7: parent broker status = 'SUCCEEDED'"
    local count
    count=$(sqlite_q "SELECT COUNT(*) FROM jobs WHERE ${CORR_COL} = '${REQ_ID}' AND type = 'voiceover.generate' AND LOWER(status) = 'succeeded'")
    if [[ "$count" != "1" ]]; then
        local actual_status
        actual_status=$(sqlite_q "SELECT COALESCE(status, '(null)') FROM jobs WHERE ${CORR_COL} = '${REQ_ID}' AND type = 'voiceover.generate' LIMIT 1")
        fail "assert7_broker_status_${actual_status}_expected_SUCCEEDED"
        printf '  %sFAIL: parent broker status=%s (expected SUCCEEDED)%s\n' "$RED" "$actual_status" "$RESET" >&2
        return 1
    fi
    printf '  %sOK: parent broker status=SUCCEEDED%s\n' "$GREEN" "$RESET"
    return 0
}

main() {
    smoke_log_section "Voiceover FASE D2 — ParentAggregator state machine (waiting_children → succeeded)"
    printf '  target:   %s\n  db:       %s\n  worker:   %s\n  folder:   %s\n  tag:      %s\n  run_id:   %s\n' \
        "$SMOKE_API_BASE" "$SMOKE_DB" "$SMOKE_TTS_WORKER_PATH" \
        "$SMOKE_DRIVE_FOLDER_ID" "$TAG_PREFIX" "$RUN_ID"

    precheck_go_server_up || { fail "precheck_go_server_up"; exit 1; }
    precheck_voice_overrides || { fail "precheck_voice_overrides"; exit 1; }

    post_single_required_item || { fail "post_single_required_item"; exit 1; }

    # Tight window: capture initial state RIGHT after POST, before
    # the aggregator's first tick can flip it.
    capture_initial_parent_state || true

    # Poll broker status to terminal (child SUCCEEDED + parent aggregator
    # marks parent broker=SUCCEEDED when domain state machine classifies).
    poll_parent_to_terminal || { fail "poll_parent_to_terminal"; }

    # Poll parent_state to terminal value (waiting_children → succeeded).
    poll_parent_state_to_terminal || { fail "poll_parent_state_to_terminal"; }

    assert_one_parent || true
    assert_one_child_succeeded || true
    assert_initial_parent_state || true
    assert_final_parent_state || true
    assert_state_transition || true
    assert_typed_column_written || true
    assert_parent_broker_status || true

    echo
    if (( ${#FAILURES[@]} == 0 )); then
        printf '%sOK: FASE D2 — ParentAggregator state machine PASS (initial=%s → final=%s, 7 assertions)%s\n' \
            "$GREEN" "$INITIAL_PARENT_STATE" "$FINAL_PARENT_STATE" "$RESET"
        printf '  parent broker terminal status: %s\n' "${SMOKE_LAST_STATUS:-?}"
        exit 0
    fi
    printf '%sFAIL: %d FASE D2 assertion(s) failed:%s\n' "$RED" "${#FAILURES[@]}" "$RESET" >&2
    for f in "${FAILURES[@]}"; do printf '  - %s\n' "$f" >&2; done
    exit 1
}
main "$@"
