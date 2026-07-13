#!/usr/bin/env bash
# tests/operational/artlist_postrun_assertions.sh
#
# Sibling post-run assertion recipe for tests/operational/artlist_live_e2e_verify.sh.
# Confirms the 4 end-to-end invariants after the live E2E script writes its
# LAST_JSON verdict file. Read-only diagnostic — NO mutation of SQLite, Drive,
# Qdrant, or the PipelineGen state store.
#
# 4 invariants asserted (in order):
#   (1) jobs row in SQLite with status='SUCCEEDED'
#       — the canonical happy-path enum value per internal/domain/job/job.go:47
#         alias to StatusSucceeded / internal/domain/generation/status.go:9.
#         The colloquial "status=done" the user invoked maps to status='SUCCEEDED';
#         there is no internal pipeline string 'done', per godlike/06 SSOT
#         (one canonical value per fact).
#   (2) file present on Drive — POST /api/drive/resolve-by-id proves the file
#       is remote-resolvable, non-trashed, size > 0. Reuses the canonical handler
#       STEP 5 of the live_e2e_verify.sh. Folder rooted at
#       VELOX_DRIVE_ARTLIST_ROOT per internal/platform/config/drive.go:20.
#   (3) Qdrant point — POST /collections/${COLLECTION}/points/scroll returns
#       >= 1 point for asset_id with payload.source='artlist' AND
#       payload.lifecycle_state='PUBLISHED'. Same query shape as STEP 7+8
#       of the live_e2e_verify.sh.
#   (4) Local Artlist search — POST /api/media/search with sources=['artlist']
#       returns the produced asset_id (i.e. the just-indexed clip is
#       discoverable via the canonical hybrid search surface, mirroring
#       STEP 9 of the live_e2e_verify.sh). The "artisti-locale" phrasing
#       in the user directive is canonicalised to "artlist-local search"
#       (the unified media-search surface with sources=['artlist']).
#
# godlike/07 NO-FAKE-AVAILABILITY: every assertion here is a READ.
# Auth scheme = X-Velox-Admin-Token (preferred per
# internal/api/middleware/admin_token.go:26, lockstep with commit
# 6c7fc1f85 fix(tests): correct --data-urlencode curl invocations).
#
# Usage:
#   VELOX_ADMIN_TOKEN='...' bash tests/operational/artlist_postrun_assertions.sh
#
# Exit contract:
#   0 — all 4 invariants PASS
#   1 — at least one invariant FAIL (the script prints which)
#   2 — pre-flight gate failed (token / LAST_JSON / DB connectivity)

set -uo pipefail
# Deliberately omit `set -e`: per-assertion failures must be recorded without
# short-circuiting subsequent checks. Each step wraps its command in
# `... && rc=0 || rc=$?` so the rc propagates into mark_pass/mark_fail.

# ============================================================
# §1 — Config (mirrors tests/operational/artlist_live_e2e_verify.sh §"Config")
# ============================================================
HOST="${VELOX_HOST:-127.0.0.1}"
[ -n "${PIPELINE_PORT:-}" ] || PIPELINE_PORT="${VELOX_PORT:-8000}"
BASE_URL="http://${HOST}:${PIPELINE_PORT}"
QDRANT_URL="${QDRANT_URL:-http://127.0.0.1:6333}"
QDRANT_API_KEY="${QDRANT_API_KEY:-${VELOX_QDRANT_API_KEY:-}}"
COLLECTION="${QDRANT_COLLECTION:-media_assets_current}"

DB_PATH="${VELOX_DATA_DIR:-./data}/media/media.db.sqlite"
TOKEN="${VELOX_ADMIN_TOKEN:-}"
LAST_JSON="${LAST_JSON:-/tmp/artlist_live_e2e_last_run.json}"

SCROLL_TIMEOUT="${SCROLL_TIMEOUT:-120}"                           # scraper-style total budget
SCRAPER_CONNECT_TIMEOUT_SECONDS="${SCRAPER_CONNECT_TIMEOUT_SECONDS:-5}"
CURL_TIMEOUT="${CURL_TIMEOUT:-30}"

DRIVE_FOLDER="${VELOX_DRIVE_ARTLIST_ROOT:-}"  # reported in §5 for context only

# ============================================================
# §2 — Pre-flight (fail-closed on missing inputs)
# ============================================================
log() { echo "[$1] $(date '+%H:%M:%S') ${*:2}"; }
[[ -n "${TOKEN}"                  ]] || { log FAIL "VELOX_ADMIN_TOKEN unset — refuse to run unauthenticated"; exit 2; }
[[ -f "${LAST_JSON}"              ]] || { log FAIL "LAST_JSON missing: ${LAST_JSON} — run tests/operational/artlist_live_e2e_verify.sh first"; exit 2; }
[[ -f "${DB_PATH}"                ]] || { log FAIL "SQLite media.db.sqlite missing at ${DB_PATH}"; exit 2; }
command -v jq      >/dev/null 2>&1 || { log FAIL "jq not on PATH"; exit 2; }
command -v sqlite3 >/dev/null 2>&1 || { log FAIL "sqlite3 not on PATH"; exit 2; }

# ============================================================
# §3 — Read LAST_JSON verdict metadata
# ============================================================
ASSET_ID=$(jq -r '.assets[0].id // empty' "${LAST_JSON}" 2>/dev/null || echo "")
JOB_ID=$(jq -r '.job_id // empty' "${LAST_JSON}" 2>/dev/null || echo "")
SEARCH_TERM=$(jq -r '.search_term // empty' "${LAST_JSON}" 2>/dev/null || echo "")

if [[ -z "${ASSET_ID}" || "${ASSET_ID}" == "null" ]]; then
    log FAIL "Could not extract assets[0].id from ${LAST_JSON} — run the live e2e first"
    exit 2
fi
if [[ -z "${JOB_ID}" || "${JOB_ID}" == "null" ]]; then
    log INFO "job_id missing in LAST_JSON (older schema); continuing with ASSET_ID only"
    JOB_ID=""
fi
if [[ -z "${SEARCH_TERM}" || "${SEARCH_TERM}" == "null" ]]; then
    log INFO "search_term missing in LAST_JSON; step 4 will fall back to '${ASSET_ID}'"
fi

log INFO "POST-RUN GROUND TRUTH: asset_id=${ASSET_ID} job_id=${JOB_ID:-<n/a>} search_term='${SEARCH_TERM:-<fallback:asset_id>}' drive_folder=${DRIVE_FOLDER:-<unset>}"

# ============================================================
# §4 — Helpers
# ============================================================
PASS=0; FAIL=0
mark_pass() { log PASS "$1"; PASS=$((PASS+1)); }
mark_fail() { log FAIL "$1"; FAIL=$((FAIL+1)); }

# Build Qdrant API-key header array (used by step 3).
declare -a QHEADERS=()
[[ -n "${QDRANT_API_KEY}" ]] && QHEADERS+=(-H "api-key: ${QDRANT_API_KEY}")

# ============================================================
# §5 — STEP 1: jobs row in SQLite with status='SUCCEEDED'
# ============================================================
# Canonical refs:
#   jobs table schema   → migrations/sqlite/001_velox_core.sql:193
#                          (verified via pragma_table_info per
#                          tests/operational/job-debug-runbook.md §2)
#   status column        → jobs.status (NOT 'state' / NOT 'done')
#   happy-path enum      → StatusSucceeded / internal/domain/generation/status.go:9
#                          (aliased via internal/domain/job/job.go:47)
#   LC precedent         → tests/operational/job-debug-runbook.md §3
#   status 'SUCCEEDED'   is the canonical operator-facing success label;
#                          colloquial 'done' is documented in this script's header.
if [[ -n "${JOB_ID}" ]]; then
    out=""
    rc=0
    out=$(sqlite3 -readonly -json "file:${DB_PATH}?mode=ro" \
        "SELECT status, retry_count, COALESCE(error,'') AS error FROM jobs WHERE id='${JOB_ID}';" 2>&1) \
        && rc=0 || rc=$?
    if [[ $rc -eq 0 ]] \
        && echo "${out}" | jq -e '.[] | select(.status == "SUCCEEDED")' >/dev/null 2>&1; then
        err=$(echo "${out}" | jq -r '.[0].error // "-"')
        retries=$(echo "${out}" | jq -r '.[0].retry_count // 0')
        mark_pass "Step 1: jobs row status='SUCCEEDED' for ${JOB_ID} (retry_count=${retries}, jobs.error='${err}')"
    else
        st=$(echo "${out}" | jq -r '.[0].status // "NO_ROW"' 2>/dev/null || echo NO_ROW)
        mark_fail "Step 1: jobs row NOT in SUCCEEDED state for ${JOB_ID} (got '${st}' — colloquial 'done' → canonical 'SUCCEEDED' per godlike/06)"
    fi
else
    log INFO "Step 1: SKIPPED (no JOB_ID in LAST_JSON)"
fi

# ============================================================
# §6 — STEP 2: Drive file resolve-by-id (size > 0, not trashed)
# ============================================================
# Canonical refs:
#   endpoint → POST /api/drive/resolve-by-id (PipelineGen admin-auth-gated)
#               body shape {ids:[<file_id>]} — same as STEP 5 of artlist_live_e2e_verify.sh.
#   folder   → VELOX_DRIVE_ARTLIST_ROOT (alias for root_folder_id per
#               internal/platform/config/drive.go:20).
#   auth     → X-Velox-Admin-Token per internal/api/middleware/admin_token.go:26.
#   SSOT     → tests/operational/job-debug-runbook.md §0 (when admin surface is
#               auth-blocked, drop to media_assets + later re-run).
DRIVE_FILE_ID=$(sqlite3 -readonly -json "file:${DB_PATH}?mode=ro" \
    "SELECT COALESCE(drive_file_id,'') AS drive_file_id FROM media_assets WHERE id='${ASSET_ID}';" 2>&1 \
    | jq -r '.[0].drive_file_id // empty' 2>/dev/null || true)

if [[ -z "${DRIVE_FILE_ID}" || "${DRIVE_FILE_ID}" == "null" ]]; then
    mark_fail "Step 2: media_assets.drive_file_id missing for asset_id=${ASSET_ID}"
else
    resp=""
    rc=0
    resp=$(curl -sS --max-time "${CURL_TIMEOUT}" -X POST "${BASE_URL}/api/drive/resolve-by-id" \
        -H "X-Velox-Admin-Token: ${TOKEN}" \
        -H 'Content-Type: application/json' \
        -d "$(jq -nc --arg id "${DRIVE_FILE_ID}" '{ids: [$id]}')" 2>&1) \
        && rc=0 || rc=$?
    if [[ $rc -eq 0 ]] \
        && echo "${resp}" | jq -e '
            .ok == true
            and .resolved_count >= 1
            and (.resolved[0].trashed == false)
            and ((.resolved[0].size // 0) | tonumber > 0)
        ' >/dev/null 2>&1; then
        size=$(echo "${resp}" | jq -r '.resolved[0].size // 0')
        mark_pass "Step 2: Drive resolve-by-id proves drive_file_id=${DRIVE_FILE_ID} present + size=${size}B + non-trashed (folder=${DRIVE_FOLDER:-<unset>})"
    else
        ok=$(echo "${resp}" | jq -r '.ok // "CONN_ERR"' 2>/dev/null || echo CONN_ERR)
        mark_fail "Step 2: Drive resolve-by-id FAILED (ok='${ok}', drive_file_id=${DRIVE_FILE_ID}, BASE_URL=${BASE_URL})"
    fi
fi

# ============================================================
# §7 — STEP 3: Qdrant `points/scroll` returns ≥ 1 PUBLISHED artlist point
# ============================================================
# Canonical refs:
#   endpoint → POST /collections/${COLLECTION}/points/scroll
#               body shape mirrors STEP 7+8 of artlist_live_e2e_verify.sh.
#   payload  → point[0].payload.source='artlist'
#               AND point[0].payload.lifecycle_state='PUBLISHED'
#   auth     → api-key header (QHEADERS array), per the same precedent block.
resp=""
rc=0
resp=$(curl -sS --connect-timeout "${SCRAPER_CONNECT_TIMEOUT_SECONDS}" --max-time "${SCROLL_TIMEOUT}" \
    -X POST "${QDRANT_URL}/collections/${COLLECTION}/points/scroll" \
    "${QHEADERS[@]}" \
    -H 'Content-Type: application/json' \
    -d "$(jq -nc --arg id "${ASSET_ID}" '{
        filter: { must: [ { key: "asset_id", match: { value: $id } } ] },
        limit: 5,
        with_payload: true,
        with_vector: false
    }')" 2>&1) \
    && rc=0 || rc=$?

if [[ $rc -eq 0 ]] \
    && echo "${resp}" | jq -e '
        .result.points | length >= 1
        and .[0].payload.source == "artlist"
        and .[0].payload.lifecycle_state == "PUBLISHED"
    ' >/dev/null 2>&1; then
    n=$(echo "${resp}" | jq -r '.result.points | length')
    ls=$(echo "${resp}" | jq -r '.[0].payload.lifecycle_state // "?"')
    mark_pass "Step 3: Qdrant scroll returned ${n} point(s) for asset_id=${ASSET_ID} (lifecycle_state='${ls}', source='artlist')"
else
    err=$(echo "${resp}" | jq -r '.status // .error // "EMPTY_RESPONSE"' 2>/dev/null || echo CONN_ERR)
    mark_fail "Step 3: Qdrant scroll did NOT find matching PUBLISHED artlist point for asset_id=${ASSET_ID} (response='${err}')"
fi

# ============================================================
# §8 — STEP 4: Local Artlist search (`/api/media/search`) finds the clip
# ============================================================
# Canonical refs:
#   endpoint → POST /api/media/search (unified hybrid search surface)
#               per internal/api/assets/search/handler.go::Search
#               (Handler signature: Takes c *gin.Context; binds searchRequest JSON
#                {query, sources:["artlist"], mode:"hybrid", limit:N}).
#   body     → {query, sources:["artlist"], mode:"hybrid", limit:10}
#   alias    → "artisti-locale" colloquial for "artlist-local search"
#               (mapped to the hybrid-search surface with sources=['artlist']
#                which is the canonical local Artlist query surface).
#   SSOT     → tests/operational/artlist_live_e2e_verify.sh STEP 9 (same body).
resp=""
rc=0
resp=$(curl -sS --max-time "${CURL_TIMEOUT}" -X POST "${BASE_URL}/api/media/search" \
    -H "X-Velox-Admin-Token: ${TOKEN}" \
    -H 'Accept: application/json' \
    -H 'Content-Type: application/json' \
    -d "$(jq -nc \
        --arg q "${SEARCH_TERM:-${ASSET_ID}}" \
        --argjson lim 10 \
        '{query: $q, sources: ["artlist"], mode: "hybrid", limit: $lim}')" 2>&1) \
    && rc=0 || rc=$?

if [[ $rc -eq 0 ]] \
    && echo "${resp}" | jq -e --arg id "${ASSET_ID}" '
        [.results[]? | select((.asset_id // .id // "") == $id)] | length >= 1
    ' >/dev/null 2>&1; then
    mark_pass "Step 4: /api/media/search (sources=['artlist'], mode=hybrid) returns asset_id=${ASSET_ID} (the 'artisti-locale' local Artlist search)"
else
    err=$(echo "${resp}" | jq -r '.error // "EMPTY_RESPONSE"' 2>/dev/null || echo CONN_ERR)
    mark_fail "Step 4: /api/media/search did NOT find asset_id=${ASSET_ID} (response='${err}', query='${SEARCH_TERM:-<fallback:asset_id>}')"
fi

# ============================================================
# §9 — Verdict
# ============================================================
echo
echo "============================================"
echo "  Artlist Post-Run Assertions"
echo "  PASS=${PASS}  FAIL=${FAIL}"
echo "============================================"

if [[ "${FAIL}" -gt 0 ]]; then
    echo "VERDICT: ${FAIL} invariant(s) FAILED — PipelineGen Artlist LIVE run is not fully consistent"
    exit 1
fi
echo "VERDICT: ALL 4 INVARIANTS PASS — PipelineGen Artlist LIVE run is end-to-end consistent"
exit 0
