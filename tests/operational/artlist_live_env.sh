#!/usr/bin/env bash
# artlist_live_env.sh — env phase for artlist_live_e2e_verify.sh
#
# Sourced by the main shim BEFORE prep/run/assert/teardown. Defines:
#   - usage()         — heredoc of --help output
#   - CLI arg parser  — `--dry-run`, `--help`, `--` end-of-args
#   - Config block    — env-var defaults for BASE_URL / SCRAPER_URL /
#                        QDRANT_URL / DB_PATH / TOKEN / ROOT_FOLDER_ID /
#                        SEARCH_TERM / LIMIT / POLL_INTERVAL / POLL_MAX /
#                        SCROLL_TIMEOUT / SCRAPER_CONNECT_TIMEOUT_SECONDS /
#                        CURL_TIMEOUT / LAST_JSON / EXPECTED_GATE_MATCHES /
#                        COLLECTION / QDRANT_API_KEY
#   - Tally counters  — PASS / WARN / FAIL (mutated by log_* helpers below)
#   - Asset verdicts  — ASSET_VERDICTS bash array (mutated by append_asset_verdict)
#   - Helpers         — log_info / log_pass / log_warn / log_fail (mutate
#                        PASS/WARN/FAIL counters via PASS=$((PASS+1)) etc.);
#                        auth_header() for the admin bearer token;
#                        append_asset_verdict() pushes per-asset tap/taw
#                        counters into ASSET_VERDICTS; require_tool()
#                        fail-closed helper that exits 2 if a tool is
#                        missing from PATH
#   - DRY_RUN block   — early-exit (exit 0) if DRY_RUN=1 OR --dry-run
#                        CLI arg, BEFORE the preflight fail-closed phase.
#                        The heredoc prints the 9-point plan + optional
#                        light read-only preflight probes (server /ready,
#                        scraper /health, Qdrant /collections, sqlite
#                        presence) when TOKEN is set.
#
# Source-only guard: this file MUST NOT be executed directly. The guard
# below ensures direct-`bash artlist_live_env.sh` callers fail with a
# clear error rather than executing setup without the main shim's
# `set -euo pipefail` + tally wiring.
#
# Cross-phase invariants: PASS / WARN / FAIL / ASSET_VERDICTS / TOKEN /
# JID / ASSET_IDS / LAST_JSON must survive from env → prep → run →
# assert → teardown. Function definitions precede first use (env is
# sourced first, so log_* are available everywhere).

if [[ "${BASH_SOURCE[0]}" == "${0}" ]]; then
    echo "[ERROR] artlist_live_env.sh must be sourced, not executed directly." >&2
    echo "[ERROR] Use the main shim: bash tests/operational/artlist_live_e2e_verify.sh" >&2
    exit 1
fi

# ============================================================
# Usage (heredoc) — surfaced via --help / -h
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
  VELOX_ARTLIST_SCRAPER_SERVER_URL Node-scraper URL (PR-ARTLIST-CONFIG-PREFIX July 2026: was ARTLIST_SCRAPER_SERVER_URL)
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

# ============================================================
# CLI arg parser
# ============================================================
# Unknown positional / unknown flag → usage to stderr + exit 2.
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
# Config block (env-driven defaults)
# ============================================================
HOST="${VELOX_HOST:-127.0.0.1}"
# PIPELINE_PORT — canonical port for PipelineGen's HTTP listener.
# Source of truth: config.example.yaml `server.port` (default 8000 per
# Operational Readiness PR, June 2026). Backward-compat VELOX_PORT is
# preserved for sibling-script consistency.
[ -n "${PIPELINE_PORT:-}" ] || PIPELINE_PORT="${VELOX_PORT:-8000}"
BASE_URL="http://${HOST}:${PIPELINE_PORT}"

SCRAPER_URL="${VELOX_ARTLIST_SCRAPER_SERVER_URL:-http://127.0.0.1:9123}"
QDRANT_URL="${QDRANT_URL:-http://127.0.0.1:6333}"
QDRANT_API_KEY="${QDRANT_API_KEY:-${VELOX_QDRANT_API_KEY:-}}"
COLLECTION="${QDRANT_COLLECTION:-media_assets_current}"

DB_PATH="${VELOX_DATA_DIR:-./data}/media/media.db.sqlite"
TOKEN="${VELOX_ADMIN_TOKEN:-}"
ROOT_FOLDER_ID="${ROOT_FOLDER_ID:-${VELOX_DRIVE_ARTLIST_ROOT:-}}"
SEARCH_TERM="${SEARCH_TERM:-boxing training}"
LIMIT="${LIMIT:-1}"

POLL_INTERVAL="${POLL_INTERVAL:-10}"
POLL_MAX="${POLL_MAX:-18}"                            # 18 * 10s = 180s job wait
# Per fix(scraper) PR + docs/operations/stock-e2e-runbook.md §11.0:
#   SCROLL_TIMEOUT = scraper total budget (was 10s, raised to 120s).
#   SCRAPER_CONNECT_TIMEOUT_SECONDS = connect budget (Chromium cold start + first nav).
SCROLL_TIMEOUT="${SCROLL_TIMEOUT:-120}"                          # scraper total budget
SCRAPER_CONNECT_TIMEOUT_SECONDS="${SCRAPER_CONNECT_TIMEOUT_SECONDS:-5}"  # connect budget
CURL_TIMEOUT="${CURL_TIMEOUT:-30}"

LAST_JSON="${LAST_JSON:-/tmp/artlist_live_e2e_last_run.json}"

# EXPECTED_GATE_MATCHES — expected TestGateXX_* function-name count
# from the meta-anchor's expectedGateTests array in
# gate11_scraper_failure_test.go (default 28). Validated as a
# positive integer at use time (see HERMETIC GATES precondition
# block in prep.sh).
EXPECTED_GATE_MATCHES="${EXPECTED_GATE_MATCHES:-28}"

# ============================================================
# Tally + asset-verdicts state
# ============================================================
PASS=0
WARN=0
FAIL=0
ASSET_VERDICTS=()  # per-asset pass/warn/fail strings (id|pass|warn|fail)

# ============================================================
# Tally helpers (mutate PASS/WARN/FAIL)
# ============================================================
log_info() { echo "[INFO]  $(date '+%H:%M:%S') $*"; }
log_pass() { echo "[PASS]  $(date '+%H:%M:%S') $*"; PASS=$((PASS + 1)); }
log_warn() { echo "[WARN]  $(date '+%H:%M:%S') $*"; WARN=$((WARN + 1)); }
log_fail() { echo "[FAIL]  $(date '+%H:%M:%S') $*"; FAIL=$((FAIL + 1)); }

auth_header() { echo "X-Velox-Admin-Token: ${TOKEN}"; }

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
# DRY_RUN: print plan, no real cycle (early-exit BEFORE prep)
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
