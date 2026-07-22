#!/usr/bin/env bash
# artlist_live_prep.sh — prep phase for artlist_live_e2e_verify.sh
#
# Sourced by the main shim AFTER env.sh. Performs all fail-closed
# preflight checks + the § 1 live-search probe + (optional) the
# HERMETIC GATES gate-test run. Any fail-closed failure exits 2 BEFORE
# the run phase consumes an Artlist quota.
#
# Cross-phase reads (from env.sh-sourced state):
#   TOKEN / BASE_URL / SCRAPER_URL / QDRANT_URL / DB_PATH / COLLECTION /
#   SCRAPER_CONNECT_TIMEOUT_SECONDS / CURL_TIMEOUT / SCROLL_TIMEOUT /
#   SEARCH_TERM / LIMIT / EXPECTED_GATE_MATCHES / SKIP_HERMETICS
#
# Cross-phase writes (consumed by assert.sh + teardown.sh):
#   SCRAPER_PROBE / SCRAPER_OK / SCRAPER_CLIPS  (used by teardown JSON)
#   QHEADERS bash array  (used by assert.sh for Qdrant scroll)
#
# Source-only guard: same pattern as env.sh.

if [[ "${BASH_SOURCE[0]}" == "${0}" ]]; then
    echo "[ERROR] artlist_live_prep.sh must be sourced, not executed directly." >&2
    exit 1
fi

# ============================================================
# Pre-flight (fail-closed: missing token/ready means exit 2)
# ============================================================
log_info "=== Artlist LIVE E2E Verification ==="

require_tool jq
require_tool sqlite3
require_tool curl

if [[ -z "${TOKEN}" ]]; then
    log_fail "VELOX_ADMIN_TOKEN is not set (refuse to run with empty token)"
    exit 2
fi

# Preflight guardrail — SCROLL_TIMEOUT minimum-budget threshold (per godlike/06 SSOT to
# docs/operations/stock-e2e-runbook.md §11.0 row "SCROLL_TIMEOUT"; 60s is the §11.0 lower
# bound, 120s the doc-public default after the fix(scraper) series). Fail-closed: a too-low
# total-budget means the Node scraper's connect-plus-render will race against the
# connect-timeout, producing false CANCELLED/RETRY_WAIT signals on cold Chromium startups
# (Chromium cold start + first nav needs ~30s alone; a 30s scraper total budget would
# always lose the race even on a healthy node-scraper). Both non-numeric AND sub-60 inputs
# fail-closed here — no silently-accepted operator error.
if ! [[ "${SCROLL_TIMEOUT}" =~ ^[0-9]+$ ]] || [[ "${SCROLL_TIMEOUT}" -lt 60 ]]; then
    log_fail "SCROLL_TIMEOUT='${SCROLL_TIMEOUT}' is invalid OR below the §11.0 minimum-budget threshold (60 seconds). Default is 120 (scraper total budget per the fix(scraper) series). Set SCROLL_TIMEOUT=120 (or any integer ≥ 60)."
    exit 2
fi
log_info "SCROLL_TIMEOUT=${SCROLL_TIMEOUT}s ≥ 60s (scraper total budget OK)"

if ! curl -s --max-time "${CURL_TIMEOUT}" "${BASE_URL}/ready" | jq -e '.status == "ready"' >/dev/null 2>&1; then
    log_fail "Server not ready at ${BASE_URL}/ready"
    exit 2
fi
log_info "Server: ready (${BASE_URL})"

if ! curl -s --max-time 3 "${SCRAPER_URL}/health" | jq -e '.ok == true' >/dev/null 2>&1; then
    log_fail "Scraper not healthy at ${SCRAPER_URL}/health"
    exit 2
fi
log_info "Scraper: healthy (${SCRAPER_URL})"

# Preflight guardrail — bare scraper /search direct connect-timeout ≤ 5s.
# The bare node-scraper /search endpoint is POST-only (per node-scraper/artlist_server.js:140,
# returning 405 on GET). We send a minimal POST body; ANY http response from the server within
# the connect-timeout window proves TCP+TLS are healthy (functional search results are
# asserted later by § 1). Per godlike/07 minimum-blast-radius + the §11.0 contract
# (SCRAPER_CONNECT_TIMEOUT_SECONDS=5):
if curl -sS --connect-timeout 5 --max-time "${SCROLL_TIMEOUT:-120}" -X POST "${SCRAPER_URL}/search" \
        -H 'Content-Type: application/json' \
        -d '{"term":"__preflight_connect_probe__","limit":1}' \
        -o /dev/null 2>/dev/null; then
    log_info "Bare scraper /search connect-timeout ≤ 5s OK (${SCRAPER_URL})"
else
    rc=$?
    log_fail "Bare scraper /search did NOT respond within 5s connect-timeout at ${SCRAPER_URL} (curl rc=${rc}). Refusing to run the live cycle — node-scraper is unreachable or hung (preflight-aborted)."
    exit 2
fi

if ! curl -s --max-time 3 "${QDRANT_URL}/collections" >/dev/null 2>&1; then
    log_fail "Qdrant not reachable at ${QDRANT_URL}"
    exit 2
fi
log_info "Qdrant: reachable (${QDRANT_URL})"

if [[ ! -f "${DB_PATH}" ]]; then
    log_fail "SQLite media.db.sqlite not found at ${DB_PATH}"
    exit 2
fi
log_info "SQLite: ${DB_PATH} present"

if [[ -z "${ROOT_FOLDER_ID}" ]]; then
    log_warn "ROOT_FOLDER_ID is empty — PipelineGen will fall back to the configured default Drive destination"
fi
log_info "Search term: '${SEARCH_TERM}' (limit=${LIMIT})"

# ============================================================
# § 1: Scraper /search probe (read-only — no Artlist download)
# ============================================================
# Independent confirmation that the scraper returns > 0 candidates for
# the live term BEFORE we enqueue the real job (where this proxy is
# exercised in-flight). The node-scraper contract (artlist_search.js)
# returns { ok:true, term, search_url, clips:[...] } on success.
log_info "=== § 1: Live-search probe (/api/artlist/search/live, term='${SEARCH_TERM}', limit=${LIMIT}) ==="
SCRAPER_PROBE=$(curl -sS --connect-timeout "${SCRAPER_CONNECT_TIMEOUT_SECONDS:-5}" --max-time "${SCROLL_TIMEOUT:-120}" -G \
    -H "$(auth_header)" \
    --data-urlencode "term=${SEARCH_TERM}" \
    --data-urlencode "limit=${LIMIT}" \
    "${BASE_URL}/api/artlist/search/live")
SCRAPER_OK=$(echo "${SCRAPER_PROBE}" | jq -r '.live_enforced // false')
SCRAPER_CLIPS=$(echo "${SCRAPER_PROBE}" | jq -r '.clips        | length // 0' 2>/dev/null || echo "0")

if [[ "${SCRAPER_OK}" != "true" ]]; then
    SCRAPER_ERR=$(echo "${SCRAPER_PROBE}" | jq -r '.error // "<no .error field>"')
    log_fail "live-search probe (/api/artlist/search/live) returned live_enforced=false: ${SCRAPER_ERR}"
elif [[ "${SCRAPER_CLIPS}" -ge 1 ]]; then
    log_pass "live-search probe (/api/artlist/search/live) returned ${SCRAPER_CLIPS} candidate(s) — job enqueue may proceed"
else
    log_warn "live-search probe (/api/artlist/search/live) returned 0 candidates — job enqueue may still succeed via fallback"
fi

# ============================================================
# Hermetic preflight gates (no real downloads)
# ============================================================
if [[ "${SKIP_HERMETICS:-0}" != "1" ]]; then
    log_info "=== HERMETIC GATES (no Artlist downloads) ==="

    # Precondition: gate test matrix integrity
    GATE_LIST_OUTPUT=$(go test -list '^TestGate[0-9]+_' \
        ./internal/application/assets/providers/artlist/... 2>&1 || true)

    if ! [[ "${EXPECTED_GATE_MATCHES}" =~ ^[1-9][0-9]*$ ]]; then
        echo "[ERROR] $(date '+%H:%M:%S') ENV var EXPECTED_GATE_MATCHES='${EXPECTED_GATE_MATCHES}' is invalid — must be a positive integer (no leading zero, no decimals, no sign, no whitespace)." >&2
        echo "[ERROR] Example valid override: EXPECTED_GATE_MATCHES=12 bash $(basename "$0")" >&2
        echo "[ERROR] Default 28 matches gate11_scraper_failure_test.go meta-anchor's expectedGateTests array length." >&2
        exit 2
    fi

    ACTUAL_GATE_MATCHES=$(printf '%s\n' "${GATE_LIST_OUTPUT}" \
        | grep -c '^TestGate' || true)
    ACTUAL_GATE_MATCHES=${ACTUAL_GATE_MATCHES:-0}

    if [[ "${ACTUAL_GATE_MATCHES}" -ne "${EXPECTED_GATE_MATCHES}" ]]; then
        echo "[ERROR] $(date '+%H:%M:%S') Hermetic gate precondition FAILED — aborting before test run" >&2
        echo "[ERROR]   expected: ${EXPECTED_GATE_MATCHES} gate test match(es)" >&2
        echo "[ERROR]   command:   go test -list '^TestGate[0-9]+_' ./internal/application/assets/providers/artlist/..." >&2
        echo "[ERROR]   actual:    ${ACTUAL_GATE_MATCHES}" >&2
        if [[ "${ACTUAL_GATE_MATCHES}" -gt 0 ]]; then
            echo "[ERROR]   actual matches:" >&2
            printf '%s\n' "${GATE_LIST_OUTPUT}" \
                | grep '^TestGate' | sed 's/^/[ERROR]     /' >&2
        else
            echo "[ERROR]   actual matches: (none — either no tests match the regex," >&2
            echo "[ERROR]                    or 'go test -list' failed with a build error;" >&2
            echo "[ERROR]                    full output above from the command.)" >&2
        fi
        echo "[ERROR] Diagnosis: a gate0X_test.go file was added/removed, a test" >&2
        echo "[ERROR] function was renamed (lost the ^TestGate[0-9]+_ prefix)," >&2
        echo "[ERROR] or a phantom test exists (function with TestGateNN_" >&2
        echo "[ERROR] naming but no backing gateNN_*.go file)." >&2
        echo "[ERROR] Action: update EXPECTED_GATE_MATCHES (this script) AND" >&2
        echo "[ERROR] expectedGateTests (gate11_scraper_failure_test.go) so the" >&2
        echo "[ERROR] spec and runtime stay in sync." >&2
        echo "[ERROR] (Or pass SKIP_HERMETICS=1 if this divergence is intentional.)" >&2
        exit 2
    fi
    log_info "Hermetic gate precondition OK: expected=${EXPECTED_GATE_MATCHES} actual=${ACTUAL_GATE_MATCHES}"

    log_info "Running: go test -count=1 -run '^TestGate' ./internal/application/assets/providers/artlist/..."
    if go test -count=1 -run '^TestGate' \
        ./internal/application/assets/providers/artlist/... 2>&1 | tail -30; then
        log_pass "Hermetic gate suite executed"
    fi
else
    log_info "Skipping hermetic gates (SKIP_HERMETICS=1)"
fi

# ============================================================
# QHEADERS (used by assert.sh for Qdrant scroll calls)
# ============================================================
declare -a QHEADERS=()
[[ -n "${QDRANT_API_KEY}" ]] && QHEADERS+=(-H "api-key: ${QDRANT_API_KEY}")
