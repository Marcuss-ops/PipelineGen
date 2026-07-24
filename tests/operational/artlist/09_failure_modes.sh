#!/usr/bin/env bash
# tests/operational/artlist/09_failure_modes.sh — Artlist DoD Gates 10 + Restart (negative tests + restartability).
#
# Reorg (July 2026): split out of tests/operational/artlist_e2e.sh (now a thin shim).
# Bundles negative-path probes + restartability check:
#   Gate 10   — SESSION_EXPIRED + STREAM_NOT_FOUND + SCRAPER_UNAVAILABLE
#   Restart   — same term → same clip_ids + drive_file_id + file_hash,
#                PASS pre AND post restart (no manual intervention)
#
# Both are STUBS in the monolithic; the next PR implements them. This
# sub-script just declares the surface so make verify-artlist-errors has a
# parseable target.

set -euo pipefail

DIR=$(cd "$(dirname "$0")" && pwd)
# shellcheck disable=SC1091
source "$DIR/../lib/common.sh"
# shellcheck disable=SC1091
source "$DIR/../lib/artlist.sh"
# shellcheck disable=SC1091
source "$DIR/../lib/artlist_runtime.sh"



smoke_require curl jq



# ── Gate 10 — negative tests ────────────────────────────────────────────
# Spec (July 2026 DoD):
#   - SESSION_EXPIRED: POST /api/artlist/run with revoked token → error code
#   - STREAM_NOT_FOUND: POST /detail with bad clip_page_url → ok=false
#     (covered by Gate 1 Phase 3 implicitly; rerun here as standalone)
#   - SCRAPER_UNAVAILABLE: stop node-scraper, re-POST /detail → graceful
#     error not 5xx
gate_explicit_errors() {
    smoke_log_section "Gate 10 — negative tests"
    log_info "[STUB] Gate 10 — implement next (SESSION_EXPIRED + STREAM_NOT_FOUND + SCRAPER_UNAVAILABLE)"
}

# ── Restart — PASS pre/post restart ─────────────────────────────────────
# Spec (July 2026 DoD):
#   - Capture clip_ids, drive_file_id, file_hash from a successful Gate 4
#   - Restart PipelineGen + node-scraper
#   - Re-run the same term+limit
#   - response carries the SAME clip_ids, drive_file_id, file_hash
#   - Battery passes if pre AND post are identical (cache survives restart)
gate_restart() {
    smoke_log_section "Restart — PASS pre/post restart"
    log_info "[STUB] Restart — implement next"
}

main() {
    if [[ "$DRY_RUN" == "1" ]]; then
        smoke_echo_safe "DRY RUN — failure modes probes (Gates 10 + Restart):"
        printf '  SESSION_EXPIRED: POST %s/api/artlist/run with revoked token\n' "$BASE_URL"
        printf '  STREAM_NOT_FOUND: POST <scraper>/detail with bad clip_page_url (Gate 1 Phase 3 rerun)\n'
        printf '  SCRAPER_UNAVAILABLE: stop <scraper>, POST <scraper>/detail, expect graceful error\n'
        printf '  Restart: same term → same clip_ids + drive_file_id + file_hash post-restart\n'
        exit 0
    fi
    gate_explicit_errors || return 1
    gate_restart || return 1

    printf '\n============================================\n'
    printf '  09_failure_modes (Gates 10 + Restart)\n'
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
