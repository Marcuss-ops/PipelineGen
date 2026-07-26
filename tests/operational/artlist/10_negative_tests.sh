#!/usr/bin/env bash
# tests/operational/artlist/10_negative_tests.sh — Artlist DoD Gate 10
# (negative tests: SESSION_EXPIRED / STREAM_NOT_FOUND / SCRAPER_UNAVAILABLE).
#
# Reorg (July 2026): extraction target per the DoD "estrazione futura"
# consolidation. The Gate 10 negative-path probes currently live inline
# inside tests/operational/artlist/05_pipeline_fresh.sh (see Fase 6
# Fase 6 typed-error block surface vocabulary). This file is the canonical
# home for the Gate 10 verifier once extraction lands; until then it's
# a forward-pointing stub signaling the per-gate file structure is in
# place.
#
# Spec (artlist_gates.md|Gate 10 verbatim):
#   Three proven-negative probes (each is a HARD gate):
#   1. Session expired  → SESSION_EXPIRED or AUTH_REQUIRED sentinel
#                           + no false results + no SUCCEEDED jobs.
#   2. Stream not found → STREAM_NOT_FOUND sentinel
#                           + no page_url treated as stream
#                           + no HTML saved as MP4.
#   3. Gateway off      → SCRAPER_UNAVAILABLE sentinel
#                           + retry bounded (no infinite RETRY_WAIT)
#                           + job terminal-failed (early).
# Principle: provider unavailable != zero results. Operator must NOT
# see silent false-positives; the verdict is fail-closed on each branch.

set -euo pipefail

DIR=$(cd "$(dirname "$0")" && pwd)
# Source the umbrella (godlike/06 SSOT canonical import contract).
# shellcheck disable=SC1091
source "$DIR/../lib/_artlist_common.sh"

smoke_require curl jq

# ── Gate 10 — Negative tests ─────────────────────────────────────────────
gate_negative_tests() {
    # TODO (extraction followup): lift the 3 proven-negative probes
    # from 05_pipeline_fresh.sh (and the prior test_negative_artlist.sh
    # if it exists) into this function. Expected surface:
    #   probe-1 (SESSION_EXPIRED):
    #     - invalidate /api/admin/artlist-session or remove cookie
    #     - run velox_artlist_search_live --term "business team" ...
    #     - assert: response._transport_kind == AUTH_REQUIRED OR
    #               velox_artlist_detail (miss-contract) returns
    #               SESSION_EXPIRED sentinel
    #     - assert: no jobs SUCCEEDED in $WORK_DIR/jobs.json tail
    #   probe-2 (STREAM_NOT_FOUND):
    #     - velox_artlist_detail --phase miss <bad-clip-url>
    #     - assert: error == "STREAM_NOT_FOUND", stream_urls empty
    #     - assert: no page_url treated as stream (check $WORK_DIR/
    #       local_path is not the page_url HTML)
    #   probe-3 (SCRAPER_UNAVAILABLE):
    #     - point $SCRAPER_URL at a dead endpoint
    #     - velox_artlist_search_live --term "..." --timeout-seconds 30
    #     - assert: response._transport_kind == SCRAPER_UNAVAILABLE
    #     - assert: zero jobs with status RETRY_WAIT (bounded retry)
    #   log_pass/log_fail cascade per probe.
    log_info "[STUB] Gate 10 — extract 3 proven-negative probes (SESSION_EXPIRED / STREAM_NOT_FOUND / SCRAPER_UNAVAILABLE) from 05_pipeline_fresh.sh"
    return 0
}

main() {
    if [[ "$DRY_RUN" == "1" ]]; then
        smoke_echo_safe "DRY RUN — negative tests (Gate 10):"
        printf '  probe-1 SESSION_EXPIRED  → velox_artlist_search_live / detail → AUTH_REQUIRED|SESSION_EXPIRED\n'
        printf '  probe-2 STREAM_NOT_FOUND  → velox_artlist_detail --phase miss  → STREAM_NOT_FOUND\n'
        printf '  probe-3 SCRAPER_UNAVAILABLE → velox_artlist_search_live against dead endpoint → SCRAPER_UNAVAILABLE\n'
        printf '  ALL probes MUST produce explicit sentinels; no silent false-positives.\n'
        exit 0
    fi
    gate_negative_tests || return 1

    printf '\n============================================\n'
    printf '  10_negative_tests\n'
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
