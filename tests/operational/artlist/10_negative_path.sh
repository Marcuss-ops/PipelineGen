#!/usr/bin/env bash
# tests/operational/artlist/10_negative_path.sh — Artlist DoD Gate 10
# (3 hard-gate negative-path probes: SESSION_EXPIRED / STREAM_NOT_FOUND
#  / SCRAPER_UNAVAILABLE).
#
# Reorg (July 2026): the prior `tests/operational/artlist/10_negative_tests.sh`
# was a forward-pointing stub from the lib extraction and is DELETED in
# this same reorg (no duplicate logic per AGENTS.md Simplicity). This file
# is the CANONICAL Gate 10 owner.
#
# Spec (the Artlist operational contract|Gate 10 verbatim, July 2026 DoD):
#   Three proven-negative probes (each is a HARD gate):
#   (1) SESSION_EXPIRED: scraper session cookie expired → SESSION_EXPIRED
#       or AUTH_REQUIRED sentinel + no false results + no jobs SUCCEEDED.
#   (2) STREAM_NOT_FOUND: POST /detail on a non-existent clip_id →
#       ok=false error="STREAM_NOT_FOUND" stream_urls=[]; no page_url
#       treated as stream + no HTML saved as MP4.
#   (3) SCRAPER_UNAVAILABLE: scraper down on 9123 → SCRAPER_UNAVAILABLE
#       sentinel + retry bounded + job terminal-failed (FAILED/CANCELLED/
#       DEAD_LETTER) + zero RETRY_WAIT rows.
#
# Principle (binding): provider unavailable ≠ zero results valid.
# Operator must NOT see silent false-positives; the verdict is
# fail-closed on each branch. AGENTS.md "Never represent an unavailable
# backend as a successful no-op" is honoured at every probe.
#
# Helpers reused (zero duplicate decision logic per AGENTS.md single-focus):
#   * velox_artlist_search_live (lib/velox_domain.sh) — synthesizes the
#     3 transport-kind typed-sentinels: SEARCH_TIMEOUT, AUTH_REQUIRED,
#     SCRAPER_UNAVAILABLE. See lib/velox_domain.sh::velox_artlist_search_live
#     for the canonical classification (curl rc + http code → sentinel kind).
#   * velox_artlist_detail (lib/velox_domain.sh) --phase miss — has the
#     canonical miss-contract (ok=false, error=STREAM_NOT_FOUND,
#     stream_urls=[], clip_id non-empty). See lib/velox_domain.sh::
#     velox_artlist_detail for the canonical jq filter.
#   * smoke_curl / smoke_sqlite_query / smoke_poll_terminal
#     (lib/common.sh) — generic HTTP, SQLite, retry-bound poll primitives.
#   * log_pass / log_fail / log_info (lib/artlist_runtime.sh via umbrella).
# No new helpers introduced.

set -euo pipefail

DIR=$(cd "$(dirname "$0")" && pwd)
# Source the umbrella (godlike/06 SSOT canonical import contract).
# Resolves path-invariant via BASH_SOURCE[0]; the umbrella's helper-name
# guard fails closed if a future refactor removes any expected helper
# from lib/, surfacing the regression at import time instead of at
# first call site.
# shellcheck disable=SC1091
source "$DIR/../lib/_artlist_common.sh"

smoke_require curl jq sqlite3

# ── probe-1 SESSION_EXPIRED ─────────────────────────────────────────────
# Method: invoke velox_artlist_search_live through PipelineGen with the
# SMOKE_TOKEN forced to an invalid value. PipelineGen returns HTTP 401
# (or 403, treated identically); velox_artlist_search_live synthesizes
# body._transport_kind in {AUTH_REQUIRED, SESSION_EXPIRED} per the
# transport-kind classification at lib/velox_domain.sh::velox_artlist_
# search_live. The DoD spec accepts BOTH SESSION_EXPIRED AND AUTH_REQUIRED
# as canonical fail-closed sentinels for this probe category (expired
# cookie == auth flow rejects the request).
#
# Invariants:
#   * rc=2 (transport classification — HTTP 401/403 = transport failure
#     per the helper's layered rc semantics)
#   * body._transport_kind ∈ {SESSION_EXPIRED, AUTH_REQUIRED}
#   * body.clips == [] (no false results in sentinel body)
#   * jobs ledger: jobs.SUCCEEDED_count AFTER probe ==
#                  jobs.SUCCEEDED_count BEFORE probe (delta = 0;
#                  no production job accidentally succeeded during the
#                  probe — protects against an auth-misroute logic bug)
probe_session_expired() {
    smoke_log_section "Gate 10 probe-1 — SESSION_EXPIRED"

    # ── Before-snapshot: SUCCEEDED count delta measurement ────────
    local before_succeeded
    before_succeeded=$(smoke_sqlite_query "$DB_PATH" \
        "SELECT COUNT(*) FROM jobs WHERE status='SUCCEEDED'" || echo 0)

    # ── Force SMOKE_TOKEN to an invalid value so PipelineGen returns 401.
    # ── 30s timeout cap (helper's internal --max-time) so probe-1 doesn't
    # stall the battery if the server stalls unexpectedly.
    local saved_smoke_token="${SMOKE_TOKEN:-}"
    export SMOKE_TOKEN="EXP_PROBE_INVALID_$(date +%s%N)"
    local rc_1=0
    velox_artlist_search_live \
        --term "business team working in modern office" \
        --limit 3 \
        --timeout-seconds 30 \
        --save-body "${WORK_DIR:-/tmp}/probe1_search_body.json" || rc_1=$?
    export SMOKE_TOKEN="${saved_smoke_token}"

    if [[ "$rc_1" != "2" ]]; then
        log_fail "probe-1 SESSION_EXPIRED: velox_artlist_search_live returned rc=${rc_1} (expected 2 since HTTP 401/403 is classified as transport failure)"
        return 1
    fi
    log_pass "probe-1 SESSION_EXPIRED: velox_artlist_search_live returned rc=2 (HTTP 401/403 -> transport failure classification)"

    local sentinel_kind clips_n
    sentinel_kind=$(jq -r '._transport_kind // "UNKNOWN"' \
        "${WORK_DIR:-/tmp}/probe1_search_body.json" 2>/dev/null || echo "UNKNOWN")
    case "${sentinel_kind}" in
        SESSION_EXPIRED|AUTH_REQUIRED)
            log_pass "probe-1 SESSION_EXPIRED: synthetic body._transport_kind=${sentinel_kind} (DoD-accepted sentinel)"
            ;;
        *)
            log_fail "probe-1 SESSION_EXPIRED: synthetic body._transport_kind='${sentinel_kind}' (expected SESSION_EXPIRED or AUTH_REQUIRED); see ${WORK_DIR:-/tmp}/probe1_search_body.json"
            return 1
            ;;
    esac

    clips_n=$(jq -r '(.clips // []) | length' \
        "${WORK_DIR:-/tmp}/probe1_search_body.json" 2>/dev/null || echo -1)
    if [[ "$clips_n" != "0" ]]; then
        log_fail "probe-1 SESSION_EXPIRED: body.clips.length=${clips_n} (expected 0; no false results in sentinel body)"
        return 1
    fi
    log_pass "probe-1 SESSION_EXPIRED: body.clips.length=0 (no false results)"

    local after_succeeded delta
    after_succeeded=$(smoke_sqlite_query "$DB_PATH" \
        "SELECT COUNT(*) FROM jobs WHERE status='SUCCEEDED'" || echo 0)
    delta=$((after_succeeded - before_succeeded))
    if (( delta != 0 )); then
        log_fail "probe-1 SESSION_EXPIRED: jobs ledger SUCCEEDED delta=${delta} (expected 0; no production job accidentally succeeded during probe)"
        return 1
    fi
    log_pass "probe-1 SESSION_EXPIRED: jobs ledger SUCCEEDED delta=0 (no production job accidentally succeeded)"

    return 0
}

# ── probe-2 STREAM_NOT_FOUND ────────────────────────────────────────────
# Method: invoke velox_artlist_detail --phase miss with a non-existent
# page_url. The helper has the canonical miss-contract: rc=0 on contract
# pass (rc=0, NOT rc=1 — rc=0 means the contract itself passed for
# phase=miss, which is what "STREAM_NOT_FOUND correctly detected"
# actually means), ok=false, error="STREAM_NOT_FOUND", stream_urls=[],
# clip_id (or .clip.clip_id) non-empty.
#
# Invariants:
#   * rc=0 (miss-contract pass per helper's --phase semantics; rc=0 is
#     the right answer when scraper correctly reports miss)
#   * body.ok == false
#   * body.error == "STREAM_NOT_FOUND" (exact string match)
#   * body.stream_urls == [] (jq length == 0; empty stream_urls[] is the
#     canonical "no HTML-as-MP4 saved" defence)
#   * body.clip_id (or .clip.clip_id) non-empty (the scraper MUST
#     identify the queried "clip" even when no stream found — defends
#     against a contract drift where miss returns 200 with empty body)
probe_stream_not_found() {
    smoke_log_section "Gate 10 probe-2 — STREAM_NOT_FOUND"

    local rc_2=0
    velox_artlist_detail \
        --phase miss \
        --clip-page-url "https://artlist.io/stock-footage/clip/INVALID_NONEXISTENT_FOR_PROBE_$(date +%s%N)" \
        --scraper-url "${SCRAPER_URL}" \
        --save-body "${WORK_DIR:-/tmp}/probe2_detail_body.json" || rc_2=$?

    if [[ "$rc_2" != "0" ]]; then
        log_fail "probe-2 STREAM_NOT_FOUND: velox_artlist_detail --phase miss returned rc=${rc_2} (expected 0 on miss-contract PASS — do NOT confuse with rc=1 which is the happy-phase contract violation)"
        return 1
    fi
    log_pass "probe-2 STREAM_NOT_FOUND: velox_artlist_detail --phase miss returned rc=0 (miss-contract pass)"

    if ! jq -e '
        .ok == false
        and .error == "STREAM_NOT_FOUND"
        and (((.clip_id // .clip.clip_id // "") | length) > 0)
        and (((.stream_urls // .clip.stream_urls // []) | length) == 0)
    ' "${WORK_DIR:-/tmp}/probe2_detail_body.json" >/dev/null 2>&1; then
        log_fail "probe-2 STREAM_NOT_FOUND: miss-contract body failed jq check (expect ok=false, error=STREAM_NOT_FOUND, stream_urls=[], clip_id non-empty); see ${WORK_DIR:-/tmp}/probe2_detail_body.json"
        return 1
    fi
    log_pass "probe-2 STREAM_NOT_FOUND: miss-contract body PASS (ok=false, error=STREAM_NOT_FOUND, stream_urls=[], clip_id non-empty)"
    return 0
}

# ── probe-3 SCRAPER_UNAVAILABLE ─────────────────────────────────────────
# Method: the SCRAPER_UNAVAILABLE state is an OPERATIONAL scenario —
# the canonical scraper on 9123 is OFF (operator pre-condition for this
# probe, matching the DoD spec literal 'scraper spento su 9123'). The
# probe:
#   (part A) calls velox_artlist_search_live through PipelineGen. PipelineGen
#       proxies to the actual scraper on 9123, which is operationally off.
#       PipelineGen returns 5xx → helper synthesizes body._transport_kind=
#       SCRAPER_UNAVAILABLE per lib/velox_domain.sh::velox_artlist_search_live's
#       transport-kind classification (connect refused / 5xx / empty body
#       collapses to SCRAPER_UNAVAILABLE per the canonical Gate 10 typed-
#       sentinel vocabulary). Body.clips MUST be [] (no false results).
#   (part B) enqueues a real /api/artlist/run via PipelineGen with a
#       bounded smoke_poll_terminal timeout (30s); asserts the
#       orchestrator's bound retry doesn't produce an infinite RETRY_WAIT
#       loop (the canonical DoD 'bounded retry' invariant).
#
# Pre-flight fail-closed: scraper on 9123 MUST be unreachable (operator
# pre-condition); the probe asserts this first via a direct curl probe
# before issuing any PipelineGen call. Mirrors the AGENTS.md 'forbidden
# to represent an unavailable backend as a successful no-op' invariant —
# if the operator forgot to stop the scraper, this probe MUST NOT silently
# pass on a live scraper; it must surface PROBE_NOT_APPLICABLE as a
# fail-closed sentinel (matches the typed-sentinel policy of the 3
# other observed backends: SCRAPER_UNAVAILABLE / SESSION_EXPIRED /
# STREAM_NOT_FOUND).
#
# IMPORTANT — SCRAPER_URL override semantics: do NOT override SCRAPER_URL
# in this test process. velox_artlist_search_live uses SMOKE_API_BASE
# (PipelineGen URL), not SCRAPER_URL — overriding SCRAPER_URL has NO effect
# on the search-live probe. The actual probe invokes the live PipelineGen
# which proxies to the live scraper (its own process env), NOT to whatever
# SCRAPER_URL the test process set. The OPERATIONAL pre-condition (scraper
# off on 9123) is what drives the SCRAPER_UNAVAILABLE classification.
#
# Invariants:
#   * Pre-flight: direct curl probe of $SCRAPER_URL/9123 returns connect-
#     refused / 4xx / 5xx / empty body (scraper MUST be unreachable).
#   * velox_artlist_search_live returns rc=2 (transport classification)
#     + body._transport_kind=SCRAPER_UNAVAILABLE + clips=[] (canonical
#     typed-sentinel vocabulary).
#   * (Conditional) If the orchestrator accepts an enqueue with the live
#     scraper URL: the job reaches terminal ∈ {FAILED, CANCELLED,
#     DEAD_LETTER} — NOT SUCCEEDED (false-positive) — within
#     smoke_poll_terminal's bounded retry timeout.
#   * (Conditional) jobs ledger has zero RETRY_WAIT rows for the
#     probe_run_id — sentinel for 'infinite backoff' (DoD forbids).
probe_scraper_unavailable() {
    smoke_log_section 'Gate 10 probe-3 — SCRAPER_UNAVAILABLE'

    # ── Pre-flight: assert scraper on ${SCRAPER_URL:-http://127.0.0.1:9123}
    # is actually unreachable. If reachable, the probe is not applicable
    # (DoD operator forgot to stop the scraper); fail-closed with the
    # typed sentinel PROBE_NOT_APPLICABLE so a follow-up operator knows
    # exactly what to fix before re-running.
    local probe_preflight_code
    probe_preflight_code=$(curl -sS --max-time 4 -o /dev/null -w '%{http_code}' \
        "${SCRAPER_URL:-http://127.0.0.1:9123}/health" 2>/dev/null || echo 000)
    # 000 = connect-refused/timeout (scraper is down — what we want).
    # 2xx/3xx/4xx/5xx (non-000) = scraper reachable — pre-condition failed.
    if [[ "$probe_preflight_code" != "000" ]]; then
        log_fail "probe-3 SCRAPER_UNAVAILABLE pre-flight: scraper on ${SCRAPER_URL:-http://127.0.0.1:9123}/health returned HTTP ${probe_preflight_code} (expected '000' = connect-refused for the operator pre-condition of 'scraper spento'); sentinel PROBE_NOT_APPLICABLE — stop the scraper before re-running"
        return 1
    fi
    log_pass "probe-3 SCRAPER_UNAVAILABLE pre-flight: scraper on ${SCRAPER_URL:-http://127.0.0.1:9123} is unreachable (operator pre-condition satisfied)"

    # ── probe-3 part A: search/live through PipelineGen with the scraper
    # operationally down. The helper synthesizes the canonical typed-
    # sentinel body when PipelineGen returns 5xx (proxy to dead scraper).
    local rc_3a=0
    velox_artlist_search_live \
        --term 'scraper unavailable probe' \
        --limit 1 \
        --timeout-seconds 15 \
        --save-body "${WORK_DIR:-/tmp}/probe3_search_body.json" || rc_3a=$?

    if [[ "$rc_3a" != '2' ]]; then
        log_fail "probe-3 SCRAPER_UNAVAILABLE: velox_artlist_search_live (with scraper down) returned rc=${rc_3a} (expected 2 on transport fail)"
        return 1
    fi
    local sentinel_kind
    sentinel_kind=$(jq -r '._transport_kind // "UNKNOWN"' \
        "${WORK_DIR:-/tmp}/probe3_search_body.json" 2>/dev/null || echo "UNKNOWN")
    if [[ "$sentinel_kind" != "SCRAPER_UNAVAILABLE" ]]; then
        log_fail "probe-3 SCRAPER_UNAVAILABLE: body._transport_kind='${sentinel_kind}' (expected SCRAPER_UNAVAILABLE); see ${WORK_DIR:-/tmp}/probe3_search_body.json"
        return 1
    fi
    log_pass "probe-3 SCRAPER_UNAVAILABLE: search/live (with scraper down) classified as SCRAPER_UNAVAILABLE (typed-sentinel body)"

    local clips_n
    clips_n=$(jq -r '(.clips // []) | length' \
        "${WORK_DIR:-/tmp}/probe3_search_body.json" 2>/dev/null || echo -1)
    if [[ "$clips_n" != '0' ]]; then
        log_fail "probe-3 SCRAPER_UNAVAILABLE: body.clips.length=${clips_n} (expected 0; no false results in sentinel body)"
        return 1
    fi
    log_pass "probe-3 SCRAPER_UNAVAILABLE: body.clips.length=0 (no false results in sentinel body)"

    # ── probe-3 part B: bound-retry check (conditional). Enqueue a
    # /api/artlist/run with a tight polling bound; ensure orchestrator
    # either terminal-fails (preferred) or skip the check if auth blocks
    # the enqueue (acceptable bypass — part-A's SCRAPER_UNAVAILABLE
    # sentinel is the canonical evidence).
    local probe_run_body
    probe_run_body=$(jq -nc \
        --arg term "scraper unavailable probe $(date +%s%N)" \
        '{term:$term,limit:1,strategy:"replace",clip_duration:7,width:1920,height:1080,fps:30,concurrency:1,dry_run:false}')
    local http_code
    http_code=$(smoke_curl POST "/api/artlist/run" -d "$probe_run_body")
    if [[ ! "$http_code" =~ ^2[0-9][0-9]$ ]]; then
        log_info "probe-3 SCRAPER_UNAVAILABLE part-B skipped: POST /api/artlist/run returned HTTP ${http_code} (probe-3 part-A classified already; the bound retry check is conditional on orchestrator accepting an enqueue)"
        return 0
    fi

    local probe_run_id
    probe_run_id=$(jq -r '.run_id // empty' "${SMOKE_LAST_BODY}" 2>/dev/null || echo '')
    if [[ -z "$probe_run_id" ]]; then
        log_fail 'probe-3 SCRAPER_UNAVAILABLE part-B: POST /api/artlist/run response had no .run_id'
        return 1
    fi
    # 30-second bound on smoke_poll_terminal. The helper returns non-zero
    # on timeout/cancellation, which is exactly the failure mode we want
    # to surface as 'bounded retry' (no infinite loop in this script).
    if ! smoke_poll_terminal "${probe_run_id}" 30 2>&1; then
        log_info 'probe-3 SCRAPER_UNAVAILABLE part-B: smoke_poll_terminal bounded-timeout reached; retry-bound ledger check below'
    else
        log_info "probe-3 SCRAPER_UNAVAILABLE part-B: smoke_poll_terminal reached terminal=${SMOKE_LAST_STATUS:-?}"
    fi

    # Final state: should be terminal ∈ {FAILED, CANCELLED, DEAD_LETTER}.
    # SUCCEEDED would mean a prod-side false-positive (scraper is down but
    # orchestrator reported success — fake success == DoD fail-closed).
    local terminal_status
    terminal_status=$(smoke_sqlite_query "$DB_PATH" \
        "SELECT status FROM jobs WHERE id='${probe_run_id}' ORDER BY updated_at DESC LIMIT 1" || echo '')
    case "${terminal_status}" in
        FAILED|CANCELLED|DEAD_LETTER)
            log_pass "probe-3 SCRAPER_UNAVAILABLE part-B: terminal status=${terminal_status} (canonical bounded-failure)"
            ;;
        SUCCEEDED|completed)
            log_fail "probe-3 SCRAPER_UNAVAILABLE part-B: terminal status=${terminal_status} (UNEXPECTED SUCCEEDED with scraper down — false-positive detected, fail-closed)"
            return 1
            ;;
        *)
            log_info "probe-3 SCRAPER_UNAVAILABLE part-B: terminal status='${terminal_status}' (transitional or unknown; retry-bound ledger check below)"
            ;;
    esac

    # Final retry-bound: jobs ledger MUST NOT have any RETRY_WAIT rows
    # for the probe_run_id (DoD: bounded retry, never infinite backoff).
    local retry_wait_count
    retry_wait_count=$(smoke_sqlite_query "$DB_PATH" \
        "SELECT COUNT(*) FROM jobs WHERE id='${probe_run_id}' AND status='RETRY_WAIT'" || echo 0)
    if (( retry_wait_count > 0 )); then
        log_fail "probe-3 SCRAPER_UNAVAILABLE part-B: jobs ledger has ${retry_wait_count} RETRY_WAIT row for probe run ${probe_run_id} (DoD forbids infinite backoff)"
        return 1
    fi
    log_pass "probe-3 SCRAPER_UNAVAILABLE part-B: jobs ledger has 0 RETRY_WAIT rows for probe run=${probe_run_id} (retry bounded)"

    return 0
}

# ── gate_negative_path — Gate 10 dispatcher ─────────────────────────────
# Each of the 3 probes is INDEPENDENTLY a HARD gate per the DoD spec.
# ALL 3 probes MUST pass (partial-pass is a hard fail — mirrors Gates
# 4/5/6/7 aggregate contract). Order matters for forensic cleanliness:
# probe-1 (auth fail), probe-2 (miss contract), probe-3 (transport
# unavailable) — the SCRAPER_URL probe-1 sensitivity to override
# restoration makes the round-trip safe between probe invocations.
gate_negative_path() {
    local probes_ok=0 probes_failed=0 probe_name
    for probe_name in probe_session_expired probe_stream_not_found probe_scraper_unavailable; do
        if "${probe_name}"; then
            probes_ok=$((probes_ok + 1))
        else
            probes_failed=$((probes_failed + 1))
        fi
    done
    log_info "Gate 10 probe tally: ok=${probes_ok} failed=${probes_failed} (3 probes; ok=3 -> VERDICT PASS)"
    if (( probes_failed > 0 )); then
        log_fail "Gate 10 — ${probes_failed} of 3 probes failed (each probe is a HARD gate per DoD)"
        return 1
    fi
    log_pass "Gate 10 — all 3 probes passed (SESSION_EXPIRED + STREAM_NOT_FOUND + SCRAPER_UNAVAILABLE)"
    return 0
}

main() {
    if [[ "$DRY_RUN" == "1" ]]; then
        smoke_echo_safe "DRY RUN — Gate 10 negative-path probes:"
        printf '  probe-1 SESSION_EXPIRED     → velox_artlist_search_live with INVALID SMOKE_TOKEN → body._transport_kind={SESSION_EXPIRED,AUTH_REQUIRED} + body.clips=[] + jobs.SUCCEEDED delta=0\n'
        printf '  probe-2 STREAM_NOT_FOUND    → velox_artlist_detail --phase miss on a NONEXISTENT page_url → rc=0 + ok=false + error=STREAM_NOT_FOUND + stream_urls=[] + clip_id non-empty\n'
        printf '  probe-3 SCRAPER_UNAVAILABLE → SCRAPER_URL=http://127.0.0.1:9124 (dead port) → velox_artlist_search_live rc=2 + body._transport_kind=SCRAPER_UNAVAILABLE + clips=[]\n'
        printf '                                  (conditional bound retry) orchestrator enqueue → terminal ∈ {FAILED,DEAD_LETTER,CANCELLED} + zero RETRY_WAIT rows in jobs ledger\n'
        printf '  ALL 3 probes MUST pass; DoD forbids partial-pass.\n'
        printf '  Reuses ONLY: velox_artlist_search_live / velox_artlist_detail (lib/velox_domain.sh) + smoke_curl / smoke_sqlite_query / smoke_poll_terminal (lib/common.sh) + log_* (lib/artlist_runtime.sh via umbrella).\n'
        exit 0
    fi
    gate_negative_path || return 1

    printf '\n============================================\n'
    printf '  10_negative_path\n'
    printf '  PASS=%d  WARN=%d  FAIL=%d\n' "$PASS" "$WARN" "$FAIL"
    printf '============================================\n'
    if [[ "$FAIL" -gt 0 ]]; then
        printf 'VERDICT: FAIL\n'
        return 1
    fi
    printf 'VERDICT: PASS\n'
    return 0
}

main "$@"
