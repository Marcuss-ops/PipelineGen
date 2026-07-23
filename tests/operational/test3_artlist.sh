#!/usr/bin/env bash
# tests/operational/test3_artlist.sh — Test 3: Artlist live search real clip.
#
# Verifies, against the live server and the real Node scraper, that:
#   - GET /api/artlist/search/live is force-live
#   - response contains live_enforced=true, cache_strategy="bypassed"
#   - returned clips are real Artlist clips with ID, title, page URL and metadata.
#
# Exit codes:
#   0 all assertions passed
#   1 one or more assertions failed
#   2 setup error / missing prerequisite

set -euo pipefail

DIR=$(cd "$(dirname "$0")" && pwd)
# shellcheck disable=SC1091
source "$DIR/lib/common.sh"

smoke_require curl jq

HOST="${VELOX_HOST:-127.0.0.1}"
PIPELINE_PORT="${VELOX_PORT:-8000}"
BASE_URL="http://${HOST}:${PIPELINE_PORT}"

TERM="business team working in modern office"
LIMIT=5

PASS=0
WARN=0
FAIL=0

log_pass() { printf '[PASS]  %s %s\n' "$(date '+%H:%M:%S')" "$*"; PASS=$((PASS + 1)); }
log_warn() { printf '[WARN]  %s %s\n' "$(date '+%H:%M:%S')" "$*"; WARN=$((WARN + 1)); }
log_fail() { printf '[FAIL]  %s %s\n' "$(date '+%H:%M:%S')" "$*"; FAIL=$((FAIL + 1)); }
log_info() { printf '[INFO]  %s %s\n' "$(date '+%H:%M:%S')" "$*"; }

require_server() {
    local code
    code=$(curl -sS --max-time 5 -o /dev/null -w '%{http_code}' "$BASE_URL/health" 2>/dev/null || echo "000")
    if [[ ! "$code" =~ ^2[0-9][0-9]$ ]]; then
        log_fail "server not reachable at $BASE_URL/health (HTTP $code)"
        exit 2
    fi
    log_info "server reachable at $BASE_URL/health"
}

run_live_search() {
    local out="$WORK_DIR/artlist_live.json"
    local code
    code=$(curl -sS --max-time 180 -G -o "$out" -w '%{http_code}' \
        -H "Authorization: Bearer $SMOKE_TOKEN" \
        --data-urlencode "term=${TERM}" \
        --data-urlencode "limit=${LIMIT}" \
        "$BASE_URL/api/artlist/search/live")
    if [[ ! "$code" =~ ^2[0-9][0-9]$ ]]; then
        log_fail "live search HTTP $code"
        smoke_echo_safe "$(head -c 800 "$out" 2>/dev/null || true)" >&2
        return 1
    fi
    printf '%s\t%s\n' "$code" "$out"
}

main() {
    if [[ "$DRY_RUN" == "1" ]]; then
        smoke_echo_safe "DRY RUN — would probe Artlist live search for: $TERM"
        exit 0
    fi

    require_server

    smoke_log_section "Artlist live search"
    local out code
    IFS=$'\t' read -r code out < <(run_live_search)

    local live_enforced cache_strategy clip_count
    live_enforced=$(jq -r '.live_enforced // false' "$out")
    cache_strategy=$(jq -r '.cache_strategy // empty' "$out")
    clip_count=$(jq '.clips | length' "$out")

    if [[ "$live_enforced" != "true" ]]; then
        log_fail "live_enforced = $live_enforced, want true"
        return 1
    fi
    log_pass "live_enforced = true"

    if [[ "$cache_strategy" != "bypassed" ]]; then
        log_fail "cache_strategy = $cache_strategy, want bypassed"
        return 1
    fi
    log_pass "cache_strategy = bypassed"

    if [[ "$clip_count" -le 0 ]]; then
        log_fail "no clips returned (count=$clip_count)"
        return 1
    fi
    log_pass "returned $clip_count clip(s)"

    # Validate each clip has the mandatory real-Artlist provenance fields.
    local i
    for i in $(seq 0 $((clip_count - 1))); do
        local id title pageurl thumbnail
        id=$(jq -r ".clips[$i].ExternalID // empty" "$out")
        title=$(jq -r ".clips[$i].Title // empty" "$out")
        pageurl=$(jq -r ".clips[$i].PageURL // empty" "$out")
        thumbnail=$(jq -r ".clips[$i].ThumbnailURL // empty" "$out")

        if [[ -z "$id" || -z "$title" || -z "$pageurl" ]]; then
            log_fail "clip[$i] missing id/title/pageurl"
            return 1
        fi

        if [[ ! "$pageurl" =~ artlist\. ]]; then
            log_fail "clip[$i] page URL does not look like Artlist: $pageurl"
            return 1
        fi

        if [[ -z "$thumbnail" || ! "$thumbnail" =~ ^https?:// ]]; then
            log_warn "clip[$i] thumbnail missing or not an HTTP URL"
        fi

        # Metadata/keyword presence: at least one keyword is required and
        # RawMetadata must be present.
        local raw_meta keywords_len
        raw_meta=$(jq -r ".clips[$i].RawMetadata // empty" "$out")
        keywords_len=$(jq ".clips[$i].Keywords | length" "$out" 2>/dev/null || echo 0)
        if [[ -z "$raw_meta" ]]; then
            log_fail "clip[$i] missing RawMetadata"
            return 1
        fi
        if [[ "$keywords_len" -eq 0 ]]; then
            log_fail "clip[$i] has no Keywords"
            return 1
        fi
    done
    log_pass "all $clip_count clip(s) have Artlist ID, title, page URL and metadata"

    printf '\n============================================\n'
    printf '  Test 3 — Artlist Live Search E2E\n'
    printf '  PASS=%d  WARN=%d  FAIL=%d\n' "$PASS" "$WARN" "$FAIL"
    printf '============================================\n'
    if [[ "$FAIL" -gt 0 ]]; then
        printf 'VERDICT: FAIL\n'
        exit 1
    fi
    printf 'VERDICT: PASS\n'
    exit 0
}

main "$@"
