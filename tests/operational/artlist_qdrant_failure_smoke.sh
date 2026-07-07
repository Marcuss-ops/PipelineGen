#!/usr/bin/env bash
# artlist_qdrant_failure_smoke.sh — Artlist DoD Gate 9: Qdrant failure mode
#
# Verifies that when Qdrant is down, the outbox does NOT silently mark
# asset.index.requested events as "completed". Instead they should stay
# "pending" or go to "dead_letter" after retry exhaustion.
#
# Prerequisites:
#   - PipelineGen server running on ${VELOX_HOST:-127.0.0.1}:${VELOX_PORT:-8000}
#   - Qdrant accessible via docker (pipelinegen-qdrant / pipelinegen-qdrant-test)
#   - VELOX_ADMIN_TOKEN env var set
#   - sqlite3 + curl + jq on PATH
#
# Usage:
#   VELOX_ADMIN_TOKEN='...' bash tests/operational/artlist_qdrant_failure_smoke.sh

set -euo pipefail

HOST="${VELOX_HOST:-127.0.0.1}"
PORT="${VELOX_PORT:-8000}"
BASE_URL="http://${HOST}:${PORT}"
TOKEN="${VELOX_ADMIN_TOKEN:-d6e31eb8d805b0cc91ef439aae42658b2838531b1de35b804f6932ca439c077d}"
DB_PATH="${VELOX_DATA_DIR:-./data}/media/media.db.sqlite"
QDRANT_CONTAINERS=("pipelinegen-qdrant" "pipelinegen-qdrant-test")
TEST_TERM="${ARTLIST_QD_FAIL_TERM:-knockout}"

PASS=0; FAIL=0
log_info()  { echo "[INFO]  $(date '+%H:%M:%S') $*"; }
log_pass()  { echo "[PASS]  $(date '+%H:%M:%S') $*"; PASS=$((PASS+1)); }
log_fail()  { echo "[FAIL]  $(date '+%H:%M:%S') $*"; FAIL=$((FAIL+1)); }

# === Pre-flight ===
log_info "=== Artlist Qdrant Failure Smoke ==="

if ! curl -s --max-time 5 "${BASE_URL}/ready" | jq -e '.status == "ready"' > /dev/null 2>&1; then
    log_fail "Server not ready"; exit 2
fi
log_info "Server: ready"

# Snapshot outbox before
BEFORE=$(sqlite3 "${DB_PATH}" "SELECT COUNT(*) FROM outbox_events WHERE event_type='asset.index.requested' AND status='dead_letter';")
log_info "Outbox dead_letter before: ${BEFORE}"

# === Step 1: Stop Qdrant ===
log_info "Stopping Qdrant containers..."
STOPPED=()
for c in "${QDRANT_CONTAINERS[@]}"; do
    if docker ps -q --filter "name=${c}" | grep -q .; then
        docker stop "${c}" 2>&1 && STOPPED+=("${c}")
    fi
done

sleep 2
if curl -s --max-time 3 http://127.0.0.1:6333/collections > /dev/null 2>&1; then
    log_fail "Qdrant still reachable after stop"
else
    log_pass "Qdrant stopped (unreachable on :6333)"
fi

# === Step 2: Submit dry_run ===
log_info "Submitting dry_run: term=${TEST_TERM}"
JID=$(curl -s --max-time 60 -X POST "${BASE_URL}/api/artlist/run" \
    -H "Authorization: Bearer ${TOKEN}" \
    -H 'Content-Type: application/json' \
    -d "{\"term\":\"${TEST_TERM}\",\"limit\":1,\"dry_run\":true}" | jq -r '.run_id // ""')

if [[ -z "${JID}" || "${JID}" == "null" ]]; then
    log_fail "No run_id in response"; 
else
    log_info "Job: ${JID}"
fi

# === Step 3: Wait for outbox events to appear ===
log_info "Waiting for outbox events (up to 60s)..."
for i in $(seq 1 12); do
    sleep 5
    DL=$(sqlite3 "${DB_PATH}" "SELECT COUNT(*) FROM outbox_events WHERE event_type='asset.index.requested' AND status='dead_letter';")
    if [[ "${DL}" -gt "${BEFORE}" ]]; then
        log_pass "New dead_letter events: +$((DL - BEFORE)) (Qdrant down = outbox NOT silently completed)"
        break
    fi
    if [[ ${i} -eq 12 ]]; then
        # Also check pending
        PEND=$(sqlite3 "${DB_PATH}" "SELECT COUNT(*) FROM outbox_events WHERE event_type='asset.index.requested' AND status='pending';")
        COMP=$(sqlite3 "${DB_PATH}" "SELECT COUNT(*) FROM outbox_events WHERE event_type='asset.index.requested' AND status='completed' AND created_at > datetime('now','-3 minutes');")
        if [[ "${PEND}" -gt 0 ]]; then
            log_pass "Pending events: ${PEND} (Qdrant down = correctly blocked from completing)"
        elif [[ "${COMP}" -gt 0 ]]; then
            log_fail "New completed events: ${COMP} (should NOT complete with Qdrant down!)"
        else
            log_fail "No outbox events observed (timeout)"
        fi
    fi
done

# === Step 4: Restart Qdrant ===
log_info "Restarting Qdrant..."
for c in "${STOPPED[@]}"; do
    docker start "${c}" 2>&1
done
sleep 3
if curl -s --max-time 5 http://127.0.0.1:6333/collections > /dev/null 2>&1; then
    log_pass "Qdrant restarted"
else
    log_fail "Qdrant failed to restart!"
fi

# === Verdict ===
echo
echo "============================================"
echo "  Artlist Qdrant Failure Smoke"
echo "  PASS=${PASS}  FAIL=${FAIL}"
echo "============================================"
[[ "${FAIL}" -eq 0 ]] && exit 0 || exit 1
