#!/usr/bin/env bash
# tests/operational/artlist/05_pipeline_fresh.sh — Artlist DoD Gates 4 + 5 (fresh run 3/3 + per-clip validation).
#
# Reorg (July 2026): split out of tests/operational/artlist_e2e.sh (now a thin shim).
# Bundles two gates that exercise the same operational surface (an end-to-end
# /api/artlist/run cycle):
#   Gate 4 — first fresh run (3/3 SUCCEEDED, failed=0, no RETRY_WAIT)
#   Gate 5 — per-clip DB + local file validation
#
# Both gates are currently STUBS in the monolithic; the next PR implements
# them via lib/artlist.sh::artlist_enqueue_run + artlist_poll_run, then walks
# the resulting clip_ids through lib/velox_domain.sh::velox_artlist_pipeline_run
# for Gate 5. This sub-script just declares the surface so make verify-artlist-pipeline
# has a parseable target.

set -euo pipefail

DIR=$(cd "$(dirname "$0")" && pwd)
# shellcheck disable=SC1091
source "$DIR/../lib/common.sh"
# shellcheck disable=SC1091
source "$DIR/../lib/artlist.sh"
# Future: source lib/velox_domain.sh once Gate 5 implementation lands.

# Per-battery runtime configuration (full set — needed once stubs land).
HOST="${VELOX_HOST:-127.0.0.1}"
PIPELINE_PORT="${PIPELINE_PORT:-${VELOX_PORT:-8000}}"
BASE_URL="http://${HOST}:${PIPELINE_PORT}"
DB_PATH="${VELOX_DATA_DIR:-./data}/media/media.db.sqlite"
ARTLIST_TERM="${ARTLIST_TERM:-business team working in modern office}"
ARTLIST_LIMIT="${ARTLIST_LIMIT:-3}"

smoke_require curl jq

# Per-battery counters
PASS=0; WARN=0; FAIL=0
log_pass() { printf '[PASS]  %s %s\n' "$(date '+%H:%M:%S')" "$*"; PASS=$((PASS + 1)); }
log_warn() { printf '[WARN]  %s %s\n' "$(date '+%H:%M:%S')" "$*"; WARN=$((WARN + 1)); }
log_fail() { printf '[FAIL]  %s %s\n' "$(date '+%H:%M:%S')" "$*"; FAIL=$((FAIL + 1)); }
log_info() { printf '[INFO]  %s %s\n' "$(date '+%H:%M:%S')" "$*"; }

# ── Gate 4 — first fresh run 3/3 ────────────────────────────────────────
# Spec (July 2026 DoD):
#   - POST /api/artlist/run with term+limit=3, walks the resulting run_id
#   - poll until terminal via lib/artlist.sh::artlist_poll_run
#   - 3/3 SUCCEEDED, failed=0
#   - no RETRY_WAIT entries in the jobs ledger
#   - per-clip clip_ids are non-empty, unique, and on artlist.io
gate_fresh_run_three() {
    smoke_log_section "Gate 4 — first fresh run 3/3"
    log_info "[STUB] Gate 4 — implement next"
}

# ── Gate 5 — per-clip DB + file validation ──────────────────────────────
# Spec (July 2026 DoD):
#   - for each clip_id from Gate 4:
#       sqlite3 read on $DB_PATH for SELECT * FROM assets WHERE id = clip_id
#       file exists at the assets.local_path
#       file size > 0
#       local file MIME == video/mp4 (sample first clip, optional)
gate_per_clip_validation() {
    smoke_log_section "Gate 5 — per-clip DB + file validation"
    log_info "[STUB] Gate 5 — implement next"
}

main() {
    if [[ "$DRY_RUN" == "1" ]]; then
        smoke_echo_safe "DRY RUN — pipeline fresh probes (Gates 4 + 5):"
        printf '  POST %s/api/artlist/run term=<ARTLIST_TERM> limit=3 (Gate 4)\n' "$BASE_URL"
        printf '  poll run_id until terminal (Gate 4)\n'
        printf '  SELECT * FROM assets WHERE id IN (Gate 5)\n'
        exit 0
    fi
    gate_fresh_run_three || return 1
    gate_per_clip_validation || return 1

    printf '\n============================================\n'
    printf '  05_pipeline_fresh (Gates 4 + 5)\n'
    printf '  PASS=%d  WARN=%d  FAIL=%d\n' "$PASS" "$WARN" "$FAIL"
    printf '============================================\n'
    if [[ "$FAIL" -gt 0 ]]; then
        printf 'VERDICT: FAIL\n'
        return 1
    fi
    printf 'VERDICT: PASS\n'
    return 0
}

main "$@"
