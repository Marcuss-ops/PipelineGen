#!/usr/bin/env bash
# tests/operational/artlist/01_startup.sh — Artlist DoD Gate 0 (clean reproducible environment).
#
# Reorg (July 2026): split out of tests/operational/artlist_e2e.sh (now a thin shim).
# Implements gate_preflight that probes the live PipelineGen stack:
#   - PipelineGen server reachable on /health and /ready
#   - artlist job-consumer active
#   - node-scraper reachable at $SCRAPER_URL/health
#   - single node artlist_server.js process
#   - ≤3 headless chrome processes (catches orphan profiles)
#   - SQLite readable at $DB_PATH
#   - no pending Artlist jobs in {QUEUED,LEASED,RUNNING,FINALIZING,RETRY_WAIT}
#   - ffmpeg + ffprobe on PATH
#   - Qdrant at $QDRANT_URL/collections
#   - VELOX_DRIVE_ARTLIST_ROOT configured
#   - Artlist session authenticated via /api/artlist/diagnostics scraper probe
#
# Inherits generic infra from ../../lib ({common,artlist,sqlite}.sh).
# The pending-jobs check will be migrated to lib/sqlite.sh::sqlite_pending_jobs
# once the stub is implemented; current implementation is verbatim.

set -euo pipefail

DIR=$(cd "$(dirname "$0")" && pwd)
# shellcheck disable=SC1091
source "$DIR/../lib/common.sh"
# shellcheck disable=SC1091
source "$DIR/../lib/artlist.sh"
# shellcheck disable=SC1091
source "$DIR/../lib/artlist_runtime.sh"
# shellcheck disable=SC1091
source "$DIR/../lib/sqlite.sh"



smoke_require curl sqlite3 file ffmpeg ffprobe jq



# ── Gate 0 — clean reproducible environment ─────────────────────────────
# Verifies: single node artlist_server.js; one Chrome profile; scraper 9123
# reachable; PipelineGen 8000 reachable; no RUNNING/QUEUED/RETRY_WAIT jobs;
# SQLite readable; ffmpeg+ffprobe on PATH; Qdrant reachable; Drive folder
# set; Artlist session authenticated. Fail-closed on any miss.
gate_preflight() {
    smoke_log_section "Gate 0 — clean reproducible environment"
    local failures=0

    smoke_curl GET "/health" >/dev/null
    if [[ "${SMOKE_LAST_HTTP:-}" =~ ^2[0-9][0-9]$ ]]; then
        log_pass "PipelineGen /health reachable at $BASE_URL"
    else
        log_fail "GET /health failed (HTTP=${SMOKE_LAST_HTTP:-empty})"
        failures=$((failures + 1))
    fi
    smoke_curl GET "/ready" >/dev/null
    if [[ "${SMOKE_LAST_HTTP:-}" =~ ^2[0-9][0-9]$ ]]; then
        log_pass "PipelineGen /ready reachable"
    else
        log_fail "GET /ready failed (HTTP=${SMOKE_LAST_HTTP:-empty})"
        failures=$((failures + 1))
    fi
    smoke_curl GET "/api/artlist/job-consumer" >/dev/null
    if [[ "${SMOKE_LAST_HTTP:-}" =~ ^2[0-9][0-9]$ ]] \
        && jq -e '.active == true and .consumer_type == "media.artlist"' \
            "${SMOKE_LAST_BODY:-/dev/null}" >/dev/null 2>&1; then
        log_pass "artlist job-consumer active"
    else
        log_fail "/api/artlist/job-consumer not active (HTTP=${SMOKE_LAST_HTTP:-empty})"
        failures=$((failures + 1))
    fi

    if ! curl -sS --max-time "$SMOKE_HTTP_TIMEOUT_SECONDS" "$SCRAPER_URL/health" 2>/dev/null \
            | jq -e '.ok == true' >/dev/null 2>&1; then
        log_fail "scraper /health not ok at $SCRAPER_URL/health"
        failures=$((failures + 1))
    else
        log_pass "scraper /health reachable at $SCRAPER_URL"
    fi

    local scraper_count
    scraper_count=$(pgrep -af 'node.*artlist_server\.js' 2>/dev/null | grep -v 'pgrep' | wc -l || true)
    if [[ "${scraper_count}" -gt 1 ]]; then
        log_fail "expected one node artlist_server.js, found ${scraper_count}"
        failures=$((failures + 1))
    else
        log_pass "single node artlist_server.js process"
    fi

    # Single browser/Chrome profile (Puppeteer-launched under node-scraper).
    # Threshold ≤15 because modern headless Chrome forks 1 main + several utility/zygote/gpu helpers per active
    # profile. > 15 = orphaned profiles / parallel browser instances.
    local chrome_total
    chrome_total=$(pgrep -ac 'chrome|chromium' 2>/dev/null || echo 0)
    if [[ "${chrome_total}" -gt 15 ]]; then
        log_fail "expected ≤15 chrome/chromium processes, found ${chrome_total} (multiple headless instances?)"
        failures=$((failures + 1))
    else
        log_pass "chrome/chromium within bounds (${chrome_total})"
    fi

    if [[ ! -f "$DB_PATH" ]]; then
        log_fail "SQLite DB missing: $DB_PATH"
        failures=$((failures + 1))
    else
        log_pass "SQLite readable at $DB_PATH"
    fi

    # No pending Artlist jobs in {QUEUED,LEASED,RUNNING,FINALIZING,RETRY_WAIT}.
    # Catches leftover state from interrupted runs without manual DB intervention.
    # Scoped to type LIKE 'media.artlist%' so unrelated voiceover/stock jobs
    # don't gate the Artlist DoD.
    # Migrated from inline sqlite3 to lib/sqlite.sh::sqlite_pending_jobs
    # (DoD refactor July 2026): the documented TODO about migrating the
    # pending-jobs check lands here. The helper owns the canonical SELECT +
    # the missing-DB fail-closed path; the gate-layer keeps only the metric
    # log lines. Returns "?" + rc=1 on probe failure (surfaced as WARN, not
    # a gate-pass because the DB is missing on dev dry-runs too).
    local pending_jobs rc=0
    pending_jobs=$(sqlite_pending_jobs "$DB_PATH" 2>/dev/null) || rc=$?
    if [[ "${pending_jobs}" == "0" ]]; then
        log_pass "no pending Artlist jobs (sqlite_pending_jobs)"
    elif [[ "${pending_jobs}" == "?" || $rc -ne 0 ]]; then
        log_warn "sqlite_pending_jobs probe inconclusive (DB missing or unreadable)"
    else
        log_fail "expected ZERO pending Artlist jobs, found ${pending_jobs} (sqlite_pending_jobs)"
        failures=$((failures + 1))
    fi

    if ! command -v ffmpeg >/dev/null 2>&1 || ! command -v ffprobe >/dev/null 2>&1; then
        log_fail "ffmpeg + ffprobe required on PATH"
        failures=$((failures + 1))
    else
        log_pass "ffmpeg+ffprobe on PATH"
    fi

    if ! curl -sS --max-time 5 "$QDRANT_URL/collections" 2>/dev/null \
            | jq -e '.result.collections | length >= 0' >/dev/null 2>&1; then
        log_fail "Qdrant unreachable at $QDRANT_URL"
        failures=$((failures + 1))
    else
        log_pass "Qdrant reachable at $QDRANT_URL"
    fi

    if [[ -z "$ARTLIST_ROOT_FOLDER" ]]; then
        log_fail "VELOX_DRIVE_ARTLIST_ROOT not configured (no Artlist Drive root)"
        failures=$((failures + 1))
    else
        log_pass "Artlist Drive root configured"
    fi

    # Artlist session is authenticated iff /api/artlist/diagnostics.scraper.ok == true.
    # The `scraper` probe inside /api/artlist/diagnostics already validates the
    # node-scraper ↔ artlist.io session (per system_prober_http.go::stage_2_session_valid).
    smoke_curl GET "/api/artlist/diagnostics?term=$(printf '%s' "$ARTLIST_TERM" | jq -sRr @uri)" >/dev/null
    if [[ "${SMOKE_LAST_HTTP:-}" =~ ^2[0-9][0-9]$ ]] \
        && jq -e '.scraper.ok == true' "${SMOKE_LAST_BODY:-/dev/null}" >/dev/null 2>&1; then
        log_pass "Artlist session authenticated (/api/artlist/diagnostics scraper probe green)"
    else
        log_fail "Artlist session NOT authenticated (scraper probe not green; HTTP=${SMOKE_LAST_HTTP:-empty})"
        failures=$((failures + 1))
    fi

    if (( failures > 0 )); then
        log_fail "Gate 0 preflight failed (${failures} sub-checks)"
        return 1
    fi
    log_pass "Gate 0 preflight clean"
}

main() {
    if [[ "$DRY_RUN" == "1" ]]; then
        smoke_echo_safe "DRY RUN — preflight probes (Gate 0):"
        printf '  GET  %s/health\n' "$BASE_URL"
        printf '  GET  %s/ready\n' "$BASE_URL"
        printf '  GET  %s/api/artlist/job-consumer\n' "$BASE_URL"
        printf '  GET  %s/health\n' "$SCRAPER_URL"
        printf '  GET  %s/api/artlist/diagnostics?term=<ARTLIST_TERM>\n' "$BASE_URL"
        printf '  pgrep -af node.*artlist_server.js\n'
        printf '  pgrep -ac chrome|chromium\n'
        printf '  sqlite3 %s (pending Artlist jobs)\n' "$DB_PATH"
        exit 0
    fi
    gate_preflight || return 1

    printf '\n============================================\n'
    printf '  01_startup (preflight)\n'
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
