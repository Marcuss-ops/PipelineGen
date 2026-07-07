#!/usr/bin/env bash
# artlist_multi_query_smoke.sh — Artlist DoD Gate 3+4: Multi-query + Idempotent test
#
# Gate 3 (Multi-query): Verifies >= 3/5 keywords return clips via dry_run.
# Gate 4 (Idempotent):  Verifies two consecutive real runs on the same keyword
#                        do NOT create duplicate media_assets.
#
# Prerequisites:
#   - PipelineGen server running on ${VELOX_HOST:-127.0.0.1}:${VELOX_PORT:-8000}
#   - Node scraper running on :9123 (/health returns ok=true)
#   - VELOX_ADMIN_TOKEN env var set (or default admin token)
#   - sqlite3 on PATH
#   - curl + jq on PATH
#
# Usage:
#   VELOX_ADMIN_TOKEN='d6e31eb8d805b0cc91ef439aae42658b2838531b1de35b804f6932ca439c077d' bash tests/operational/artlist_multi_query_smoke.sh

set -euo pipefail

# ----------------------------- config -----------------------------
HOST="${VELOX_HOST:-127.0.0.1}"
PORT="${VELOX_PORT:-8000}"
BASE_URL="http://${HOST}:${PORT}"
TOKEN="${VELOX_ADMIN_TOKEN:-d6e31eb8d805b0cc91ef439aae42658b2838531b1de35b804f6932ca439c077d}"
SCRAPER_URL="http://127.0.0.1:9123"
DB_PATH="${VELOX_DATA_DIR:-./data}/media/media.db.sqlite"
DRY_RUN_LIMIT="${ARTLIST_MQ_LIMIT:-1}"
REAL_RUN_LIMIT="${ARTLIST_IDEMPOTENT_LIMIT:-1}"
IDEMPOTENT_TERM="${ARTLIST_IDEMPOTENT_TERM:-boxing}"
POLL_INTERVAL="${ARTLIST_POLL_INTERVAL:-10}"
POLL_MAX="${ARTLIST_POLL_MAX:-18}"
MULTI_KEYWORDS=("boxing" "training" "fight" "gloves" "punch")

PASS=0
FAIL=0

# ----------------------------- helpers -----------------------------
auth_header() { echo "Authorization: Bearer ${TOKEN}"; }

log_info()  { echo "[INFO]  $(date '+%H:%M:%S') $*"; }
log_pass()  { echo "[PASS]  $(date '+%H:%M:%S') $*"; PASS=$((PASS + 1)); }
log_fail()  { echo "[FAIL]  $(date '+%H:%M:%S') $*"; FAIL=$((FAIL + 1)); }

# Poll a job until terminal status or max polls reached.
# Usage: poll_job <job_id>
poll_job() {
    local jid="$1"
    for i in $(seq 1 ${POLL_MAX}); do
        sleep "${POLL_INTERVAL}"
        local resp
        resp=$(curl -s --max-time 10 "${BASE_URL}/api/jobs/${jid}/full" -H "$(auth_header)" 2>/dev/null) || true
        local status
        status=$(echo "${resp}" | jq -r '.status // "?"' 2>/dev/null) || true
        if [[ "${status}" == "SUCCEEDED" || "${status}" == "FAILED" ]]; then
            echo "${status}"
            return 0
        fi
        if [[ "${status}" == "?" || -z "${status}" ]]; then
            echo "UNREACHABLE"
            return 1
        fi
    done
    echo "TIMEOUT"
    return 1
}

# ----------------------------- pre-flight -----------------------------
log_info "=== Artlist Multi-Query + Idempotent Smoke ==="

# Check server
if ! curl -s --max-time 5 "${BASE_URL}/ready" | jq -e '.status == "ready"' > /dev/null 2>&1; then
    log_fail "Server not ready at ${BASE_URL}/ready"
    exit 2
fi
log_info "Server: ready"

# Check scraper
if ! curl -s --max-time 3 "${SCRAPER_URL}/health" | jq -e '.ok == true' > /dev/null 2>&1; then
    log_fail "Scraper not healthy at ${SCRAPER_URL}/health"
    exit 2
fi
log_info "Scraper: healthy"

# Check sqlite3
if ! command -v sqlite3 &>/dev/null; then
    log_fail "sqlite3 not found on PATH"
    exit 2
fi
log_info "sqlite3: available"

# ----------------------------- Gate 3: Multi-query -----------------------------
log_info "=== GATE 3: Multi-query dry_run ==="

declare -A multi_results
for term in "${MULTI_KEYWORDS[@]}"; do
    log_info "Submitting dry_run: term=${term}"
    resp=$(curl -s --max-time 60 -X POST "${BASE_URL}/api/artlist/run" \
        -H "$(auth_header)" \
        -H 'Content-Type: application/json' \
        -d "{\"term\":\"${term}\",\"limit\":${DRY_RUN_LIMIT},\"dry_run\":true}") || true

    jid=$(echo "${resp}" | jq -r '.run_id // ""')
    if [[ -z "${jid}" || "${jid}" == "null" ]]; then
        log_fail "term=${term}: no run_id in response"
        multi_results["${term}"]=0
        continue
    fi

    status=$(poll_job "${jid}")
    if [[ "${status}" != "SUCCEEDED" ]]; then
        log_fail "term=${term}: job status=${status}"
        multi_results["${term}"]=0
        continue
    fi

    # Get found count
    fresp=$(curl -s --max-time 10 "${BASE_URL}/api/jobs/${jid}/full" -H "$(auth_header)")
    found=$(echo "${fresp}" | jq -r '.result.found // 0')
    log_info "term=${term}: found=${found}"
    multi_results["${term}"]="${found}"
done

# Tally: >= 3/5 must have found >= 1
multi_pass=0
for term in "${MULTI_KEYWORDS[@]}"; do
    found="${multi_results[${term}]:-0}"
    if [[ "${found}" -ge 1 ]]; then
        multi_pass=$((multi_pass + 1))
    fi
done

if [[ "${multi_pass}" -ge 3 ]]; then
    log_pass "Gate 3: ${multi_pass}/5 keywords found clips"
else
    log_fail "Gate 3: only ${multi_pass}/5 keywords found clips (need >=3)"
fi

# ----------------------------- Gate 4: Idempotent -----------------------------
log_info "=== GATE 4: Idempotent test ==="

# Snapshot before
before_count=$(sqlite3 "${DB_PATH}" "SELECT COUNT(*) FROM media_assets WHERE source='artlist';")
before_ids=$(sqlite3 "${DB_PATH}" "SELECT GROUP_CONCAT(id) FROM media_assets WHERE source='artlist' ORDER BY id;")
log_info "Before: count=${before_count} ids=${before_ids}"

# RUN 1
log_info "RUN 1: term=${IDEMPOTENT_TERM}"
resp1=$(curl -s --max-time 60 -X POST "${BASE_URL}/api/artlist/run" \
    -H "$(auth_header)" \
    -H 'Content-Type: application/json' \
    -d "{\"term\":\"${IDEMPOTENT_TERM}\",\"limit\":${REAL_RUN_LIMIT},\"dry_run\":false}") || true

jid1=$(echo "${resp1}" | jq -r '.run_id // ""')
if [[ -z "${jid1}" || "${jid1}" == "null" ]]; then
    log_fail "RUN 1: no run_id"
else
    status1=$(poll_job "${jid1}")
    fresp1=$(curl -s --max-time 10 "${BASE_URL}/api/jobs/${jid1}/full" -H "$(auth_header)")
    found1=$(echo "${fresp1}" | jq -r '.result.found // 0')
    proc1=$(echo "${fresp1}" | jq -r '.result.processed // 0')
    log_info "RUN 1: status=${status1} found=${found1} processed=${proc1}"
fi

sleep 3

# RUN 2
log_info "RUN 2: term=${IDEMPOTENT_TERM}"
resp2=$(curl -s --max-time 60 -X POST "${BASE_URL}/api/artlist/run" \
    -H "$(auth_header)" \
    -H 'Content-Type: application/json' \
    -d "{\"term\":\"${IDEMPOTENT_TERM}\",\"limit\":${REAL_RUN_LIMIT},\"dry_run\":false}") || true

jid2=$(echo "${resp2}" | jq -r '.run_id // ""')
if [[ -z "${jid2}" || "${jid2}" == "null" ]]; then
    log_fail "RUN 2: no run_id"
else
    status2=$(poll_job "${jid2}")
    fresp2=$(curl -s --max-time 10 "${BASE_URL}/api/jobs/${jid2}/full" -H "$(auth_header)")
    found2=$(echo "${fresp2}" | jq -r '.result.found // 0')
    proc2=$(echo "${fresp2}" | jq -r '.result.processed // 0')
    log_info "RUN 2: status=${status2} found=${found2} processed=${proc2}"
fi

# Snapshot after
after_count=$(sqlite3 "${DB_PATH}" "SELECT COUNT(*) FROM media_assets WHERE source='artlist';")
after_ids=$(sqlite3 "${DB_PATH}" "SELECT GROUP_CONCAT(id) FROM media_assets WHERE source='artlist' ORDER BY id;")
log_info "After: count=${after_count} ids=${after_ids}"

# Verify no duplicates
dupes=$(sqlite3 "${DB_PATH}" "SELECT COUNT(*) FROM (SELECT id, COUNT(*) as cnt FROM media_assets WHERE source='artlist' GROUP BY id HAVING cnt > 1);")
if [[ "${dupes}" -eq 0 ]]; then
    log_pass "Gate 4: No duplicate asset_ids after 2 idempotent runs"
else
    log_fail "Gate 4: ${dupes} duplicate asset_ids found!"
fi

# Verify count didn't explode (tolerance: +1 for a genuinely new clip)
count_diff=$((after_count - before_count))
if [[ "${count_diff}" -le 1 ]]; then
    log_pass "Gate 4: Count diff=${count_diff} (within tolerance: <= 1)"
else
    log_fail "Gate 4: Count diff=${count_diff} (expected <= 1)"
fi

# ----------------------------- verdict -----------------------------
echo
echo "============================================"
echo "  Artlist Multi-Query + Idempotent Smoke"
echo "  PASS=${PASS}  FAIL=${FAIL}"
echo "============================================"

if [[ "${FAIL}" -gt 0 ]]; then
    echo "VERDICT: SOME CHECKS FAILED"
    exit 1
else
    echo "VERDICT: ALL CHECKS PASSED"
    exit 0
fi
