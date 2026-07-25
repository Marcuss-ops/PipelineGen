#!/usr/bin/env bash
# tests/operational/lib/artlist_runtime.sh — Artlist-battery runtime config + counters.
#
# Source-able library. Every arlist/*.sh sub-script does:
#   DIR=$(cd "$(dirname "$0")" && pwd)
#   # shellcheck disable=SC1091
#   source "$DIR/../lib/common.sh"
#   # shellcheck disable=SC1091
#   source "$DIR/../lib/artlist.sh"
#   # shellcheck disable=SC1091
#   source "$DIR/../lib/artlist_runtime.sh"
#
# Contract:
#   - Exposes HOST / PIPELINE_PORT / BASE_URL / DB_PATH / SCRAPER_URL /
#     QDRANT_URL / QDRANT_COLLECTION / ARTLIST_ROOT_FOLDER / ARTLIST_TERM
#     (all env-overridable; canonical DoD defaults).
#   - Exposes PASS / WARN / FAIL counters + log_pass / log_warn /
#     log_fail / log_info helpers (verbatim-identical across every battery).
#
# Environment variables (all overridable; defaults shown):
#   VELOX_HOST                            host (default 127.0.0.1)
#   VELOX_PORT                            pipeline port (default 8000)
#   PIPELINE_PORT                         port, higher precedence than VELOX_PORT
#   VELOX_DATA_DIR                        parent of media.db.sqlite (default ./data)
#   VELOX_ARTLIST_SCRAPER_SERVER_URL      scraper base URL (default http://127.0.0.1:9123)
#   QDRANT_URL                            Qdrant base URL (default http://127.0.0.1:6333)
#   QDRANT_COLLECTION                     Qdrant collection (default media_assets_current)
#   VELOX_DRIVE_ARTLIST_ROOT              Artlist Drive root folder (no default)
#   ROOT_FOLDER_ID                        alt source for VELOX_DRIVE_ARTLIST_ROOT
#   ARTLIST_TERM                          default search term (default "business …").
#
# DoD refactor (July 2026, post-verify-* split): replaces ~225 lines of
# copy-pasted boilerplate across 9 arlist sub-scripts + run_all.sh with
# a single ~60-line canonical source.

set -euo pipefail

# ── Runtime config (env-overridable, canonical DoD defaults) ──────────────
HOST="${VELOX_HOST:-127.0.0.1}"
PIPELINE_PORT="${PIPELINE_PORT:-${VELOX_PORT:-8000}}"
BASE_URL="http://${HOST}:${PIPELINE_PORT}"
DB_PATH="${VELOX_DATA_DIR:-./data}/media/media.db.sqlite"
SCRAPER_URL="${VELOX_ARTLIST_SCRAPER_SERVER_URL:-http://127.0.0.1:9123}"
QDRANT_URL="${QDRANT_URL:-http://127.0.0.1:6333}"
QDRANT_COLLECTION="${QDRANT_COLLECTION:-media_assets_current}"
ARTLIST_ROOT_FOLDER="${VELOX_DRIVE_ARTLIST_ROOT:-${ROOT_FOLDER_ID:-}}"
ARTLIST_TERM="${ARTLIST_TERM:-business team working in modern office}"
export HOST BASE_URL DB_PATH SCRAPER_URL \
       QDRANT_URL QDRANT_COLLECTION \
       ARTLIST_ROOT_FOLDER ARTLIST_TERM PIPELINE_PORT

# ── Artlist DoD fingerprint run-id (Gate 0 no-manual-intervention) ─────
# UTC timestamp that fingerprints a single battery run. Exported so
# 01_startup.sh (save) and run_all.sh (verify) anchor under the same
# per-run directory keyed by this id. Anchor path:
#   ${VELOX_DATA_DIR:-./data}/.artlist_dod_fingerprint/${ARTLIST_DOD_RUN_ID}/
# Smoke_no_tampering_save + smoke_no_tampering_verify in lib/common.sh
# honour ${ARTLIST_DOD_FP_DIR} if already exported (test harness override);
# fall back to the canonical computed path otherwise.
ARTLIST_DOD_RUN_ID="${ARTLIST_DOD_RUN_ID:-$(date -u +%Y%m%dT%H%M%SZ 2>/dev/null || date -u +%Y%m%dT%H%M%S)}"
export ARTLIST_DOD_RUN_ID

# ── WORK_DIR defensive default (exported for downstream stub libs) ───────
# DRY_RUN path + stub paths in artlist.sh / sqlite.sh / drive.sh /
# qdrant.sh read $WORK_DIR for last.body / artifact paths. Defaulting
# here keeps the stub bodies small (no per-call ${WORK_DIR:-/tmp} guard)
# while still letting an operator-supplied WORK_DIR take precedence.
: "${WORK_DIR:=$PWD/.tmp}"
export WORK_DIR

# ── Per-battery counters + log_* helpers (verbatim-identical per battery)
# Latent footguards (read carefully before refactoring):
#   (a) do NOT declare PASS/WARN/FAIL local inside any sub-script or log_*
#       writes silently break — log_pass/log_warn/log_fail increment the
#       globally-sourced counters, NOT a captured local; `local PASS=0`
#       in a sub-script shadows the lib's PASS and the log_* helpers
#       would still bump the shadowed one (visible, silently divergent).
#   (b) PASS/WARN/FAIL are NOT exported on purpose; bumps inside $(…)
#       subshells wouldn't propagate to the parent shell anyway, so
#       exporting would be dead noise. Keep them script-local; this
#       matches the original inline definition every sub-script
#       copy-pasted before the runtime.sh refactor.
PASS=0; WARN=0; FAIL=0

log_pass() { printf '[PASS]  %s %s\n' "$(date '+%H:%M:%S')" "$*"; PASS=$((PASS + 1)); }
log_warn() { printf '[WARN]  %s %s\n' "$(date '+%H:%M:%S')" "$*"; WARN=$((WARN + 1)); }
log_fail() { printf '[FAIL]  %s %s\n' "$(date '+%H:%M:%S')" "$*"; FAIL=$((FAIL + 1)); }
log_info() { printf '[INFO]  %s %s\n' "$(date '+%H:%M:%S')" "$*"; }

# ── LIVE_QUERIES env-override (defaults + validation) ───────────────────
# Every arlist sub-script that gates on a search-term triplet does:
#   artlist_live_queries_validate && artlist_live_queries_default
# Operator contract:
#   - LIVE_QUERIES env (pipe-delimited, exactly 3 non-empty slots)
#     takes precedence; if malformed, validate emits [FAIL] to stderr,
#     writes $WORK_DIR/live_queries_validation_failed.json, and exit 2
#     (canonical setup-error per lib/common.sh header convention).
#   - LIVE_QUERY_1/2/3 env vars are accepted as a 3-tuple fallback
#     (no validation; assume operator knows the catalog).
#   - Else: canonical English 3-term DoD defaults.
# LIVE_QUERIES array is set as a side-effect per the above chain.
# Source-order requirement: this section MUST be sourced BEFORE the
# sub-script references ${LIVE_QUERIES[@]}.

# artlist_live_queries_validate() — validates $LIVE_QUERIES env override.
# If unset: no-op (return 0). If set + valid: parses pipe-delimited into
# LIVE_QUERIES array. If set + invalid: emits [FAIL] + artifact +
# exit 2 (fail-closed per AGENTS.md). Per-slot env-vars + hardcoded
# fallback are handled by artlist_live_queries_default(); this function
# only validates the LIVE_QUERIES env override.
#
# Note: `IFS='|' read -ra` reverts IFS after read returns, so the
# downstream ${LIVE_QUERIES[*]} expansion joins by the ORIGINAL IFS
# (space by default in the caller scope), preserving the human-readable
# FAIL-line output the monolithic relied on.
artlist_live_queries_validate() {
    if [[ -z "${LIVE_QUERIES+x}" ]]; then
        return 0
    fi
    IFS='|' read -ra LIVE_QUERIES <<<"${LIVE_QUERIES}"
    if [[ ${#LIVE_QUERIES[@]} -ne 3 \
       || -z "${LIVE_QUERIES[0]:-}" \
       || -z "${LIVE_QUERIES[1]:-}" \
       || -z "${LIVE_QUERIES[2]:-}" ]]; then
        local ts
        ts="$(date '+%Y-%m-%dT%H:%M:%S')"
        printf >&2 '[FAIL]  %s  LIVE_QUERIES env override must yield exactly 3 non-empty pipe-delimited terms; got %d slot(s): "%s"\n' \
            "$ts" "${#LIVE_QUERIES[@]}" "${LIVE_QUERIES[*]}"
        # Honors $TMPDIR so macOS / systemd-private-tmp / CI sandboxes
        # land the artifact under the operator's tmpdir. mkdir is
        # fail-safe so a read-only /tmp does not mask the canonical
        # exit-2 code (under set -e, mkdir failure would otherwise
        # abort with exit 1 and lose the setup-error classification).
        : "${WORK_DIR:=${TMPDIR:-/tmp}/artlist_e2e_validation}"
        export WORK_DIR
        if ! mkdir -p "$WORK_DIR" 2>/dev/null; then
            printf >&2 '[WARN]  %s  could not mkdir %s (validation artifact skipped)\n' \
                "$ts" "$WORK_DIR"
            exit 2
        fi
        # Build the value array length-N faithfully: pipe the bash array
        # via NUL separator (canonical CSV null-record delimiter per
        # IEEE Std 1003.1), slurp + split + map empty-to-null + slice
        # to ${#LIVE_QUERIES[@]} so the trailing empty from printf's
        # terminal NUL does NOT inflate the count. --argjson value
        # carries the full JSON array regardless of operator-supplied N,
        # so the artifact faithfully reports {slots:N, value:[s0, ..., sN-1]}.
        # Fail-safe wrapper around the jq pipeline: under set -euo
        # pipefail the value_json=$(...) compound would otherwise mask
        # the canonical exit-2 classification if jq exits non-zero.
        local value_json=""
        if ! value_json=$(printf '%s\0' "${LIVE_QUERIES[@]}" | jq -Rs --argjson n "${#LIVE_QUERIES[@]}" \
            'split("\u0000") | map(if . == "" then null else . end) | .[:$n]'); then
            printf >&2 '[WARN]  %s  jq pipeline failed producing the value array (artifact dropped, exit 2 still enforced)\n' \
                "$ts"
            exit 2
        fi
        jq -nc --arg ts "$ts" --argjson slots "${#LIVE_QUERIES[@]}" \
            --argjson value "$value_json" \
            '{event:"live_queries_validation_failed",ts:$ts,slots:$slots,value:$value}' \
            > "$WORK_DIR/live_queries_validation_failed.json"
        # exit 2 (canonical setup-error exit code per lib/common.sh header)
        # hard-terminates the run.
        exit 2
    fi
}

# artlist_live_queries_default() — fallback resolver for the LIVE_QUERIES
# array. Picks from, in priority order (only if validate did not already
# populate the array):
#   1. LIVE_QUERY_1 + LIVE_QUERY_2 + LIVE_QUERY_3 env vars (no validation)
#   2. canonical English 3-term DoD defaults
#                   (verified Artlist returns hits against /search/live)
# Idempotent: skip if LIVE_QUERIES array already populated (by validate or
# upstream caller). Also unsets LIVE_QUERY_1..3 to prevent downstream
# leakage into unrelated env-dump operations.
artlist_live_queries_default() {
    # Bash-compatible array-length check. `${#LIVE_QUERIES[@]:-0}` would
    # be terser but bash rejects it with `bad substitution` (only
    # ksh93/zsh honour `:-` after array-length expansion). The
    # unset-guard + explicit count dance is the canonical replacement
    # and tolerates LIVE_QUERIES never being populated (e.g. when the
    # caller never exported it nor invoked validate first).
    if [[ -n "${LIVE_QUERIES+x}" && ${#LIVE_QUERIES[@]} -gt 0 ]]; then
        return 0
    fi
    if [[ -n "${LIVE_QUERY_1:-}" && -n "${LIVE_QUERY_2:-}" && -n "${LIVE_QUERY_3:-}" ]]; then
        LIVE_QUERIES=("${LIVE_QUERY_1}" "${LIVE_QUERY_2}" "${LIVE_QUERY_3}")
    else
        LIVE_QUERIES=(
            "business team working in modern office"
            "heavyweight boxer training in gym"
            "boxing arena crowd celebrating"
        )
    fi
    unset LIVE_QUERY_1 LIVE_QUERY_2 LIVE_QUERY_3
}
