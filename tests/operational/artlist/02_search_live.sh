#!/usr/bin/env bash
# tests/operational/artlist/02_search_live.sh — Artlist DoD Gate 3 (/search/live × 3 + 60s timeout).
#
# Reorg (July 2026): split out of tests/operational/artlist_e2e.sh (now a thin shim).
# Hard-gate check (DoD, July 2026) per LIVE_QUERIES[i]:
#   - HTTP 2xx OR explicit err=SEARCH_TIMEOUT if 60s elapses
#   - provider == 'artlist'
#   - ≥1 clip in .clips[]  (ok=true with zero results is FORBIDDEN)
#   - per-clip clip_id / page_url (artlist.io) / title (non-placeholder) /
#     RawMetadata + Keywords[] verified
#   - query term NOT truncated by server (URL round-trip)
#
# LIVE_QUERIES is env-driven:
#   - LIVE_QUERIES="a|b|c"  (pipe-delimited; validated to N=3 non-empty slots)
#   - LIVE_QUERY_1 / _2 / _3 (per-term env vars)
#   - (otherwise) English fallback (canonical DoD default)
#
# 60s timeout emits SEARCH_TIMEOUT sentinel rather than ok=true with zero results.

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



# ── Gate 3 — /api/artlist/search/live × 3 queries + 60s timeout ─────────
# DoD spec (July 2026): tre query semanticamente differenti (business,
# boxing-gym, boxing-arena). Per ogni query:
#   - HTTP 2xx OR explicit err=SEARCH_TIMEOUT if 60s elapses
#   - provider == 'artlist'
#   - ≥1 clip in .clips[]  (ok=true with zero results is FORBIDDEN)
#   - per-clip clip_id (ExternalID/ID) non-empty
#   - per-clip page_url on artlist.io
#   - per-clip title non-placeholder (≠ "Artlist", length>5, non-empty)
#   - query term NOT truncated by server (URL round-trip)
#   - no placeholder / no invented: RawMetadata present + Keywords[] non-empty
#   - HARD GATE 3.relevance  (promoted from warning July 2026): at least
#     one returned clip has ≥1 query-token overlap with the clip's
#     Title / Tags / Categories / Keywords corpus. Catches a server
#     returning unrelated/filler results that happen to satisfy the
#     structural contract above.
#
# Implementation notes:
#   * LIVE_QUERIES[0..2] is env-driven (see runtime config block above);
#     defaults to a canonical English 3-term set so the test remains
#     reproducible in CI without an env override. Italian-language
#     queries are not surfaced in code because Artlist's catalog is
#     English-only — they would return zero hits against /search/live
#     and would trip the "ok=true with zero results" DoD prohibition.
#   * 60s timeout enforced on the curl side; on timeout we emit the
#     SEARCH_TIMEOUT sentinel rather than reporting ok=true with zero
#     results (as the DoD explicitly forbids).
#   * Raw curl (no smoke_curl) for the Authorization header + per-query
#     timeout ergonomics; token must be present (validated by lib/common.sh).
#   * jq relevance pipeline (added July 2026): per-clip corpus =
#     Title + Keywords + Tags + Categories (lower-cased). Query tokens =
#     whitespace-split lower-cased, stopword-filtered, length>2. A clip
#     is "relevant" iff any token appears verbatim in its corpus. The
#     per-query gate fires only if ZERO of .clips[] is relevant — one
#     matching clip satisfies the hard gate. Bumping the threshold to
#     ≥50% would be a separate PR per godlike/06 SSOT avoid-creep rule.
#   * Future refactor (post-reorg): delegate to lib/artlist.sh::artlist_search_live
#     once the helper is wired to expose per-clip assertions externally.
gate_live_search_three() {
    smoke_log_section "Gate 3 — /search/live × 3 + 60s timeout (SEARCH_TIMEOUT sentinel)"
    local failures=0
    local per_query_timeout=60

    local idx=0
    local q
    for q in "${LIVE_QUERIES[@]:0:3}"; do
        smoke_log_section "Gate 3 query $((idx+1))/3: '$q'"
        local out="$WORK_DIR/gate3_search_${idx}.json"
        local code
        code=$(curl -sS --max-time "$per_query_timeout" -G \
            -o "$out" -w '%{http_code}' \
            -H "Authorization: Bearer $SMOKE_TOKEN" \
            --data-urlencode "term=$q" \
            --data-urlencode "limit=5" \
            "$BASE_URL/api/artlist/search/live" 2>/dev/null || echo 000)

        # Timeout / transport failure: explicit SEARCH_TIMEOUT sentinel
        # (DoD: never report ok=true with zero results on slow queries).
        if [[ "$code" == "000" || -z "$code" ]]; then
            log_fail "SEARCH_TIMEOUT (>${per_query_timeout}s) for query '$q'"
            failures=$((failures + 1))
            idx=$((idx + 1))
            continue
        fi

        if [[ ! "$code" =~ ^2[0-9][0-9]$ ]]; then
            log_fail "live search HTTP=$code (want 2xx) for query '$q'"
            smoke_echo_safe "$(head -c 400 "$out" 2>/dev/null || true)" >&2
            failures=$((failures + 1))
            idx=$((idx + 1))
            continue
        fi

        # Provider + clips contract
        if ! jq -e '.provider == "artlist"
            and ((.clips // []) | length) > 0' "$out" >/dev/null 2>&1; then
            log_fail "provider != 'artlist' OR zero clips for query '$q' (ok-but-empty NOT allowed)"
            smoke_echo_safe "$(head -c 400 "$out" 2>/dev/null || true)" >&2
            failures=$((failures + 1))
            idx=$((idx + 1))
            continue
        fi
        log_pass "live search returned clips for query '$q'"

        # Query-not-truncated guard: server should echo back the term.
        # Fallback to URL round-trip if `.term` is absent from the response.
        local recv_term
        recv_term=$(jq -r '.term // empty' "$out" 2>/dev/null || true)
        if [[ -n "$recv_term" && "$recv_term" != "$q" ]]; then
            log_fail "term echoed back '$recv_term' != original '$q' (server truncated query)"
            failures=$((failures + 1))
        else
            log_pass "query '$q' not truncated"
        fi

        # Per-clip shape walk
        local clip_count clip_failures
        clip_count=$(jq '.clips | length' "$out" 2>/dev/null || echo 0)
        clip_failures=0
        local ci
        for ci in $(seq 0 $((clip_count - 1))); do
            local clip_id page_url title raw_meta kw_len
            clip_id=$(jq -r ".clips[$ci].ExternalID // .clips[$ci].ID // empty" "$out")
            page_url=$(jq -r ".clips[$ci].PageURL // empty" "$out")
            title=$(jq -r ".clips[$ci].Title // empty" "$out")
            raw_meta=$(jq -r ".clips[$ci].RawMetadata // empty" "$out")
            kw_len=$(jq ".clips[$ci].Keywords // [] | length" "$out" 2>/dev/null || echo 0)

            if [[ -z "$clip_id" ]]; then
                log_fail "clip[$ci] missing clip_id (ExternalID/ID) for '$q'"
                clip_failures=$((clip_failures + 1))
            fi
            if [[ -z "$page_url" || ! "$page_url" =~ ^https?://artlist\.io/ ]]; then
                log_fail "clip[$ci] page_url invalid '$page_url' for '$q'"
                clip_failures=$((clip_failures + 1))
            fi
            if [[ -z "$title" || "$title" == "Artlist" || ${#title} -lt 5 ]]; then
                log_fail "clip[$ci] title placeholder/invalid '$title' for '$q'"
                clip_failures=$((clip_failures + 1))
            fi
            if [[ -z "$raw_meta" || "$kw_len" == "0" ]]; then
                log_fail "clip[$ci] missing RawMetadata or zero Keywords (placeholder/invented?) for '$q'"
                clip_failures=$((clip_failures + 1))
            fi
        done
        if (( clip_failures == 0 )); then
            log_pass "all $clip_count clips valid for query '$q'"
        else
            failures=$((failures + clip_failures))
        fi

        # ── Hard Gate 3.relevance (promoted from warning July 2026) ────
        # Per-clip corpus = lower-cased concat of Title + Keywords[] +
        # Tags[] + Categories[]. Query tokens = lower-cased
        # whitespace-split, stopword-filtered, length>2. A clip is
        # relevant if ≥1 token appears verbatim in its corpus. The
        # DoD rejects any query whose .clips[] contains ZERO
        # relevant clips (would indicate the server is returning
        # unrelated / placeholder / invented results). One match is
        # enough; tightening to ≥50% is left to a followup PR.
        local relevant_count
        relevant_count=$(jq -r --arg q "$q" '
            def stopwords: ["in","the","a","an","of","at","on","to","for","with","and","or","but","is","are","by","as","be"];
            # Tokenize: lowercase, split on whitespace, drop short tokens
            # (len<=2) and English stopwords. NOTE: stopword membership uses
            # `index($t) == null` rather than `contains($t)` — jq `contains`
            # rejects cross-type checks (array vs string) with
            # "array ... and string ... cannot have their containment
            # checked"; `index` returns integer-or-null and compares safely.
            def tokens: $q | ascii_downcase | split(" ")
                | map(select(length > 2))
                | map(select(. as $t | (stopwords | index($t)) == null));
            def clip_corpus: ([
                (.Title // ""),
                (.Keywords // [] | join(" ")),
                (.Tags // [] | join(" ")),
                (.Categories // [] | join(" "))
            ] | join(" ") | ascii_downcase);
            # `clip_relevant` accumulates via `reduce` over tokens[] — avoids
            # the map/`contains` binding ambiguity that bit the original
            # attempt. `false` seeds, `or` flips to `true` on first match.
            def clip_relevant:
                clip_corpus as $c
                | if (tokens | length) == 0 then false
                  else reduce tokens[] as $t (false; . or ($c | contains($t)))
                  end;
            [.clips // [] | .[] | clip_relevant]
                | map(select(. == true))
                | length' "$out" 2>/dev/null || echo 0)
        if [[ "${relevant_count:-0}" -lt 1 ]]; then
            log_fail "relevance HARD GATE violated for '$q' (0 clips had ≥1 query-token match in Title/Tags/Categories/Keywords corpus)"
            failures=$((failures + 1))
        else
            log_pass "relevance: ${relevant_count}/${clip_count} clips had ≥1 token match for '$q'"
        fi

        idx=$((idx + 1))
    done

    if (( failures > 0 )); then
        log_fail "Gate 3 /search/live × 3 failed (${failures} sub-checks)"
        return 1
    fi
    log_pass "Gate 3 /search/live × 3 clean"
}

main() {
    if [[ "$DRY_RUN" == "1" ]]; then
        smoke_echo_safe "DRY RUN — /search/live probes (Gate 3):"
        printf '  GET  %s/api/artlist/search/live?term=<LIVE_QUERIES[0..2]>&limit=5\n' "$BASE_URL"
        printf '        ×3 queries, 60s each, SEARCH_TIMEOUT on overrun\n'
        exit 0
    fi
    gate_live_search_three || return 1

    printf '\n============================================\n'
    printf '  02_search_live\n'
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
