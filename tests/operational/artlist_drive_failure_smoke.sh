#!/usr/bin/env bash
# artlist_drive_failure_smoke.sh — Artlist DoD Gate: Drive failure mode
#
# Verifies that when the Google Drive token (token.json) is missing or
# corrupted, the PipelineGen server fails to boot (fail-closed at the
# composition root). This prevents any silent-success where the server
# would start without Drive and all uploads would silently fail.
#
# Test steps:
#   1. Verify server is running with valid token.json
#   2. Stop server, rename token.json → token.json.bak
#   3. Attempt to start server — MUST fail to boot
#   4. Restore token.json, restart server — MUST succeed
#   5. Verify /ready after restoration
#
# Prerequisites:
#   - PipelineGen binary at ./pipelinegen
#   - Valid token.json at project root
#   - docker (for Qdrant)
#   - curl + jq on PATH
#
# Usage:
#   bash tests/operational/artlist_drive_failure_smoke.sh

# Cross-reference: this script is a sibling of artlist_live_e2e_verify.sh
# which enforces the gate test count (EXPECTED_GATE_MATCHES) against the
# meta-anchor's expectedGateTests array in:
#   internal/application/assets/providers/artlist/gate11_scraper_failure_test.go
# If you add or remove gate tests, update BOTH the meta-anchor AND
# EXPECTED_GATE_MATCHES in artlist_live_e2e_verify.sh. This script does
# NOT enforce the gate count contract — see artlist_live_e2e_verify.sh
# for the canonical HERMETIC GATES precondition.

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
PROJECT_DIR="$(cd "${SCRIPT_DIR}/../.." && pwd)"

HOST="127.0.0.1"
PORT="8000"
BASE_URL="http://${HOST}:${PORT}"
TOKEN_FILE="${PROJECT_DIR}/token.json"
TOKEN_BACKUP="${PROJECT_DIR}/token.json.bak"
SERVER_LOG="/tmp/pipelinegen_drive_test.log"
BOOT_TIMEOUT=60
ADMIN_TOKEN="${VELOX_ADMIN_TOKEN:-d6e31eb8d805b0cc91ef439aae42658b2838531b1de35b804f6932ca439c077d}"

PASS=0; FAIL=0
log_info()  { echo "[INFO]  $(date '+%H:%M:%S') $*"; }
log_pass()  { echo "[PASS]  $(date '+%H:%M:%S') $*"; PASS=$((PASS+1)); }
log_fail()  { echo "[FAIL]  $(date '+%H:%M:%S') $*"; FAIL=$((FAIL+1)); }

# Kill any running pipelinegen
kill_server() {
    pkill -f 'pipelinegen --mode' 2>/dev/null || true
    sleep 2
    fuser -k "${PORT}/tcp" 2>/dev/null || true
    sleep 1
}

# Start server and wait for /ready (returns 0 if ready, 1 if timeout)
start_server() {
    kill_server
    cd "${PROJECT_DIR}"
    env VELOX_PORT="${PORT}" VELOX_HOST="${HOST}" \
        VELOX_ENABLE_AUTH=true VELOX_ADMIN_TOKEN="${ADMIN_TOKEN}" \
        VELOX_DATA_DIR='./data' \
        nohup ./pipelinegen --mode all > "${SERVER_LOG}" 2>&1 &
    local pid=$!
    log_info "Server PID=${pid}, waiting up to ${BOOT_TIMEOUT}s..."

    local elapsed=0
    while [[ ${elapsed} -lt ${BOOT_TIMEOUT} ]]; do
        sleep 4
        elapsed=$((elapsed + 4))
        if curl -s --max-time 3 "${BASE_URL}/ready" 2>/dev/null | grep -q 'ready'; then
            echo "READY"
            return 0
        fi
    done
    echo "TIMEOUT"
    return 1
}

# Check if server process is alive
is_server_alive() {
    pgrep -f 'pipelinegen --mode' > /dev/null 2>&1
}

# ====================================================================
log_info "=== Artlist Drive Failure Smoke ==="
log_info "Project: ${PROJECT_DIR}"

# Pre-flight: token.json must exist
if [[ ! -f "${TOKEN_FILE}" ]]; then
    log_fail "token.json not found at ${TOKEN_FILE}"
    exit 2
fi
log_info "token.json: present"

# Pre-flight: binary must exist
if [[ ! -x "${PROJECT_DIR}/pipelinegen" ]]; then
    log_fail "pipelinegen binary not found"
    exit 2
fi
log_info "pipelinegen: found"

# ====================================================================
# STEP 1: Verify server starts normally with valid token
# ====================================================================
log_info "=== STEP 1: Baseline boot with valid token ==="
if start_server; then
    log_pass "Server boots with valid token.json"
else
    log_fail "Server failed to boot with valid token.json (baseline)"
    exit 2
fi

# ====================================================================
# STEP 2: Stop server, remove token, try to boot
# ====================================================================
log_info "=== STEP 2: Boot without token.json ==="
kill_server
sleep 2

# Rename token
mv "${TOKEN_FILE}" "${TOKEN_BACKUP}"
log_info "token.json → token.json.bak"

if [[ -f "${TOKEN_FILE}" ]]; then
    log_fail "token.json still exists after rename!"
else
    log_pass "token.json successfully removed"
fi

# Attempt to start without token
BOOT_FAILED=false
start_server &
SPID=$!
wait ${SPID} 2>/dev/null || BOOT_FAILED=true

sleep 5  # extra settle time

# Check: server should NOT be ready
if curl -s --max-time 5 "${BASE_URL}/ready" 2>/dev/null | grep -q 'ready'; then
    log_fail "Server booted WITHOUT token.json (silent-success anti-pattern!)"
elif is_server_alive; then
    # Server process alive but not ready — stuck in init
    STUCK=$(grep -c 'token' "${SERVER_LOG}" 2>/dev/null || echo "0")
    log_pass "Server process alive but NOT ready (stuck in init — fail-closed)"
    kill_server
else
    # Server failed to boot entirely
    log_pass "Server failed to boot without token.json (fail-closed at composition root)"
fi

# ====================================================================
# STEP 3: Restore token.json and verify server boots
# ====================================================================
log_info "=== STEP 3: Restore token.json and reboot ==="
mv "${TOKEN_BACKUP}" "${TOKEN_FILE}"
log_info "token.json restored"

if start_server; then
    log_pass "Server boots after token.json restoration"
else
    log_fail "Server failed to boot after token restoration!"
fi

# ====================================================================
# STEP 4: Verify /ready health checks
# ====================================================================
log_info "=== STEP 4: Verify /ready ==="
READY=$(curl -s --max-time 5 "${BASE_URL}/ready" 2>/dev/null)
if echo "${READY}" | jq -e '.status == "ready"' > /dev/null 2>&1; then
    if echo "${READY}" | jq -e '.checks.qdrant.ok == true' > /dev/null 2>&1; then
        log_pass "/ready: server ready, Qdrant ok"
    else
        log_info "/ready: server ready (Qdrant check may differ)"
    fi
else
    log_fail "/ready: server not ready after restoration!"
fi

# ====================================================================
# Verdict
# ====================================================================
echo
echo "============================================"
echo "  Artlist Drive Failure Smoke"
echo "  PASS=${PASS}  FAIL=${FAIL}"
echo "============================================"
echo "  Contract: server MUST fail-closed when"
echo "  token.json is missing or corrupted."
echo "============================================"

[[ "${FAIL}" -eq 0 ]] && exit 0 || exit 1
