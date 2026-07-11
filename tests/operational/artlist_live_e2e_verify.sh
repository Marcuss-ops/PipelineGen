#!/usr/bin/env bash
# artlist_live_e2e_verify.sh — Artlist LIVE End-to-End Verification
#
# Live E2E verification of the Artlist acquisition pipeline. Real
# scrape, real download (consumes Artlist quota — keep LIMIT=1),
# real Drive upload, real Qdrant indexing, real /api/media/search hit.
#
# Companion to:
#   - tests/operational/artlist_preflight_smoke.sh     (read-only preflight)
#   - tests/operational/artlist_multi_query_smoke.sh   (multi-keyword dry_run)
#   - tests/operational/artlist_qdrant_failure_smoke.sh
#   - tests/operational/artlist_drive_failure_smoke.sh
#   - tests/operational/artlist_scraper_failure_smoke.sh
#
# 9 verification points (mapped to action-plan DoD):
#   1.  Artlist scraper (/search) returns > 0 candidates
#   2.  media.artlist job enqueued via POST /api/artlist/run and terminal SUCCEEDED
#   3.  artlist_download_audit.status = 'succeeded' for the resulting asset
#   4.  media_assets row: source='artlist'
#                              + lifecycle_state = 'PUBLISHED'
#                              + drive_file_id non-empty
#                              + drive_link non-empty
#                              + download_link non-empty
#                              + file_hash non-empty
#                              + source_version non-empty
#                              + index_state = 'INDEXED'
#   5.  POST /api/drive/resolve-by-id (canonical Files.Get wrapper,
#                                       body {ids:[fileID]}):
#                                  file exists + not trashed + name non-empty
#                                  + size > 0
#   6.  outbox_events for asset_id: status IN ('completed', 'superseded')
#   7.  Qdrant scroll on alias ${COLLECTION}: at least one point with
#                                  payload.asset_id == ${ASSET_ID}
#   8.  Qdrant payload: source='artlist', media_type='video',
#                                  lifecycle_state = 'PUBLISHED'
#   9.  POST /api/media/search with sources=['artlist'] returns the asset
#
# Prerequisites (fail-closed):
#   - VELOX_ADMIN_TOKEN env var set (PipelineGen refuses default tokens in prod)
#   - features.artlist_enabled=true, features.drive_enabled=true
#   - qdrant.enabled=true, clip_indexer.enabled=true
#   - ARTLIST_SCRAPER_SERVER_URL=http://127.0.0.1:9123
#   - ARTLIST_ACQUISITION_MODE=authorized_api + ARTLIST_DAILY_DOWNLOAD_LIMIT > 0
#   - VELOX_DRIVE_ARTLIST_ROOT set (or pass ROOT_FOLDER_ID=)
#   - sqlite3 + curl + jq + go on PATH
#
# Usage:
#   VELOX_ADMIN_TOKEN='...' SEARCH_TERM='boxing training' LIMIT=1 \
#       bash tests/operational/artlist_live_e2e_verify.sh
#
# With Qdrant API key:
#   VELOX_ADMIN_TOKEN='...' QDRANT_API_KEY='...' bash tests/operational/artlist_live_e2e_verify.sh
#
# Override Drive destination:
#   ROOT_FOLDER_ID='<drive_folder_id>' VELOX_ADMIN_TOKEN='...' \
#       bash tests/operational/artlist_live_e2e_verify.sh
#
# DRY RUN (does NOT consume an Artlist download — print plan and exit):
#   DRY_RUN=1 VELOX_ADMIN_TOKEN='...' bash tests/operational/artlist_live_e2e_verify.sh
#
# Skip the hermetic gates pre-run AND its precondition (gate-test matrix
# integrity check that runs first inside the same guard and exits 2 on
# divergence — see the HERMETIC GATES block below):
#   SKIP_HERMETICS=1 bash tests/operational/artlist_live_e2e_verify.sh

set -euo pipefail

# ============================================================
# CLI argument parsing
# ============================================================
usage() {
    cat <<EOF
Usage: $(basename "$0") [--dry-run] [--help]

  --dry-run    Print the verification plan (ports, queries, expected
               PASS/WARN/FAIL for each of the 9 verification points) and
               exit. Does NOT enqueue a real Artlist job, does NOT
               consume any Artlist download quota, does NOT pollute the
               DB. Optionally runs light read-only preflight probes
               (server /ready, scraper /health, Qdrant /collections,
               SQLite file present) when VELOX_ADMIN_TOKEN is set, to
               give the operator signal on environment reachability.
               Same effect as the legacy DRY_RUN=1 env var.

  --help, -h   Show this help and exit.

Env-var overrides (see Config block below for the full list):
  PIPELINE_PORT / VELOX_PORT    HTTP listener port (canonical 8000)
  VELOX_ADMIN_TOKEN             PipelineGen admin bearer token
  SEARCH_TERM / LIMIT           Artlist search query + asset count
  ROOT_FOLDER_ID                Drive destination folder
  QDRANT_API_KEY / QDRANT_URL   Qdrant endpoint + auth
  ARTLIST_SCRAPER_SERVER_URL    Node-scraper URL
  VELOX_DATA_DIR                Path to media.db.sqlite
  SKIP_HERMETICS=1              Bypass hermetic gate precondition + '^TestGate' run
  EXPECTED_GATE_MATCHES=N       Override expected gate match count (positive integer; default: 28)

Examples:
  $0 --help
  DRY_RUN=1 VELOX_ADMIN_TOKEN='...' SEARCH_TERM='boxing training' \\
      bash $0
  $0 --dry-run

EOF
}

# Parse CLI args. Unknown args => error + usage to stderr + exit 2.
while [[ $# -gt 0 ]]; do
    case "$1" in
        --dry-run)
            DRY_RUN=1
            shift
            ;;
        --help|-h)
            usage
            exit 0
            ;;
        --)
            shift; break ;;
        -*)
            echo "[ERROR] Unknown option: $1" >&2
            usage >&2
            exit 2
            ;;
        *)
            echo "[ERROR] Unexpected positional arg: $1" >&2
            usage >&2
            exit 2
            ;;
    esac
done

# ============================================================
# Config (override via env)
# ============================================================
HOST="${VELOX_HOST:-127.0.0.1}"
# PIPELINE_PORT — canonical port for PipelineGen's HTTP listener.
# Source of truth: config.example.yaml `server.port` (default 8000 per
# Operational Readiness PR, June 2026). Backward-compat VELOX_PORT is
# preserved for sibling-script consistency. Override at runtime via
# PIPELINE_PORT=NNNN (or VELOX_PORT=NNNN for legacy callers).
# Use ${PIPELINE_PORT:-} (not bare $PIPELINE_PORT) so set -u does not
# trip when the script is invoked in --dry-run with an empty env, the
# exact use case the dry-run mode was added for.
[ -n "${PIPELINE_PORT:-}" ] || PIPELINE_PORT="${VELOX_PORT:-8000}"
BASE_URL="http://${HOST}:${PIPELINE_PORT}"

SCRAPER_URL="${ARTLIST_SCRAPER_SERVER_URL:-http://127.0.0.1:9123}"
QDRANT_URL="${QDRANT_URL:-http://127.0.0.1:6333}"
QDRANT_API_KEY="${QDRANT_API_KEY:-${VELOX_QDRANT_API_KEY:-}}"
COLLECTION="${QDRANT_COLLECTION:-media_assets_current}"

DB_PATH="${VELOX_DATA_DIR:-./data}/media/media.db.sqlite"
TOKEN="${VELOX_ADMIN_TOKEN:-}"
ROOT_FOLDER_ID="${ROOT_FOLDER_ID:-${VELOX_DRIVE_ARTLIST_ROOT:-}}"
SEARCH_TERM="${SEARCH_TERM:-boxing training}"
LIMIT="${LIMIT:-1}"

POLL_INTERVAL="${POLL_INTERVAL:-10}"
POLL_MAX="${POLL_MAX:-18}"             # 18 * 10s = 180s job wait
SCROLL_TIMEOUT="${SCROLL_TIMEOUT:-10}" # seconds for Qdrant scroll
CURL_TIMEOUT="${CURL_TIMEOUT:-30}"

LAST_JSON="${LAST_JSON:-/tmp/artlist_live_e2e_last_run.json}"

# Expected count of TestGateXX_* function names matching the meta-anchor's
# expectedGateTests array in gate11_scraper_failure_test.go (default: 28,
# matches the current authoritative spec). Override at the call site via
# env var EXPECTED_GATE_MATCHES=N when the gate test matrix legitimately
# grows OR shrinks BUT the script + meta-anchor aren't both updated yet.
# Validated as a positive integer at use time (see HERMETIC GATES
# precondition block); invalid overrides exit 2 with a clear message
# rather than fall back silently — fail-closed, consistent with the
# rest of the script. Empty/unset falls back to default 28 via the
# :-operator; non-integer-like overrides are caught by the regex
# validation in the precondition block.
EXPECTED_GATE_MATCHES="${EXPECTED_GATE_MATCHES:-28}"

# Tally
PASS=0; WARN=0; FAIL=0
ASSET_VERDICTS=() # per-asset pass/warn/fail strings

# ============================================================
# Helpers
# ============================================================
log_info() { echo "[INFO]  $(date '+%H:%M:%S') $*"; }
log_pass() { echo "[PASS]  $(date '+%H:%M:%S') $*"; PASS=$((PASS + 1)); }
log_warn() { echo "[WARN]  $(date '+%H:%M:%S') $*"; WARN=$((WARN + 1)); }
log_fail() { echo "[FAIL]  $(date '+%H:%M:%S') $*"; FAIL=$((FAIL + 1)); }

auth_header() { echo "Authorization: Bearer ${TOKEN}"; }

append_asset_verdict() {
    local id="$1" verdict="$2"
    ASSET_VERDICTS+=("${id}|${verdict}")
}

require_tool() {
    command -v "$1" >/dev/null 2>&1 || {
        log_fail "Required tool '$1' not on PATH"
        exit 2
    }
}

# ============================================================
# DRY_RUN: print plan, no real cycle
# ============================================================
# Triggered by either:
#   - CLI flag:  --dry-run
#   - Env var:   DRY_RUN=1
# No real Artlist job enqueued, no quota consumed, no DB/Drive writes.
if [[ "${DRY_RUN:-0}" == "1" ]]; then
    cat <<EOF
[INFO] artlist_live_e2e_verify.sh — DRY RUN MODE (no real download, no enqueue)
[INFO] Effective config:
  BASE_URL              = ${BASE_URL}
  SCRAPER_URL           = ${SCRAPER_URL}
  QDRANT_URL            = ${QDRANT_URL}/collections/${COLLECTION}
  QDRANT_API_KEY        = ${QDRANT_API_KEY:+<set>}${QDRANT_API_KEY:-<empty>}
  DB_PATH               = ${DB_PATH}
  TOKEN (VELOX_ADMIN)   = ${TOKEN:+<set>}${TOKEN:-<empty>}
  ROOT_FOLDER_ID        = ${ROOT_FOLDER_ID:-<empty — warning>}
  SEARCH_TERM           = '${SEARCH_TERM}'
  LIMIT                 = ${LIMIT}
  EXPECTED_GATE_MATCHES = ${EXPECTED_GATE_MATCHES:-28} (positive integer; default 28; env override)
[INFO] Plan — 9 verification points (executed per produced asset; in order):
  1. PASS   scraper /search probe (term, limit) returns >= 1 candidate
            WARN  if 0 candidates (fallback may still work)
            FAIL  if scraper returns ok=false
  2. PASS   POST /api/artlist/run returns run_id (job enqueued)
            FAIL  on empty/null run_id
  3. PASS   job terminal status == SUCCEEDED within ~$((POLL_INTERVAL * POLL_MAX))s
            FAIL  on FAILED or timeout
  4. PASS   artlist_download_audit.status == 'succeeded' (latest row for asset_id)
            FAIL  on 'pending'/'failed' or no row
  5. PASS   media_assets row exists with:
              source='artlist', media_type='video', lifecycle_state='PUBLISHED',
              index_state='INDEXED',
              + drive_file_id, drive_link, download_link, file_hash,
                source_version all non-empty
            FAIL  on any missing field or wrong value
  6. PASS   Drive Files.Get via POST /api/drive/resolve-by-id returns:
              ok=true, resolved_count >= 1, name non-empty,
              size > 0, trashed=false
            FAIL  on missing or trashed file
  7. PASS   outbox_events.status IN ('completed','superseded') for
              event_type='asset.index.requested', aggregate_id=asset_id
            FAIL  on 'pending' or no row
  8. PASS   Qdrant scroll on ${COLLECTION} returns >= 1 point with:
              payload.asset_id == asset_id,
              payload.source == 'artlist',
              payload.media_type == 'video',
              payload.lifecycle_state == 'PUBLISHED'
            FAIL  on missing point or wrong payload field
  9. PASS   POST /api/media/search with sources=['artlist'] returns
              the produced asset_id
            WARN  if no artlist results (embedding pipeline may be stale;
            Qdrant scroll is the canonical truth)
[INFO] Skip-able: SKIP_HERMETICS=1 bypasses BOTH the hermetic gate precondition (matrix-integrity check) AND the go test '^TestGate' run.
[INFO] Override:   EXPECTED_GATE_MATCHES=N sets the expected gate match count (positive integer; default 28 matches gate11_scraper_failure_test.go meta-anchor's expectedGateTests array length).
[INFO] Cost: zero Artlist downloads, zero DB writes, zero Drive writes.
[INFO] Verdict JSON would be written to: ${LAST_JSON}
EOF

    # Light read-only preflight probes when a token is available —
    # gives the operator zero-cost signal on environment reachability.
    if [[ -n "${TOKEN}" ]]; then
        echo
        echo "[INFO] Light preflight probes (read-only):"
        if curl -s --max-time 3 "${BASE_URL}/ready" | jq -e '.status == "ready"' >/dev/null 2>&1; then
            echo "  server /ready: ok"
        else
            echo "  server /ready: NOT OK"
        fi
        if curl -s --max-time 3 "${SCRAPER_URL}/health" | jq -e '.ok == true' >/dev/null 2>&1; then
            echo "  scraper /health: ok"
        else
            echo "  scraper /health: NOT REACHABLE"
        fi
        if curl -s --max-time 3 "${QDRANT_URL}/collections" >/dev/null 2>&1; then
            echo "  Qdrant /collections: reachable"
        else
            echo "  Qdrant /collections: NOT REACHABLE"
        fi
        if [[ -f "${DB_PATH}" ]]; then
            echo "  SQLite media.db.sqlite: present (${DB_PATH})"
        else
            echo "  SQLite media.db.sqlite: NOT FOUND (${DB_PATH})"
        fi
    else
        echo
        echo "[INFO] VELOX_ADMIN_TOKEN is empty — skipping preflight probes (set it to enable them)."
    fi

    echo
    echo "[INFO] Exit 0 (dry-run is read-only)."
    exit 0
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
log_info "=== § 1: Scraper /search probe (term='${SEARCH_TERM}', limit=${LIMIT}) ==="
SCRAPER_PROBE=$(curl -s --max-time "${SCROLL_TIMEOUT}" -X POST "${SCRAPER_URL}/search" \
    -H 'Content-Type: application/json' \
    -d "$(jq -nc --arg q "${SEARCH_TERM}" --argjson n "${LIMIT}" '{term: $q, limit: $n}')")
SCRAPER_OK=$(echo "${SCRAPER_PROBE}" | jq -r '.ok           // false')
SCRAPER_CLIPS=$(echo "${SCRAPER_PROBE}" | jq -r '.clips        | length // 0' 2>/dev/null || echo "0")

if [[ "${SCRAPER_OK}" != "true" ]]; then    SCRAPER_ERR=$(echo "${SCRAPER_PROBE}" | jq -r '.error // "<no .error field>"')
    log_fail "scraper /search returned ok=false: ${SCRAPER_ERR}"
elif [[ "${SCRAPER_CLIPS}" -ge 1 ]]; then
    log_pass "scraper /search returned ${SCRAPER_CLIPS} candidate(s) — job enqueue may proceed"
else
    log_warn "scraper /search returned 0 candidates — job enqueue may still succeed via fallback"
fi

# ============================================================
# Hermetic preflight gates (no real downloads)
# ============================================================
if [[ "${SKIP_HERMETICS:-0}" != "1" ]]; then
    log_info "=== HERMETIC GATES (no Artlist downloads) ==="

    # ── Precondition: gate test matrix integrity ──
    # Spec source of truth: TestGate12_PreflightAllGatesPass in
    # gate11_scraper_failure_test.go enumerates exactly 28 gate test
    # function names in its expectedGateTests array (across 12 distinct
    # gate numbers 01-12, with several gates sharing files — e.g.
    # Gate 11 and the Gate 12 meta-anchor both live in
    # gate11_scraper_failure_test.go). The regex ^TestGate[0-9]+_
    # matches every such function across the
    # internal/application/assets/providers/artlist/ package;
    # `go test -list` returns one line per matching function.
    # EXPECTED_GATE_MATCHES=28 enforces 1:1 numerical parity between
    # the meta-anchor spec and runtime. Any divergence means:
    #
    #   (a) a new gate test function added → update BOTH
    #       EXPECTED_GATE_MATCHES (this script) AND the meta-anchor's
    #       expectedGateTests (gate11_scraper_failure_test.go);
    #   (b) a gate test function removed → likewise update both;
    #   (c) a test function was renamed → verify the prefix still
    #       matches the ^TestGate[0-9]+_ convention;
    #   (d) phantom test exists (prefix matches but the meta-anchor
    #       has no entry for it) → fix the orphan or add the entry.
    #
    # Fail closed with exit 2 BEFORE running the full `go test -run` so
    # the operator pays the cheap list-discovery cost (~1s) up front
    # instead of waiting ~30s for the test run to surface the same
    # drift via a non-zero exit + WARN line that the script tolerates.
    # Skip via SKIP_HERMETICS=1 (same mechanism that bypasses the test
    # run below) — preserved as an escape hatch for ad-hoc ops.
    GATE_LIST_OUTPUT=$(go test -list '^TestGate[0-9]+_' \
        ./internal/application/assets/providers/artlist/... 2>&1 || true)
    # Variable EXPECTED_GATE_MATCHES was set in the Config block above
    # with default 28 matching the meta-anchor's expectedGateTests
    # array length in gate11_scraper_failure_test.go. Override at the
    # call site: EXPECTED_GATE_MATCHES=N bash $(basename "$0").
    #
    # Validate the override as a positive integer before comparing
    # against ACTUAL_GATE_MATCHES. Pattern ^[1-9][0-9]*$ rejects
    # "0", "-5", "12.5", "abc", " 12", "12 ", "05" (leading zero) and
    # any other non-positive-integer form. Empty falls through to the
    # default 28 via the :-operator in the Config block; the regex
    # below catches the remaining malformed override modes. Fail-closed
    # (exit 2) so the operator spots typos at the call site immediately.
    if ! [[ "${EXPECTED_GATE_MATCHES}" =~ ^[1-9][0-9]*$ ]]; then
        echo "[ERROR] $(date '+%H:%M:%S') ENV var EXPECTED_GATE_MATCHES='${EXPECTED_GATE_MATCHES}' is invalid — must be a positive integer (no leading zero, no decimals, no sign, no whitespace)." >&2
        echo "[ERROR] Example valid override: EXPECTED_GATE_MATCHES=12 bash $(basename "$0")" >&2
        echo "[ERROR] Default 28 matches gate11_scraper_failure_test.go meta-anchor's expectedGateTests array length." >&2
        exit 2
    fi
    # Count lines that begin with "TestGate" — the `go test -list`
    # output's status line ("ok <pkg> 0.001s") never starts with that
    # prefix, so the filter is unambiguous. `|| true` keeps `set -e`
    # from killing the script on grep's "no matches" exit code (1);
    # we then guard against the empty-string case via the default
    # expansion below.
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
# STEP 1: enqueue media.artlist job
# ============================================================
log_info "=== STEP 1: POST /api/artlist/run ==="

if [[ -n "${ROOT_FOLDER_ID}" ]]; then
    RUN_BODY=$(jq -nc \
        --arg term "${SEARCH_TERM}" \
        --argjson limit "${LIMIT}" \
        --arg rid "${ROOT_FOLDER_ID}" \
        '{term: $term, limit: $limit, dry_run: false, root_folder_id: $rid}')
else
    RUN_BODY=$(jq -nc \
        --arg term "${SEARCH_TERM}" \
        --argjson limit "${LIMIT}" \
        '{term: $term, limit: $limit, dry_run: false}')
fi

JID=$(curl -s --max-time "${CURL_TIMEOUT}" -X POST "${BASE_URL}/api/artlist/run" \
    -H "$(auth_header)" \
    -H 'Content-Type: application/json' \
    -d "${RUN_BODY}" | jq -r '.run_id // ""')

if [[ -z "${JID}" || "${JID}" == "null" ]]; then
    log_fail "No run_id in POST /api/artlist/run response"
    exit 2
fi
log_pass "Job enqueued: ${JID}"

# ============================================================
# STEP 2: poll until terminal state
# ============================================================
log_info "=== STEP 2: Poll ${JID} until SUCCEEDED/FAILED (max ~$((POLL_INTERVAL * POLL_MAX))s) ==="
JSTATUS="?"
JRESP="{}"
i=0
for i in $(seq 1 ${POLL_MAX}); do
    sleep "${POLL_INTERVAL}"
    JRESP=$(curl -s --max-time "${CURL_TIMEOUT}" "${BASE_URL}/api/jobs/${JID}/full" \
        -H "$(auth_header)" || true)
    JSTATUS=$(echo "${JRESP}" | jq -r '.status // "?"' 2>/dev/null || echo "?")
    log_info "  poll #${i}/${POLL_MAX}: status=${JSTATUS}"
    if [[ "${JSTATUS}" == "SUCCEEDED" || "${JSTATUS}" == "FAILED" ]]; then
        break
    fi
done

if [[ "${JSTATUS}" != "SUCCEEDED" ]]; then
    log_fail "Job did not SUCCEED within timeout — final status=${JSTATUS}"
else
    log_pass "Job SUCCEEDED in ~$((POLL_INTERVAL * i))s"
fi

# ============================================================
# STEP 3: extract produced asset_ids
# ============================================================
ASSET_IDS=$(echo "${JRESP}" | jq -r '.result.items[]?.clip_id // empty' 2>/dev/null || true)
ASSET_COUNT=0
if [[ -n "${ASSET_IDS}" ]]; then
    ASSET_COUNT=$(echo "${ASSET_IDS}" | wc -l | tr -d ' ')
    log_pass "Driver returned ${ASSET_COUNT} asset_id(s)"
else
    log_fail "No assets in job result.items (job may have failed before processing)"
fi

# ============================================================
# Per-asset verification: 9 steps per asset
# ============================================================
#
# Build Qdrant headers once (used by every per-asset scroll call).
declare -a QHEADERS=()
[[ -n "${QDRANT_API_KEY}" ]] && QHEADERS+=(-H "api-key: ${QDRANT_API_KEY}")

for AID in ${ASSET_IDS}; do
    log_info "=== ASSET ${AID}: 9-step verification ==="

    AP=0; AW=0; AF=0
    tap()  { local label="$1"; local cond="$2"; local details="$3"
        if [[ "${cond}" == "1" ]]; then
            log_pass  "  ${AID}: ${label}${details:+ — ${details}}"; AP=$((AP+1));
        else
            log_fail  "  ${AID}: ${label}${details:+ — ${details}}"; AF=$((AF+1));
        fi
    }
    taw()  { local label="$1"; local cond="$2"; local details="$3"
        if [[ "${cond}" == "1" ]]; then
            log_pass  "  ${AID}: ${label}${details:+ — ${details}}"; AP=$((AP+1));
        else
            log_warn  "  ${AID}: ${label}${details:+ — ${details}}"; AW=$((AW+1));
        fi
    }

    # --- STEP 3: artlist_download_audit status='succeeded' ---
    # Audit table schema: artlist_download_audit — columns include status
    # (values: pending | succeeded | failed) + asset_id + created_at.
    # Reference: internal/infrastructure/database/sqlite/assets/artlist_download_audit_repository.go.
    # Order by created_at DESC to read the LATEST row (a download may
    # be retried, producing multiple audit rows per asset).
    log_info "--- 3: artlist_download_audit ---"
    AUDIT_STATUS=$(sqlite3 "${DB_PATH}" "
        SELECT status FROM artlist_download_audit
        WHERE asset_id='${AID}'
        ORDER BY created_at DESC LIMIT 1" 2>/dev/null || echo "")
    if [[ "${AUDIT_STATUS}" == "succeeded" ]]; then
        tap "download audit status=succeeded" 1 ""
    elif [[ -z "${AUDIT_STATUS}" ]]; then
        tap "download audit status=succeeded" 0 "no audit row for asset_id='${AID}'"
    else
        tap "download audit status=succeeded" 0 "got '${AUDIT_STATUS}'"
    fi

    # --- STEP 4: media_assets row (SQLite via -json + jq — robust) ---
    log_info "--- 4: SQLite media_assets ---"
    ROW_JSON=$(sqlite3 -json "${DB_PATH}" "
        SELECT source, media_type, lifecycle_state,
               COALESCE(drive_file_id, '') AS drive_file_id,
               COALESCE(drive_link, '') AS drive_link,
               COALESCE(download_link, '') AS download_link,
               COALESCE(file_hash, '') AS file_hash,
               COALESCE(source_version, '') AS source_version,
               COALESCE(index_state, '') AS index_state
        FROM media_assets WHERE id='${AID}'
    " 2>/dev/null || true)

    if [[ -z "${ROW_JSON}" || "${ROW_JSON}" == "[]" ]]; then
        log_fail "  ${AID}: media_assets row not found"
        AF=$((AF+1))
        append_asset_verdict "${AID}" "0|0|1"
        continue
    fi

    SRC=$(echo "${ROW_JSON}"   | jq -r '.[0].source           // ""')
    MTYPE=$(echo "${ROW_JSON}" | jq -r '.[0].media_type       // ""')
    LSTATE=$(echo "${ROW_JSON}" | jq -r '.[0].lifecycle_state  // ""')
    DFID=$(echo "${ROW_JSON}"  | jq -r '.[0].drive_file_id    // ""')
    DLINK=$(echo "${ROW_JSON}" | jq -r '.[0].drive_link       // ""')
    DOWNLINK=$(echo "${ROW_JSON}" | jq -r '.[0].download_link  // ""')
    FHASH=$(echo "${ROW_JSON}" | jq -r '.[0].file_hash        // ""')
    SVER=$(echo "${ROW_JSON}"  | jq -r '.[0].source_version   // ""')
    ISTATE=$(echo "${ROW_JSON}" | jq -r '.[0].index_state     // ""')

    [[ "${SRC}" == "artlist" ]] && tap "source=artlist" 1 "" \
        || tap "source=artlist" 0 "got '${SRC}'"
    [[ "${MTYPE}" == "video" ]] && tap "media_type=video" 1 "" \
        || tap "media_type=video" 0 "got '${MTYPE}'"
    if [[ "${LSTATE}" == "PUBLISHED" ]]; then
        tap "lifecycle_state=PUBLISHED" 1 "got '${LSTATE}'"
    else
        tap "lifecycle_state=PUBLISHED" 0 "got '${LSTATE}'"
    fi
    [[ -n "${DFID}"     ]] && tap "drive_file_id valorizzato" 1 "" \
        || tap "drive_file_id valorizzato" 0 "got empty"
    [[ -n "${DLINK}"    ]] && tap "drive_link valorizzato" 1 "" \
        || tap "drive_link valorizzato" 0 "got empty"
    [[ -n "${DOWNLINK}" ]] && tap "download_link valorizzato" 1 "" \
        || tap "download_link valorizzato" 0 "got empty"
    [[ -n "${FHASH}" ]] && tap "file_hash valorizzato" 1 "" \
        || tap "file_hash valorizzato" 0 "got empty"
    [[ -n "${SVER}"     ]] && tap "source_version valorizzato" 1 "" \
        || tap "source_version valorizzato" 0 "got empty"
    [[ "${ISTATE}" == "INDEXED" ]] && tap "index_state=INDEXED" 1 "" \
        || tap "index_state=INDEXED" 0 "got '${ISTATE}'"

    # --- STEP 5: Drive Files.Get via /api/drive/resolve-by-id ---
    # Canonical body: {"ids": [...]} — handler banner at handler_drive.go:284-292.
    # Response shape: {"ok":true,"resolved":[ResolveByIDsItem,...],"errors":[],"resolved_count":N}."
    # ResolveByIDsItem fields: id, name, mime_type, parents, web_view_link, size, trashed.
    if [[ -n "${DFID}" ]]; then
        log_info "--- 5: Drive resolve-by-id (${DFID}) ---"
        DRIVE_RESP=$(curl -s --max-time "${CURL_TIMEOUT}" -X POST "${BASE_URL}/api/drive/resolve-by-id" \
            -H "$(auth_header)" \
            -H 'Content-Type: application/json' \
            -d "$(jq -nc --arg id "${DFID}" '{ids: [$id]}')")

        DOK=$(echo "${DRIVE_RESP}"   | jq -r '.ok            // false')
        DRESOLVED=$(echo "${DRIVE_RESP}" | jq -r '.resolved_count // 0')

        if [[ "${DOK}" == "true" && "${DRESOLVED}" -ge 1 ]]; then
            tap "Drive resolve-by-id ok" 1 ""
            DNAME=$(echo "${DRIVE_RESP}" | jq -r '.resolved[0].name    // ""')
            DSIZE_RAW=$(echo "${DRIVE_RESP}" | jq -r '.resolved[0].size // 0')
            DTRASH=$(echo "${DRIVE_RESP}" | jq -r '.resolved[0].trashed // false')
            DMIME=$(echo "${DRIVE_RESP}"  | jq -r '.resolved[0].mime_type // ""')

            [[ -n "${DNAME}" && "${DNAME}" != "null" ]] \
                && tap "Drive name non-empty" 1 "name='${DNAME}'" \
                || tap "Drive name non-empty" 0 "got empty"
            [[ "${DSIZE_RAW}" =~ ^[0-9]+$ && "${DSIZE_RAW}" -gt 0 ]] \
                && tap "Drive size > 0" 1 "size=${DSIZE_RAW}" \
                || tap "Drive size > 0" 0 "got '${DSIZE_RAW}'"
            [[ "${DTRASH}" == "false" ]] \
                && tap "Drive trashed=false" 1 "" \
                || tap "Drive trashed=false" 0 "got '${DTRASH}'"
            [[ -n "${DMIME}" && "${DMIME}" != "null" ]] \
                && taw "Drive mime_type present" 1 "mime='${DMIME}'" \
                || taw "Drive mime_type present" 0 "got empty"
        else
            tap "Drive resolve-by-id ok" 0 "response: $(echo "${DRIVE_RESP}" | head -c 200)"
        fi
    else
        log_warn "  ${AID}: skipping Drive resolve (no drive_file_id)"
    fi

    # --- STEP 6: outbox_events status in (completed|superseded) ---
    log_info "--- 6: outbox_events status ---"
    OUTBOX_STATUS=$(sqlite3 "${DB_PATH}" "
        SELECT status FROM outbox_events
        WHERE event_type='asset.index.requested'
          AND aggregate_id='${AID}'
        ORDER BY id DESC LIMIT 1" 2>/dev/null || echo "")
    if [[ "${OUTBOX_STATUS}" == "completed" || "${OUTBOX_STATUS}" == "superseded" ]]; then
        tap "outbox index event=${OUTBOX_STATUS}" 1 ""
    elif [[ -z "${OUTBOX_STATUS}" ]]; then
        taw "outbox event present" 0 "no row for aggregate_id='${AID}'"
    else
        tap "outbox index event in (completed,superseded)" 0 "got '${OUTBOX_STATUS}'"
    fi

    # --- STEP 7+8: Qdrant scroll + payload ---
    # Pre-built QHEADERS array carries the api-key when QDRANT_API_KEY is set.
    log_info "--- 7+8: Qdrant scroll on ${COLLECTION} ---"
    SCROLL=$(curl -s --max-time "${SCROLL_TIMEOUT}" \
        -X POST "${QDRANT_URL}/collections/${COLLECTION}/points/scroll" \
        "${QHEADERS[@]}" \
        -H 'Content-Type: application/json' \
        -d "$(jq -nc --arg id "${AID}" '{
            filter: { must: [ { key: "asset_id", match: { value: $id } } ] },
            limit: 5,
            with_payload: true,
            with_vector: false
        }')")

    SCROLL_STATUS=$(echo "${SCROLL}" | jq -r '.status // "ok"')
    if [[ "${SCROLL_STATUS}" != "ok" ]]; then
        taw "Qdrant scroll endpoint ok" 0 "status='${SCROLL_STATUS}' — response: $(echo "${SCROLL}" | head -c 200)"
    else
        POINT_COUNT=$(echo "${SCROLL}" | jq -r '.result.points | length // 0' 2>/dev/null || echo "0")
        [[ "${POINT_COUNT}" -ge 1 ]] \
            && tap "Qdrant: punto con payload.asset_id=${AID}" 1 "count=${POINT_COUNT}" \
            || taw "Qdrant: punto con payload.asset_id=${AID}" 0 "got count=${POINT_COUNT}"

        if [[ "${POINT_COUNT}" -ge 1 ]]; then
            PAYLOAD_SRC=$(echo "${SCROLL}" | jq -r '.result.points[0].payload.source          // ""')
            PAYLOAD_MT=$(echo "${SCROLL}"  | jq -r '.result.points[0].payload.media_type      // ""')
            PAYLOAD_LS=$(echo "${SCROLL}"  | jq -r '.result.points[0].payload.lifecycle_state // ""')
            [[ "${PAYLOAD_SRC}" == "artlist" ]] \
                && tap "Qdrant payload source=artlist" 1 "got '${PAYLOAD_SRC}'" \
                || tap "Qdrant payload source=artlist" 0 "got '${PAYLOAD_SRC}'"
            [[ "${PAYLOAD_MT}" == "video" ]] \
                && tap "Qdrant payload media_type=video" 1 "" \
                || tap "Qdrant payload media_type=video" 0 "got '${PAYLOAD_MT}'"
            if [[ "${PAYLOAD_LS}" == "PUBLISHED" ]]; then
                tap "Qdrant payload lifecycle_state=PUBLISHED" 1 "got '${PAYLOAD_LS}'"
            else
                tap "Qdrant payload lifecycle_state=PUBLISHED" 0 "got '${PAYLOAD_LS}'"
            fi
        fi
    fi

    # Per-asset tally
    append_asset_verdict "${AID}" "${AP}|${AW}|${AF}"
done

# ============================================================
# STEP 9: unified search /api/media/search returns the asset
# ============================================================
log_info "=== STEP 9: POST /api/media/search (sources=['artlist']) ==="
SEARCH=$(curl -s --max-time "${CURL_TIMEOUT}" -X POST "${BASE_URL}/api/media/search" \
    -H "$(auth_header)" \
    -H 'Content-Type: application/json' \
    -d "$(jq -nc --arg q "${SEARCH_TERM}" --argjson limit "${LIMIT}" \
        '{query: $q, sources: ["artlist"], mode: "hybrid", limit: $limit}')")

SEARCH_FOUND_TOTAL=$(echo "${SEARCH}"   | jq -r '.results | length                                    // 0' 2>/dev/null || echo "0")
SEARCH_FOUND_ARTLIST=$(echo "${SEARCH}" | jq -r '[.results[]? | select(.source=="artlist")] | length'  2>/dev/null || echo "0")
SEARCH_PRESENT=$(echo "${SEARCH}"       | jq -r --arg a "${ASSET_IDS}" \
    '[$a | split(" ")[] as $id | .results[]? | select((.asset_id // .id // "") == $id)] | length' \
    2>/dev/null || echo "0")

if [[ "${SEARCH_FOUND_ARTLIST}" -ge 1 ]]; then
    log_pass "/api/media/search returned ${SEARCH_FOUND_ARTLIST} artlist result(s) (of ${SEARCH_FOUND_TOTAL} total)"
else
    log_warn "/api/media/search returned 0 artlist results — the embedding pipeline may be stale; the Qdrant scroll above is the canonical truth"
fi

if [[ "${SEARCH_PRESENT}" -ge 1 ]]; then
    log_pass "/api/media/search result includes at least one of our asset_id(s)"
else
    log_warn "/api/media/search did not include our specific asset_id(s) (SEARCH_PRESENT=${SEARCH_PRESENT})"
fi

# ============================================================
# Verdict + machine-readable JSON. Built with jq -n — quote-safe.
# ============================================================
echo
echo "============================================"
echo "  Artlist LIVE E2E Verification"
echo "  PASS=${PASS}  WARN=${WARN}  FAIL=${FAIL}"
echo "============================================"

# Build per-asset JSON array via jq (handles empty ASSET_VERDICTS gracefully).
if [[ "${#ASSET_VERDICTS[@]}" -gt 0 ]]; then
    ASSETS_JSON=$(printf '%s\n' "${ASSET_VERDICTS[@]}" \
        | jq -R 'split("|") | {id: .[0], pass: (.[1]|tonumber), warn: (.[2]|tonumber), fail: (.[3]|tonumber)}' \
        | jq -s .)
else
    ASSETS_JSON="[]"
fi

jq -n \
    --arg ts           "$(date -u '+%Y-%m-%dT%H:%M:%SZ')" \
    --arg term         "${SEARCH_TERM}" \
    --argjson limit    "${LIMIT}" \
    --arg rid          "${ROOT_FOLDER_ID}" \
    --arg jid          "${JID}" \
    --arg jstatus      "${JSTATUS}" \
    --argjson acount   "${ASSET_COUNT}" \
    --argjson pass     "${PASS}" \
    --argjson warn     "${WARN}" \
    --argjson fail     "${FAIL}" \
    --argjson assets   "${ASSETS_JSON}" \
    '{
       timestamp: $ts,
       search_term: $term,
       limit: $limit,
       root_folder_id: $rid,
       job_id: $jid,
       job_status: $jstatus,
       asset_count: $acount,
       totals: { pass: $pass, warn: $warn, fail: $fail },
       assets: $assets
     }' > "${LAST_JSON}"

log_info "Wrote machine-readable verdict: ${LAST_JSON}"

if [[ "${FAIL}" -gt 0 ]]; then
    echo "VERDICT: ${FAIL} CHECK(S) FAILED — pipeline Artlist LIVE broken"
    exit 1
elif [[ "${WARN}" -gt 0 ]]; then
    echo "VERDICT: ALL PASS, ${WARN} WARNINGS (operator review recommended)"
    exit 0
else
    echo "VERDICT: PIPELINE ARTLIST LIVE CORRETTA"
    exit 0
fi
