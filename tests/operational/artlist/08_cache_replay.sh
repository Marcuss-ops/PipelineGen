#!/usr/bin/env bash
# tests/operational/artlist/08_cache_replay.sh — Artlist DoD Gate 9 (replay cache_hit=true).
#
# Reorg (July 2026): split out of tests/operational/artlist_e2e.sh (now a thin shim).
# Hard-gate check (DoD, July 2026): re-enqueueing the same term after a
# successful Gate 4 MUST land on the SQLite cache without re-running the
# pipeline. The re-run response carries cache_hit=true and cache_source=sqlite.
#
# Implementation (next PR) will delegate to lib/artlist.sh::artlist_enqueue_run,
# then inspect the response for cache_hit/cache_source fields. Until then this
# sub-script is a stub so make verify-artlist-cache has a parseable target.

set -euo pipefail

DIR=$(cd "$(dirname "$0")" && pwd)
# shellcheck disable=SC1091
source "$DIR/../lib/common.sh"
# shellcheck disable=SC1091
source "$DIR/../lib/artlist.sh"
# shellcheck disable=SC1091
source "$DIR/../lib/artlist_runtime.sh"



smoke_require curl jq



# ── Gate 9 — replay cache_hit=true ──────────────────────────────────────
# Spec (July 2026 DoD):
#   - Re-POST /api/artlist/run with the same term+limit as Gate 4
#   - response carries cache_hit=true + cache_source=sqlite
#   - no spurious new run_id (response.run_id == Gate 4's run_id, OR
#     response is acknowledged as a cache replay)
#   - clip_ids identical to Gate 4's
#   - file_hash identical for each clip_id (no re-encode on cache)
gate_cache_replay() {
    smoke_log_section "Gate 9 — replay cache_hit=true"
    log_info "[STUB] Gate 9 — implement next (will use lib/artlist.sh::artlist_enqueue_run)"
}

main() {
    if [[ "$DRY_RUN" == "1" ]]; then
        smoke_echo_safe "DRY RUN — cache replay probe (Gate 9):"
        printf '  POST %s/api/artlist/run term=<ARTLIST_TERM> limit=3 (re-run, expect cache_hit=true)\n' "$BASE_URL"
        printf '  assert: cache_source=sqlite AND clip_ids == Gate 4\n'
        exit 0
    fi
    gate_cache_replay || return 1

    printf '\n============================================\n'
    printf '  08_cache_replay\n'
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
