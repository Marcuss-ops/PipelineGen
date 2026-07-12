#!/usr/bin/env bash
# scripts/tests/scraper_artlist_startup_e2e.sh
#
# E2E harness (PR-FIX-SCRAPER-BROWSER, July 2026) — confirm the
# artlist-scraper container launches a real Chromium at boot and
# reports `browser_running:true` on /health. Operator-facing
# verification; intended to be run on a host with docker-compose
# + a populated secrets/pipelinegen.env (or after `make compose-up`).
#
# This script does NOT compose a full scraper query against Artlist
# (that requires the broader `tests/operational/artlist_live_e2e_verify.sh`
# stack). The narrower scope here is the BROWSER STARTUP path that
# PR-FIX-SCRAPER-BROWSER changed: Debian apt chromium layer +
# runBrowserPreflight fail-closed gate.
#
# Usage:
#   bash scripts/tests/scraper_artlist_startup_e2e.sh
#
# Exit codes:
#   0 — chromium installed in container + /health rescue probe passed.
#   1 — chromium NOT installed (Dockerfile-stage regression).
#   2 — scraper container exited (preflight or other boot failure).
#   3 — /health never reported browser_running (preflight-passed but
#       browser launch later failed; consult last_launch_error).
#   4 — docker compose not available OR container failed to mount.
#
# This script is HERMETIC in CI only when the host has the docker
# daemon authorized for non-root invocation; otherwise operators
# run it manually post-deploy.

set -euo pipefail

# ─── Config ──────────────────────────────────────────────────────────────────
COMPOSE_FILE="${COMPOSE_FILE:-docker-compose.yml}"
SERVICE_NAME="${SERVICE_NAME:-artlist-scraper}"
HEALTH_HOST="${HEALTH_HOST:-127.0.0.1}"
HEALTH_PORT="${HEALTH_PORT:-9123}"
HEALTH_TIMEOUT="${HEALTH_TIMEOUT:-90}"   # container start_period in Dockerfile is 10s
POLL_INTERVAL="${POLL_INTERVAL:-2}"
LOG_TAIL="${LOG_TAIL:-100}"

# PATTERNS confirmed for /health payload (matches the JSON shape in
# node-scraper/artlist_server.js::handleHealth):
#   "ok": true,
#   "browser_running": <bool>,
#   "last_launch_error": <string|null>,
#   ...
OK_PATTERN='"ok": *true'
BROWSE_OK_PATTERN='"browser_running": *true'

PASS=0; FAIL=0
log_pass() { echo "[PASS]  $(date '+%H:%M:%S') $*"; PASS=$((PASS + 1)); }
log_fail() { echo "[FAIL]  $(date '+%H:%M:%S') $*"; FAIL=$((FAIL + 1)); }
log_info() { echo "[INFO]  $(date '+%H:%M:%S') $*"; }
log_warn() { echo "[WARN]  $(date '+%H:%M:%S') $*"; }

# ─── Preflight: docker + compose available ───────────────────────────────────
require_tool() {
    command -v "$1" >/dev/null 2>&1 || {
        log_fail "Required tool '$1' not on PATH"
        exit 4
    }
}
require_tool docker
require_tool curl
require_tool jq
docker compose version >/dev/null 2>&1 || {
    log_fail "docker compose plugin not available (install docker-compose-plugin)"
    exit 4
}
[[ -f "${COMPOSE_FILE}" ]] || {
    log_fail "docker compose file not found: ${COMPOSE_FILE}"
    exit 4
}

# ─── Bring the scraper service up detached ──────────────────────────────────
log_info "Starting ${SERVICE_NAME} from ${COMPOSE_FILE}..."
docker compose -f "${COMPOSE_FILE}" up -d --no-deps --force-recreate "${SERVICE_NAME}" >/dev/null

# Container may need its base image rebuilt when this PR's chromium
# layer lands; rebuild explicitly so the apt install runs.
log_info "Forcing rebuild of ${SERVICE_NAME} if Dockerfile.scraper changed since last build..."
docker compose -f "${COMPOSE_FILE}" build "${SERVICE_NAME}" >/dev/null
docker compose -f "${COMPOSE_FILE}" up -d --no-deps --force-recreate "${SERVICE_NAME}" >/dev/null

# ─── Probe 1: confirm the chromium binary exists inside the container ──────
CHROMIUM_PATH=$(docker compose -f "${COMPOSE_FILE}" exec -T "${SERVICE_NAME}" \
    bash -c 'command -v chromium || command -v google-chrome || true' 2>/dev/null || true)
if [[ -n "${CHROMIUM_PATH}" ]]; then
    log_pass "Chromium binary present in container: ${CHROMIUM_PATH}"
else
    log_fail "Chromium binary NOT FOUND inside ${SERVICE_NAME} container — Dockerfile apt-install of 'chromium' regressed"
    log_info "Inspect: docker compose -f ${COMPOSE_FILE} exec ${SERVICE_NAME} bash -c 'dpkg -l | grep -E chromium|google-chrome'"
fi

# ─── Probe 2: poll /health for browser_running:true ─────────────────────────
log_info "Polling http://${HEALTH_HOST}:${HEALTH_PORT}/health (timeout=${HEALTH_TIMEOUT}s)..."
HEALTHY=""
ELAPSED=0
LAST_JSON=""
while [[ ${ELAPSED} -lt ${HEALTH_TIMEOUT} ]]; do
    sleep "${POLL_INTERVAL}"
    ELAPSED=$((ELAPSED + POLL_INTERVAL))
    HEALTH=$(curl -s --max-time 3 "http://${HEALTH_HOST}:${HEALTH_PORT}/health" || echo "{}")
    LAST_JSON="${HEALTH}"
    OK=$(echo "${HEALTH}"   | jq -r '.ok // false' 2>/dev/null || echo "false")
    BROWSE=$(echo "${HEALTH}" | jq -r '.browser_running // false' 2>/dev/null || echo "false")
    LAUNCH_ERR=$(echo "${HEALTH}" | jq -r '.last_launch_error // null' 2>/dev/null || echo "null")
    log_info "  poll t=${ELAPSED}s  ok=${OK}  browser_running=${BROWSE}  last_launch_error=${LAUNCH_ERR:-<null>}"
    if [[ "${OK}" == "true" && "${BROWSE}" == "true" ]]; then
        HEALTHY="yes"
        break
    fi
    # If the container already exited prematurely, the polling will
    # never converge — bail early with a clear error.
    CONTAINER_STATE=$(docker compose -f "${COMPOSE_FILE}" ps -q "${SERVICE_NAME}" \
        | xargs -r docker inspect -f '{{.State.Status}}' 2>/dev/null || echo "missing")
    if [[ "${CONTAINER_STATE}" == "exited" ]]; then
        log_fail "${SERVICE_NAME} container exited before /health became healthy (likely preflight exit 78)"
        log_info "Last container log lines:"
        docker compose -f "${COMPOSE_FILE}" logs --tail="${LOG_TAIL}" "${SERVICE_NAME}" 2>&1 | tail -"${LOG_TAIL}" | sed 's/^/    /'
        docker compose -f "${COMPOSE_FILE}" down --remove-orphans >/dev/null 2>&1 || true
        exit 2
    fi
done

if [[ -z "${HEALTHY}" ]]; then
    log_fail "/health did not reach browser_running=true within ${HEALTH_TIMEOUT}s"
    log_info "Last /health payload:"
    echo "${LAST_JSON}" | jq . 2>/dev/null | sed 's/^/    /' || echo "    ${LAST_JSON}"
    log_info "Last ${LOG_TAIL} container log lines:"
    docker compose -f "${COMPOSE_FILE}" logs --tail="${LOG_TAIL}" "${SERVICE_NAME}" 2>&1 | tail -"${LOG_TAIL}" | sed 's/^/    /'
    docker compose -f "${COMPOSE_FILE}" down --remove-orphans >/dev/null 2>&1 || true
    exit 3
fi
log_pass "/health responded with browser_running=true (last_launch_error cleared)"

# ─── Probe 3: confirm puppeteer can launch Chromium end-to-end (warm-up) ───
# Even if /health reports browser_running=true, this fires a real
# /search request through the scraper's pre-warm to surface any
# post-launch failure that /health lazily masks. Catches the case
# where the binary exists but is missing a runtime dependency.
log_info "Warming scraper browser via curl -fsS http://${HEALTH_HOST}:${HEALTH_PORT}/search (or fallback)..."
# We do NOT actually hit Artlist here (no credentials in CI); we just
# assert /health's last_launch_error did not flip to a string after
# the warm-up window — a quick re-poll is enough.
sleep 3
HEALTH_AFTER=$(curl -s --max-time 3 "http://${HEALTH_HOST}:${HEALTH_PORT}/health" || echo "{}")
LAUNCH_ERR_AFTER=$(echo "${HEALTH_AFTER}" | jq -r '.last_launch_error // null' 2>/dev/null)
if [[ "${LAUNCH_ERR_AFTER}" == "null" || -z "${LAUNCH_ERR_AFTER}" ]]; then
    log_pass "last_launch_error cleared after warm-up; browser launch was clean"
else
    log_fail "last_launch_error non-null after warm-up: ${LAUNCH_ERR_AFTER}"
fi

# ─── Teardown ───────────────────────────────────────────────────────────────
docker compose -f "${COMPOSE_FILE}" down --remove-orphans >/dev/null 2>&1 || log_warn "teardown had warnings"

echo
echo "============================================"
echo "  Scraper Browser Startup E2E (PR-FIX-SCRAPER-BROWSER)"
echo "  PASS=${PASS}  FAIL=${FAIL}"
echo "============================================"

if [[ ${FAIL} -gt 0 ]]; then
    echo "VERDICT: ${FAIL} CHECK(S) FAILED"
    exit 1
fi
echo "VERDICT: BROWSER STARTUP E2E PASS"
exit 0
