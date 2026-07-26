#!/usr/bin/env bash
# tests/operational/artlist/03_detail_stream.sh — Artlist DoD Gate 1 (POST /detail hard gate).
#
# Reorg (July 2026): split out of tests/operational/artlist/run_all.sh (now a thin shim).
# Two-phase acceptance probe (DoD, July 2026):
#
#   Happy path: ok=true + clip_id non-empty + page_url starts with
#               https://artlist.io/ + primary_url is m3u8/MP4 (or
#               /manifest|/playlist fallback) + primary_url != page_url +
#               stream_urls[] non-empty
#   Negative:   ok=false + error=="STREAM_NOT_FOUND" + stream_urls==[] +
#               clip_id non-empty
#
# Anything else → fail-closed (gate returns 1, battery aborts).
#
# Implementation choice (DoD refactor July 2026): POST goes through the
# lib helper `artlist_detail` (lib/velox_domain.sh) because:
#   (a) the helper is the single source of truth for the /detail contract,
#   (b) sharing lets Gate 10 Probe B reuse the same miss-phase probe without
#       duplicating jq constraints.
# Test clip_page_url for the happy path is sampled live: first hit from
# GET /api/artlist/search/live with LIVE_QUERIES[0] so the test always
# exercises a real, currently-routable Artlist URL.
#
# Collision-safe miss-path URL (fixup! July 2026): bad_page_url is 15
# digits long (000000999999999) — real Artlist clip ids run 6–9 digits;
# a 15-digit id is geometrically guaranteed to be a miss, defeating the
# "real clip id happens to be 00000000" false-positive risk identified
# in the first reviewer pass.

set -euo pipefail

DIR=$(cd "$(dirname "$0")" && pwd)
# shellcheck disable=SC1091
source "$DIR/../lib/_artlist_common.sh"



artlist_live_queries_validate && artlist_live_queries_default

smoke_require curl jq



# ── Gate 1 — POST /detail hard gate ─────────────────────────────────────
# Spec (July 2026 DoD):
#   happy:   ok=true + clip_id non-empty + page_url starts with https://artlist.io/ +
#            primary_url is m3u8/MP4 (or /manifest|/playlist fallback) +
#            primary_url != page_url + stream_urls[] non-empty
#   negative: ok=false + error=="STREAM_NOT_FOUND" + stream_urls==[] + clip_id non-empty
# Anything else → fail-closed (gate returns 1, battery aborts).
#
# Implementation choice (DoD refactor July 2026):
# Phase 1 (live search) stays inline (search-domain, not /detail-domain).
# Phase 2 (happy) + Phase 3 (miss) delegate to
# tests/operational/lib/velox_domain.sh::artlist_detail which
# encodes both contracts in jq -e and is the single source of truth.
# Gate 10 Probe B reuses the miss contract via a separate sub-battery call.
gate_detail_stream() {
    smoke_log_section "Gate 1 — POST /detail hard gate (STREAM_NOT_FOUND ok path)"
    local failures=0 transport_fail=0 contract_fail=0
    local real_page_url bad_page_url
    # fixup! July 2026: 15-digit id guaranteed non-existent; pre-fix 8-digit
    # 00000000 was colliding with a possible real clip id.
    bad_page_url="https://artlist.io/stock-footage/clip/000000999999999"

    # ── Phase 1: source a real clip_page_url from the live-search surface.
    # Migrated from inline smoke_curl to lib/artlist.sh::artlist_search_live
    # (DoD refactor July 2026). The helper applies the canonical /search/live
    # contract (provider + shape tuple + term round-trip) so the gate gets a
    # free filter against stub responses. On contract violation rc=1; on
    # transport failure rc=2. Body saved for forensic inspection.
    local probe_out="$WORK_DIR/gate1_probe.json"
    local rc=0
    artlist_search_live --term "${LIVE_QUERIES[0]}" --limit 5 \
        --save-body "$probe_out" || rc=$?
    if (( rc != 0 )); then
        log_fail "live search probe for /detail failed (rc=$rc; transport or contract violation; see $probe_out)"
        smoke_echo_safe "$(head -c 400 "$probe_out" 2>/dev/null || true)" >&2
        return 1
    fi
    real_page_url=$(jq -r '.clips[0].PageURL // .clips[0].page_url // empty' "$probe_out")
    if [[ -z "$real_page_url" || "$real_page_url" == "null" ]] \
       || ! [[ "$real_page_url" =~ ^https://artlist\.io/ ]]; then
        log_fail "first live clip PageURL invalid: '$real_page_url'"
        return 1
    fi

    # ── Phase 2: happy-path — delegate to artlist_detail.
    # Lib contract enforces: ok=true + .clip.ok==true + page_url startswith
    # artlist.io + playlist-shaped primary_url (\\.m3u8(\\?|$)|\\.mp4(\\?|$)
    # |/manifest|/playlist) + primary_url != "" + primary_url != page_url +
    # stream_urls[] non-empty + clip_id non-empty. Body saved to a
    # deterministic WORK_DIR file so forensic inspection after a mismatch
    # exposes the raw scraper response (smoke_echo_safe redacts tokens).
    local detail_ok="$WORK_DIR/gate1_detail_ok.json"
    local rc=0
    artlist_detail --phase happy --clip-page-url "$real_page_url" \
        --scraper-url "$SCRAPER_URL" --save-body "$detail_ok" || rc=$?
    case "$rc" in
        0)
            log_pass "/detail happy-path ok=true for $real_page_url"
            ;;
        2)
            log_fail "/detail transport/HTTP error (rc=2) for $real_page_url (see $detail_ok)"
            smoke_echo_safe "$(head -c 600 "$detail_ok" 2>/dev/null || true)" >&2
            transport_fail=$((transport_fail + 1))
            failures=$((failures + 1))
            ;;
        *)
            log_fail "/detail happy-path contract failed for $real_page_url"
            smoke_echo_safe "$(head -c 800 "$detail_ok" 2>/dev/null || true)" >&2
            contract_fail=$((contract_fail + 1))
            failures=$((failures + 1))
            ;;
    esac

    # ── Phase 3: miss-path STREAM_NOT_FOUND — delegate to artlist_detail.
    # Lib enforces: ok=false + .error=="STREAM_NOT_FOUND" + clip_id non-empty
    # + stream_urls[] EMPTY (the "no HTML saved as MP4" guard).
    local detail_snf="$WORK_DIR/gate1_detail_snf.json"
    rc=0
    artlist_detail --phase miss --clip-page-url "$bad_page_url" \
        --scraper-url "$SCRAPER_URL" --save-body "$detail_snf" || rc=$?
    case "$rc" in
        0)
            log_pass "/detail STREAM_NOT_FOUND ok=false for $bad_page_url"
            ;;
        2)
            log_fail "/detail transport/HTTP error (rc=2) for $bad_page_url (see $detail_snf)"
            smoke_echo_safe "$(head -c 600 "$detail_snf" 2>/dev/null || true)" >&2
            transport_fail=$((transport_fail + 1))
            failures=$((failures + 1))
            ;;
        *)
            log_fail "/detail STREAM_NOT_FOUND contract failed for $bad_page_url"
            smoke_echo_safe "$(head -c 800 "$detail_snf" 2>/dev/null || true)" >&2
            contract_fail=$((contract_fail + 1))
            failures=$((failures + 1))
            ;;
    esac

    if (( failures > 0 )); then
        log_fail "Gate 1 /detail hard gate failed (failures=${failures} transport=${transport_fail} contract=${contract_fail})"
        return 1
    fi
    log_pass "Gate 1 /detail hard gate clean (transport=0 contract=0)"
}

main() {
    if [[ "$DRY_RUN" == "1" ]]; then
        smoke_echo_safe "DRY RUN — /detail probes (Gate 1):"
        printf '  POST %s/detail (clip_page_url from LIVE_QUERIES[0], happy path)\n' "$SCRAPER_URL"
        printf '  POST %s/detail (clip_page_url=%s, negative STREAM_NOT_FOUND)\n' \
            "$SCRAPER_URL" "https://artlist.io/stock-footage/clip/000000999999999"
        exit 0
    fi
    gate_detail_stream || return 1

    printf '\n============================================\n'
    printf '  03_detail_stream\n'
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
