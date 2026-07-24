#!/usr/bin/env bash
# tests/operational/artlist/03_detail_stream.sh — Artlist DoD Gate 1 (POST /detail hard gate).
#
# Reorg (July 2026): split out of tests/operational/artlist_e2e.sh (now a thin shim).
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
# Implementation choice (DoD refactor July 2026): POST goes directly to
# the node-scraper endpoint $SCRAPER_URL/detail because:
#   (a) the scraper is the source-of-truth for STREAM_NOT_FOUND semantics, and
#   (b) hitting the Go server's forwarding layer would mask scraper errors.
# Test clip_page_url for the happy path is sampled live: first hit from
# GET /api/artlist/search/live with LIVE_QUERIES[0] so the test always
# exercises a real, currently-routable Artlist URL.

set -euo pipefail

DIR=$(cd "$(dirname "$0")" && pwd)
# shellcheck disable=SC1091
source "$DIR/../lib/common.sh"
# shellcheck disable=SC1091
source "$DIR/../lib/artlist.sh"
# shellcheck disable=SC1091
source "$DIR/../lib/artlist_runtime.sh"



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
# POST goes directly to the node-scraper endpoint $SCRAPER_URL/detail because:
#   (a) the scraper is the source-of-truth for STREAM_NOT_FOUND semantics, and
#   (b) hitting the Go server's forwarding layer would mask scraper errors.
# Test clip_page_url for the happy path is sampled live: first hit from
# GET /api/artlist/search/live with LIVE_QUERIES[0] so the test always
# exercises a real, currently-routable Artlist URL.
# Future refactor (post-reorg): happy-path probe delegates to
# lib/artlist.sh::artlist_detail; negative-path probe stays inline (lib has no
# STREAM_NOT_FOUND helper yet).
gate_detail_stream() {
    smoke_log_section "Gate 1 — POST /detail hard gate (STREAM_NOT_FOUND ok path)"
    local failures=0
    local real_page_url bad_page_url
    bad_page_url="https://artlist.io/stock-footage/clip/00000000"

    # ── Phase 1: source a real clip_page_url from the live-search surface
    smoke_curl GET "/api/artlist/search/live?term=${LIVE_QUERIES[0]}&limit=5" >/dev/null
    if [[ ! "${SMOKE_LAST_HTTP:-}" =~ ^2[0-9][0-9]$ ]] \
       || ! jq -e '.clips // [] | length > 0' "${SMOKE_LAST_BODY:-/dev/null}" >/dev/null 2>&1; then
        log_fail "live search probe for /detail failed (HTTP=${SMOKE_LAST_HTTP:-empty})"
        return 1
    fi
    real_page_url=$(jq -r '.clips[0].PageURL // empty' "${SMOKE_LAST_BODY:-/dev/null}")
    if [[ -z "$real_page_url" || "$real_page_url" == "null" ]] \
       || ! [[ "$real_page_url" =~ ^https://artlist\.io/ ]]; then
        log_fail "first live clip PageURL invalid: '$real_page_url'"
        return 1
    fi

    # ── Phase 2: happy-path POST /detail
    local detail_ok="$WORK_DIR/gate1_detail_ok.json"
    local code
    code=$(curl -sS --max-time "$SMOKE_HTTP_TIMEOUT_SECONDS" \
        -X POST -H 'Content-Type: application/json' \
        -d "$(jq -nc --arg u "$real_page_url" '{clip_page_url:$u}')" \
        "$SCRAPER_URL/detail" -o "$detail_ok" -w '%{http_code}' 2>/dev/null || echo 000)

    if [[ ! "$code" =~ ^2[0-9][0-9]$ ]]; then
        log_fail "POST /detail HTTP=$code (expected 2xx) for $real_page_url"
        smoke_echo_safe "$(head -c 600 "$detail_ok" 2>/dev/null || true)" >&2
        failures=$((failures + 1))
    elif ! jq -e '.ok == true
        and (.clip.ok // false) == true
        and ((.clip.page_url // .page_url // "") | startswith("https://artlist.io/"))
        and ((.clip.primary_url // .primary_url // "") | test("\\.m3u8(\\?|$)|\\.mp4(\\?|$)|/manifest|/playlist"))
        and ((.clip.primary_url // .primary_url // "") != "")
        and ((.clip.primary_url // .primary_url // "") != (.clip.page_url // .page_url // ""))
        and ((.clip.stream_urls // .stream_urls // []) | length) > 0' \
        "$detail_ok" >/dev/null 2>&1; then
        log_fail "/detail happy-path contract failed for $real_page_url"
        smoke_echo_safe "$(head -c 800 "$detail_ok" 2>/dev/null || true)" >&2
        failures=$((failures + 1))
    else
        log_pass "/detail happy-path ok=true for $real_page_url"
    fi

    # ── Phase 3: negative POST /detail with a known-invalid clip_page_url
    local detail_snf="$WORK_DIR/gate1_detail_snf.json"
    code=$(curl -sS --max-time "$SMOKE_HTTP_TIMEOUT_SECONDS" \
        -X POST -H 'Content-Type: application/json' \
        -d "$(jq -nc --arg u "$bad_page_url" '{clip_page_url:$u}')" \
        "$SCRAPER_URL/detail" -o "$detail_snf" -w '%{http_code}' 2>/dev/null || echo 000)

    if [[ ! "$code" =~ ^2[0-9][0-9]$ ]]; then
        log_fail "POST /detail (negative) HTTP=$code (expected 2xx with STREAM_NOT_FOUND) for $bad_page_url"
        smoke_echo_safe "$(head -c 600 "$detail_snf" 2>/dev/null || true)" >&2
        failures=$((failures + 1))
    elif ! jq -e '.ok == false
        and .error == "STREAM_NOT_FOUND"
        and ((.clip_id // "") | length) > 0
        and ((.stream_urls // []) | length) == 0' \
        "$detail_snf" >/dev/null 2>&1; then
        log_fail "/detail STREAM_NOT_FOUND contract failed for $bad_page_url"
        smoke_echo_safe "$(head -c 800 "$detail_snf" 2>/dev/null || true)" >&2
        failures=$((failures + 1))
    else
        log_pass "/detail STREAM_NOT_FOUND ok=false for $bad_page_url"
    fi

    if (( failures > 0 )); then
        log_fail "Gate 1 /detail hard gate failed (${failures} sub-checks)"
        return 1
    fi
    log_pass "Gate 1 /detail hard gate clean"
}

main() {
    if [[ "$DRY_RUN" == "1" ]]; then
        smoke_echo_safe "DRY RUN — /detail probes (Gate 1):"
        printf '  POST %s/detail (clip_page_url from LIVE_QUERIES[0], happy path)\n' "$SCRAPER_URL"
        printf '  POST %s/detail (clip_page_url=%s, negative STREAM_NOT_FOUND)\n' \
            "$SCRAPER_URL" "https://artlist.io/stock-footage/clip/00000000"
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
