#!/usr/bin/env bash
# tests/operational/artlist/02_search_live.sh — Artlist DoD Gate 3 (/search/live × 3 + 60s timeout).
#
# Reorg (July 2026): split out of tests/operational/artlist/run_all.sh (now a thin shim).
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
# Transport sentinel split (fixup! July 2026): the helper
# artlist_search_live now synthesizes a typed _transport_kind in
# the response body when transport fails, so the gate can disambiguate:
#   * curl exit 28 (--max-time exceeded) → _transport_kind:"SEARCH_TIMEOUT"
#   * HTTP 401/403 (auth reject)        → _transport_kind:"AUTH_REQUIRED"
#   * any other transport failure        → _transport_kind:"SCRAPER_UNAVAILABLE"
#     (connect refused, couldn't resolve, HTTP 5xx, empty body)
# The gate sub-case-labels these distinctly in the verdict line so
# transport_fail counts name the failure category.  SEARCH_TIMEOUT is
# reserved for actual --max-time overruns per the DoD spec literal
# "Timeout 60s deve produrre SEARCH_TIMEOUT"; the prior conflation
# (every transport failure → SEARCH_TIMEOUT) was over-broad.

set -euo pipefail

DIR=$(cd "$(dirname "$0")" && pwd)
source "${DIR}/../lib/_artlist_common.sh"



artlist_live_queries_validate && artlist_live_queries_default

smoke_require curl jq



# ── Gate 3 — /api/artlist/search/live × 3 queries + 60s timeout ─────────
# DoD spec (July 2026): tre query semanticamente differenti (business,
# boxing-gym, boxing-arena). Per ogni query:
#   - HTTP 2xx OR explicit SEARCH_TIMEOUT sentinel on >60s wall-clock
#     (the gate labels this specifically — curl exit 28 path)
#   - provider == 'artlist' (encoded in artlist_search_live's jq)
#   - ≥1 clip in .clips[] (ok=true with zero results is FORBIDDEN;
#                                encoded in the helper)
#   - per-clip shape tuple: ExternalID/ID, page_url on artlist.io,
#     Title non-placeholder (≠ "Artlist"), RawMetadata + Keywords[]
#     non-empty (encoded in the helper)
#   - query term NOT truncated (encoded via (.term // $term) == $term
#                                jq contract; server may not echo .term
#                                and that's treated as not-truncated)
#   - HARD GATE 3.relevance  (promoted from warning July 2026, stays
#     inline at the gate layer for per-query semantic reason): at least
#     one returned clip has ≥1 query-token overlap with the clip's
#     Title / Tags / Categories / Keywords corpus.
#
# Transport sentinel split: rc=2 from the helper means a transport-level
# failure.  We read `_transport_kind` from the body to disambiguate:
#   SEARCH_TIMEOUT     → --max-time exceeded (curl exit 28)
#   AUTH_REQUIRED      → HTTP 401/403 (token role or missing header)
#   SCRAPER_UNAVAILABLE → connect-refused / couldn't resolve / 5xx / empty
# Each category increments per-category counters so the verdict line
# names the failure mode at a glance.
#
# Implementation notes:
#   * LIVE_QUERIES[0..2] is env-driven; defaults to a canonical English
#     3-term set so the test remains reproducible in CI.
#   * 60s timeout enforced inside the helper via --max-time; the helper
#     shadows that as _transport_kind="SEARCH_TIMEOUT" in the body.
#   * jq relevance pipeline (per-clip corpus = Title + Keywords + Tags
#     + Categories vs query tokens filtered by stopwords + length>2).
#     A clip is relevant iff ≥1 token appears verbatim in its corpus.
#     The per-query hard-gate fires only if ZERO of .clips[] is relevant.
gate_live_search_three() {
    smoke_log_section "Gate 3 — /search/live × 3 + 60s timeout (typed transport sentinels)"
    local failures=0
    local search_timeout_fail=0 auth_required_fail=0 scraper_unavailable_fail=0
    local contract_fail=0 relevance_fail=0
    local per_query_timeout=60

    local idx=0
    local q
    for q in "${LIVE_QUERIES[@]:0:3}"; do
        smoke_log_section "Gate 3 query $((idx+1))/3: '$q'"
        local out="$WORK_DIR/gate3_search_${idx}.json"
        local rc=0
        artlist_search_live --term "$q" \
            --timeout-seconds "$per_query_timeout" \
            --save-body "$out" || rc=$?
        case "$rc" in
            0)
                # Helper's contract jq has already verified provider +
                # clip-count + term round-trip + per-clip shape tuple
                # (ExternalID/ID + PageURL artlist.io + Title non-
                # "Artlist" + RawMetadata + Keywords[]). No re-checks.
                log_pass "live search returned valid clips for query '$q'"
                # Relevance HARD GATE — kept at gate layer because
                # tokens differ per query and the lib can't see
                # ${LIVE_QUERIES[i]} at runtime.
                local clip_count relevant_count
                clip_count=$(jq '.clips | length' "$out" 2>/dev/null || echo 0)
                relevant_count=$(jq -r --arg q "$q" '
                    def stopwords: ["in","the","a","an","of","at","on","to","for","with","and","or","but","is","are","by","as","be"];
                    def tokens: $q | ascii_downcase | split(" ")
                        | map(select(length > 2))
                        | map(select(. as $t | (stopwords | index($t)) == null));
                    def clip_corpus: ([
                        (.Title // ""),
                        (.Keywords // [] | join(" ")),
                        (.Tags // [] | join(" ")),
                        (.Categories // [] | join(" "))
                    ] | join(" ") | ascii_downcase);
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
                    relevance_fail=$((relevance_fail + 1))
                    failures=$((failures + 1))
                else
                    log_pass "relevance: ${relevant_count}/${clip_count} clips had ≥1 token match for '$q'"
                fi
                ;;
            2)
                # Transport-level failure. The helper synthesized a
                # typed _transport_kind in the body so we can disambiguate
                # SEARCH_TIMEOUT (curl exit 28) from SCRAPER_UNAVAILABLE
                # (connect refused, couldn't resolve, HTTP 5xx, empty
                # body) and AUTH_REQUIRED (HTTP 401/403). The prior
                # conflated "SEARCH_TIMEOUT" was over-broad.
                local transport_kind transport_http curl_rc timeout_sec
                transport_kind=$(jq -r '._transport_kind // "TRANSPORT_ERROR"' "$out" 2>/dev/null || echo TRANSPORT_ERROR)
                transport_http=$(jq -r '._transport_http // empty' "$out" 2>/dev/null || true)
                curl_rc=$(jq -r '._curl_rc // empty' "$out" 2>/dev/null || true)
                timeout_sec=$(jq -r '._timeout_seconds // empty' "$out" 2>/dev/null || true)
                case "$transport_kind" in
                    SEARCH_TIMEOUT)
                        log_fail "SEARCH_TIMEOUT (>${timeout_sec:-${per_query_timeout}}s) for query '$q' (curl_rc=${curl_rc:-?})"
                        search_timeout_fail=$((search_timeout_fail + 1))
                        ;;
                    AUTH_REQUIRED)
                        log_fail "AUTH_REQUIRED (HTTP=${transport_http:-?}) for query '$q'"
                        auth_required_fail=$((auth_required_fail + 1))
                        ;;
                    SCRAPER_UNAVAILABLE)
                        log_fail "SCRAPER_UNAVAILABLE (HTTP=${transport_http:-?} curl_rc=${curl_rc:-?}) for query '$q'"
                        scraper_unavailable_fail=$((scraper_unavailable_fail + 1))
                        ;;
                    *)
                        log_fail "TRANSPORT_ERROR (kind=${transport_kind} HTTP=${transport_http:-?} curl_rc=${curl_rc:-?}) for query '$q'"
                        scraper_unavailable_fail=$((scraper_unavailable_fail + 1))
                        ;;
                esac
                failures=$((failures + 1))
                ;;
            *)
                # Contract violation: helper's jq filter rejected the
                # response (provider != artlist OR zero clips OR a per-
                # clip shape-tuple field missing OR title="Artlist" OR
                # RawMetadata empty OR Keywords[] empty OR term-echoed-
                # back mismatch). Forensic via head -c 800 of the
                # captured body (smoke_echo_safe redacts tokens).
                log_fail "live search contract violated for query '$q'"
                smoke_echo_safe "$(head -c 800 "$out" 2>/dev/null || true)" >&2
                contract_fail=$((contract_fail + 1))
                failures=$((failures + 1))
                ;;
        esac
        idx=$((idx + 1))
    done

    if (( failures > 0 )); then
        log_fail "Gate 3 /search/live × 3 failed (failures=${failures} search_timeout=${search_timeout_fail} auth_required=${auth_required_fail} scraper_unavailable=${scraper_unavailable_fail} contract=${contract_fail} relevance=${relevance_fail})"
        return 1
    fi
    log_pass "Gate 3 /search/live × 3 clean (search_timeout=0 auth_required=0 scraper_unavailable=0 contract=0 relevance=0)"
}

main() {
    if [[ "$DRY_RUN" == "1" ]]; then
        smoke_echo_safe "DRY RUN — /search/live probes (Gate 3):"
        printf '  GET  %s/api/artlist/search/live?term=<LIVE_QUERIES[0..2]>&limit=5 (via artlist_search_live)\n' "$BASE_URL"
        printf '        ×3 queries, 60s each, typed transport sentinels (SEARCH_TIMEOUT | AUTH_REQUIRED | SCRAPER_UNAVAILABLE)\n'
        printf '        relevance HARD GATE inline (Title/Tags/Categories/Keywords token overlap)\n'
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
