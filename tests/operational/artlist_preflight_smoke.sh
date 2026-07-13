#!/usr/bin/env bash
# artlist_preflight_smoke.sh — Artlist DoD Gate 11-12: Operator Preflight Checklist
#
# Read-only diagnostic probes that verify the Artlist integration is
# healthy end-to-end. All probes are SELECT-only or GET-only — zero
# side effects. Safe to run repeatedly.
#
# Based on the QDRANT-CHAIN-VERIFY-2026-07-04 operator checklist
# (architecture/action-plans/2026-07-04-qdrant-verification-chain.md §4).
#
# Prerequisites:
#   - PipelineGen server running on ${VELOX_HOST:-127.0.0.1}:${VELOX_PORT:-8000}
#   - Node scraper running on :9123
#   - Qdrant running on :6333
#   - VELOX_ADMIN_TOKEN env var set
#   - sqlite3 + curl + jq on PATH
#
# Usage:
#   VELOX_ADMIN_TOKEN='...' bash tests/operational/artlist_preflight_smoke.sh

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
DB_PATH="${VELOX_DATA_DIR:-./data}/media/media.db.sqlite"
QDRANT_URL="http://127.0.0.1:6333"
COLLECTION="media_assets_current"
# Per fix(scraper) PR + docs/operations/stock-e2e-runbook.md §11.0:
SCROLL_TIMEOUT="${SCROLL_TIMEOUT:-120}"
SCRAPER_CONNECT_TIMEOUT_SECONDS="${SCRAPER_CONNECT_TIMEOUT_SECONDS:-5}"

PASS=0; FAIL=0; WARN=0
log_info()  { echo "[INFO]  $(date '+%H:%M:%S') $*"; }
log_pass()  { echo "[PASS]  $(date '+%H:%M:%S') $*"; PASS=$((PASS+1)); }
log_fail()  { echo "[FAIL]  $(date '+%H:%M:%S') $*"; FAIL=$((FAIL+1)); }
log_warn()  { echo "[WARN]  $(date '+%H:%M:%S') $*"; WARN=$((WARN+1)); }

# ====================================================================
# CHECK 1: Server health
# ====================================================================
log_info "=== CHECK 1: Server /ready ==="
READY=$(curl -s --max-time 5 "${BASE_URL}/ready")
if echo "${READY}" | jq -e '.status == "ready"' > /dev/null 2>&1; then
    log_pass "Server ready"
else
    log_fail "Server not ready: $(echo ${READY} | head -1)"
fi

# ====================================================================
# CHECK 2: DB schema — artlist tables exist
# ====================================================================
log_info "=== CHECK 2: Artlist tables in SQLite ==="
TABLES=$(sqlite3 "${DB_PATH}" "SELECT name FROM sqlite_master WHERE type='table' AND name LIKE '%artlist%';" 2>/dev/null)
if echo "${TABLES}" | grep -q 'artlist'; then
    log_pass "Artlist tables: $(echo ${TABLES} | tr '\n' ' ')"
else
    log_warn "No artlist-specific tables found (check schema)"
fi

# ====================================================================
# CHECK 3: Artlist media_assets with drive_link
# ====================================================================
log_info "=== CHECK 3: Artlist media_assets with drive_link ==="
ARTLIST_COUNT=$(sqlite3 "${DB_PATH}" "SELECT COUNT(*) FROM media_assets WHERE source='artlist';")
ARTLIST_DRIVE=$(sqlite3 "${DB_PATH}" "SELECT COUNT(*) FROM media_assets WHERE source='artlist' AND drive_link IS NOT NULL AND drive_link != '';")
if [[ "${ARTLIST_COUNT}" -gt 0 ]]; then
    log_pass "Artlist media_assets: ${ARTLIST_COUNT} total, ${ARTLIST_DRIVE} with drive_link"
else
    log_warn "No artlist media_assets (run a real artlist job first)"
fi

# ====================================================================
# CHECK 4: Artlist assets lifecycle_state
# ====================================================================
log_info "=== CHECK 4: Lifecycle states ==="
ACTIVE=$(sqlite3 "${DB_PATH}" "SELECT COUNT(*) FROM media_assets WHERE source='artlist' AND lifecycle_state='ACTIVE';")
STAGING=$(sqlite3 "${DB_PATH}" "SELECT COUNT(*) FROM media_assets WHERE source='artlist' AND lifecycle_state='STAGING';")
log_info "ACTIVE=${ACTIVE} STAGING=${STAGING}"
if [[ "${ACTIVE}" -gt 0 || "${STAGING}" -gt 0 ]]; then
    log_pass "Lifecycle states: ACTIVE=${ACTIVE} STAGING=${STAGING}"
else
    log_warn "No ACTIVE or STAGING artlist assets"
fi

# ====================================================================
# CHECK 5: Outbox events for artlist assets
# ====================================================================
log_info "=== CHECK 5: Outbox events ==="
OUTBOX_TOTAL=$(sqlite3 "${DB_PATH}" "SELECT COUNT(*) FROM outbox_events WHERE event_type='asset.index.requested';")
OUTBOX_COMP=$(sqlite3 "${DB_PATH}" "SELECT COUNT(*) FROM outbox_events WHERE event_type='asset.index.requested' AND status='completed';")
OUTBOX_PEND=$(sqlite3 "${DB_PATH}" "SELECT COUNT(*) FROM outbox_events WHERE event_type='asset.index.requested' AND status='pending';")
OUTBOX_DL=$(sqlite3 "${DB_PATH}" "SELECT COUNT(*) FROM outbox_events WHERE event_type='asset.index.requested' AND status='dead_letter';")
log_info "total=${OUTBOX_TOTAL} completed=${OUTBOX_COMP} pending=${OUTBOX_PEND} dead_letter=${OUTBOX_DL}"
if [[ "${OUTBOX_DL}" -gt 0 ]]; then
    log_warn "${OUTBOX_DL} dead_letter events (check Qdrant connectivity)"
elif [[ "${OUTBOX_COMP}" -gt 0 ]]; then
    log_pass "Outbox: ${OUTBOX_COMP} completed events"
else
    log_warn "No completed outbox events"
fi

# ====================================================================
# CHECK 6: Qdrant scroll for artlist source
# ====================================================================
log_info "=== CHECK 6: Qdrant scroll (source=artlist) ==="
SCROLL=$(curl -sS --connect-timeout "${SCRAPER_CONNECT_TIMEOUT_SECONDS:-5}" --max-time "${SCROLL_TIMEOUT:-120}" -X POST "${QDRANT_URL}/collections/${COLLECTION}/points/scroll" \
    -H 'Content-Type: application/json' \
    -d '{"filter":{"must":[{"key":"source","match":{"value":"artlist"}}]},"limit":10,"with_payload":true,"with_vector":false}' 2>/dev/null)
SCROLL_COUNT=$(echo "${SCROLL}" | jq -r '.result.points | length // 0' 2>/dev/null)
if [[ "${SCROLL_COUNT}" -gt 0 ]]; then
    log_pass "Qdrant: ${SCROLL_COUNT} artlist points"
else
    log_warn "No artlist points in Qdrant (indexing may not have run)"
fi

# ====================================================================
# CHECK 7: Hybrid search returns artlist results
# ====================================================================
log_info "=== CHECK 7: Hybrid search (sources=['artlist']) ==="
SEARCH=$(curl -s --max-time 30 -X POST "${BASE_URL}/api/media/search" \
    -H "Authorization: Bearer ${TOKEN}" \
    -H 'Content-Type: application/json' \
    -H 'X-Workspace-ID: default' \
    -d '{"query":"boxing training","sources":["artlist"],"mode":"hybrid","limit":5}' 2>/dev/null)
SEARCH_COUNT=$(echo "${SEARCH}" | jq -r '.results | length // 0' 2>/dev/null)
if [[ "${SEARCH_COUNT}" -gt 0 ]]; then
    HAS_ARTLIST=$(echo "${SEARCH}" | jq -r '[.results[] | select(.source=="artlist")] | length' 2>/dev/null)
    if [[ "${HAS_ARTLIST}" -gt 0 ]]; then
        log_pass "Hybrid search: ${HAS_ARTLIST} artlist results (of ${SEARCH_COUNT} total)"
    else
        log_warn "Search returned results but none from artlist source"
    fi
else
    log_warn "Search returned 0 results (backend may be down)"
fi

# ====================================================================
# CHECK 8: Supersede gate — no duplicate asset_ids
# ====================================================================
log_info "=== CHECK 8: Supersede gate (no duplicate asset_ids) ==="
DUPES=$(sqlite3 "${DB_PATH}" "SELECT COUNT(*) FROM (SELECT id, COUNT(*) as cnt FROM media_assets WHERE source='artlist' GROUP BY id HAVING cnt > 1);" 2>/dev/null)
if [[ "${DUPES}" -eq 0 ]]; then
    log_pass "Supersede gate: 0 duplicate asset_ids"
else
    log_fail "Supersede gate: ${DUPES} duplicate asset_ids!"
fi

# ====================================================================
# CHECK 9: index_state for artlist assets
# ====================================================================
log_info "=== CHECK 9: index_state ==="
INDEXED=$(sqlite3 "${DB_PATH}" "SELECT COUNT(*) FROM media_assets WHERE source='artlist' AND index_state='INDEXED';")
DISCOVERED=$(sqlite3 "${DB_PATH}" "SELECT COUNT(*) FROM media_assets WHERE source='artlist' AND index_state='DISCOVERED';")
SKIPPED=$(sqlite3 "${DB_PATH}" "SELECT COUNT(*) FROM media_assets WHERE source='artlist' AND index_state='INDEXING_SKIPPED_NO_INDEXER';")
log_info "INDEXED=${INDEXED} DISCOVERED=${DISCOVERED} SKIPPED=${SKIPPED}"
if [[ "${INDEXED}" -gt 0 ]]; then
    log_pass "Index state: ${INDEXED} INDEXED"
else
    log_warn "No INDEXED artlist assets"
fi

# ====================================================================
# CHECK 10: Scraper health
# ====================================================================
log_info "=== CHECK 10: Scraper health ==="
SCRAPER=$(curl -sS --connect-timeout "${SCRAPER_CONNECT_TIMEOUT_SECONDS:-5}" --max-time "${SCROLL_TIMEOUT:-120}" http://127.0.0.1:9123/health 2>/dev/null)
if echo "${SCRAPER}" | jq -e '.ok == true' > /dev/null 2>&1; then
    UPTIME=$(echo "${SCRAPER}" | jq -r '.uptime_seconds')
    BROWSER=$(echo "${SCRAPER}" | jq -r '.browser_running')
    log_pass "Scraper: ok uptime=${UPTIME}s browser=${BROWSER}"
else
    log_fail "Scraper not healthy"
fi

# ====================================================================
# CHECK 11: Qdrant health
# ====================================================================
log_info "=== CHECK 11: Qdrant health ==="
QDRANT_HEALTH=$(curl -s --max-time 3 "${QDRANT_URL}/collections" 2>/dev/null)
QDRANT_COLS=$(echo "${QDRANT_HEALTH}" | jq -r '.result.collections | length // 0' 2>/dev/null)
if [[ "${QDRANT_COLS}" -gt 0 ]]; then
    log_pass "Qdrant: ${QDRANT_COLS} collections"
else
    log_fail "Qdrant unreachable or 0 collections"
fi

# ====================================================================
# Verdict
# ====================================================================
echo
echo "============================================"
echo "  Artlist Preflight Checklist"
echo "  PASS=${PASS}  WARN=${WARN}  FAIL=${FAIL}"
echo "============================================"

if [[ "${FAIL}" -gt 0 ]]; then
    echo "VERDICT: ${FAIL} CHECK(S) FAILED"
    exit 1
elif [[ "${WARN}" -gt 0 ]]; then
    echo "VERDICT: ${PASS} PASS, ${WARN} WARNINGS (operator review recommended)"
    exit 0
else
    echo "VERDICT: ALL ${PASS} CHECKS PASSED"
    exit 0
fi
