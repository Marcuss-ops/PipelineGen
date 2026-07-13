#!/usr/bin/env bash
# artlist_scraper_failure_smoke.sh — Artlist DoD Gate 10: Scraper failure mode
#
# Verifies that when the Node scraper (:9123) is down, artlist/run
# either returns HTTP 503 immediately or the job fails with a clear
# scraper-unavailable error (NOT silent-success with found=0).
#
# Prerequisites:
#   - PipelineGen server running on ${VELOX_HOST:-127.0.0.1}:${VELOX_PORT:-8000}
#   - Node scraper running on :9123 (will be killed during test)
#   - VELOX_ADMIN_TOKEN env var set
#   - curl + jq on PATH
#
# Usage:
#   VELOX_ADMIN_TOKEN='...' bash tests/operational/artlist_scraper_failure_smoke.sh

# Cross-reference: this script is a sibling of artlist_live_e2e_verify.sh
# which enforces the gate test count (EXPECTED_GATE_MATCHES) against the
# meta-anchor's expectedGateTests array in:
#   internal/application/assets/providers/artlist/gate11_scraper_failure_test.go
# If you add or remove gate tests, update BOTH the meta-anchor AND
# EXPECTED_GATE_MATCHES in artlist_live_e2e_verify.sh. This script does
# NOT enforce the gate count contract — see artlist_live_e2e_verify.sh
# for the canonical HERMETIC GATES precondition.

set -euo pipefail

HOST="${VELOX_HOST:-127.0.0.1}"
PORT="${VELOX_PORT:-8000}"
BASE_URL="http://${HOST}:${PORT}"
TOKEN="${VELOX_ADMIN_TOKEN:-d6e31eb8d805b0cc91ef439aae42658b2838531b1de35b804f6932ca439c077d}"
SCRAPER_URL="http://127.0.0.1:9123"
TEST_TERM="${ARTLIST_SCR_FAIL_TERM:-shadow}"
# Per fix(scraper) PR + docs/operations/stock-e2e-runbook.md §11.0:
SCROLL_TIMEOUT="${SCROLL_TIMEOUT:-120}"
SCRAPER_CONNECT_TIMEOUT_SECONDS="${SCRAPER_CONNECT_TIMEOUT_SECONDS:-5}"

PASS=0; FAIL=0
log_info()  { echo "[INFO]  $(date '+%H:%M:%S') $*"; }
log_pass()  { echo "[PASS]  $(date '+%H:%M:%S') $*"; PASS=$((PASS+1)); }
log_fail()  { echo "[FAIL]  $(date '+%H:%M:%S') $*"; FAIL=$((FAIL+1)); }

# === Pre-flight ===
log_info "=== Artlist Scraper Failure Smoke ==="

if ! curl -s --max-time 5 "${BASE_URL}/ready" | jq -e '.status == "ready"' > /dev/null 2>&1; then
    log_fail "Server not ready"; exit 2
fi
log_info "Server: ready"

if ! curl -s --max-time 3 "${SCRAPER_URL}/health" | jq -e '.ok == true' > /dev/null 2>&1; then
    log_fail "Scraper not healthy at start"; exit 2
fi
log_info "Scraper: healthy"

# === Step 1: Kill scraper ===
log_info "Killing scraper..."
SCRAPER_PID=$(ps aux | grep 'node.*artlist_server' | grep -v grep | awk '{print $2}' | head -1)
if [[ -z "${SCRAPER_PID}" ]]; then
    log_fail "Could not find scraper PID"
else
    kill "${SCRAPER_PID}" 2>&1
    sleep 2
fi

if curl -s --max-time 3 "${SCRAPER_URL}/health" > /dev/null 2>&1; then
    log_fail "Scraper still reachable after kill"
else
    log_pass "Scraper killed (unreachable on :9123)"
fi

# === Step 2: Submit artlist/run ===
log_info "Submitting artlist/run with scraper down..."
HTTP_CODE=$(curl -sS --connect-timeout "${SCRAPER_CONNECT_TIMEOUT_SECONDS:-5}" -o /tmp/artlist_scr_fail.json -w '%{http_code}' --max-time "${SCROLL_TIMEOUT:-120}" \
    -X POST "${BASE_URL}/api/artlist/run" \
    -H "Authorization: Bearer ${TOKEN}" \
    -H 'Content-Type: application/json' \
    -d "{\"term\":\"${TEST_TERM}\",\"limit\":1,\"dry_run\":true}")

log_info "HTTP status: ${HTTP_CODE}"

# === Step 3: Verify failure mode ===
if [[ "${HTTP_CODE}" == "503" ]]; then
    log_pass "HTTP 503 returned (scraper unavailable - fail-closed)"
elif [[ "${HTTP_CODE}" == "202" ]]; then
    # Job accepted - poll it to see if it fails with scraper error
    JID=$(jq -r '.run_id // ""' /tmp/artlist_scr_fail.json 2>/dev/null)
    if [[ -n "${JID}" && "${JID}" != "null" ]]; then
        log_info "Job queued: ${JID} - polling for outcome..."
        for i in $(seq 1 12); do
            sleep 5
            STATUS=$(curl -s --max-time 10 "${BASE_URL}/api/jobs/${JID}/full" \
                -H "Authorization: Bearer ${TOKEN}" | jq -r '.status // ""')
            if [[ "${STATUS}" == "FAILED" ]]; then
                log_pass "Job FAILED (scraper down - correct failure mode)"
                break
            elif [[ "${STATUS}" == "SUCCEEDED" ]]; then
                FOUND=$(curl -s --max-time 10 "${BASE_URL}/api/jobs/${JID}/full" \
                    -H "Authorization: Bearer ${TOKEN}" | jq -r '.result.found // 0')
                if [[ "${FOUND}" == "0" ]]; then
                    log_fail "Job SUCCEEDED with found=0 (silent-success anti-pattern!)"
                else
                    log_info "Job SUCCEEDED (scraper may have recovered)"
                fi
                break
            fi
            if [[ ${i} -eq 12 ]]; then
                log_fail "Job still not terminal after 60s: ${STATUS}"
            fi
        done
    else
        log_fail "No job_id in 202 response"
    fi
else
    log_fail "Unexpected HTTP status: ${HTTP_CODE}"
fi

# === Step 4: Restart scraper ===
log_info "Restarting scraper..."
cd "$(dirname "$0")/../../node-scraper" 2>/dev/null || cd /home/pierone/src/go-master/projects/Pyt/VeloxEditing/refactored/node-scraper
setsid node artlist_server.js > /tmp/scraper.log 2>&1 & disown
sleep 4
if curl -s --max-time 5 "${SCRAPER_URL}/health" | jq -e '.ok == true' > /dev/null 2>&1; then
    log_pass "Scraper restarted"
else
    log_fail "Scraper failed to restart!"
fi

# === Verdict ===
echo
echo "============================================"
echo "  Artlist Scraper Failure Smoke"
echo "  PASS=${PASS}  FAIL=${FAIL}"
echo "============================================"
[[ "${FAIL}" -eq 0 ]] && exit 0 || exit 1
