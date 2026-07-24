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

# Per-battery runtime configuration.
HOST="${VELOX_HOST:-127.0.0.1}"
PIPELINE_PORT="${PIPELINE_PORT:-${VELOX_PORT:-8000}}"
BASE_URL="http://${HOST}:${PIPELINE_PORT}"

if [[ -n "${LIVE_QUERIES:-}" ]]; then
    IFS='|' read -ra LIVE_QUERIES <<<"${LIVE_QUERIES}"
    if [[ ${#LIVE_QUERIES[@]} -ne 3 \
       || -z "${LIVE_QUERIES[0]:-}" \
       || -z "${LIVE_QUERIES[1]:-}" \
       || -z "${LIVE_QUERIES[2]:-}" ]]; then
        # ISO 8601 local time. Out-of-band artifact is read days later
        # for post-mortem, so explicit date attribution beats HH:MM:SS.
        # Gates keep HH:MM:SS because they are in-band within a single run.
        ts="$(date '+%Y-%m-%dT%H:%M:%S')"
        printf >&2 '[FAIL]  %s  LIVE_QUERIES env override must yield exactly 3 non-empty pipe-delimited terms; got %d slot(s): "%s"\n' \
            "$ts" "${#LIVE_QUERIES[@]}" "${LIVE_QUERIES[*]}"
        # JSON artifact (matches $WORK_DIR/gateN_*.json pattern from the
        # live gates; jq-safe, no shell-injection via re-source path).
        # value is a JSON array via per-slot args so consumers can read
        # slot boundaries faithfully regardless of override shape (the
        # previous round used ${LIVE_QUERIES[*]} which joins with default
        # IFS-space and lost slot structure for operators to inspect).
        # Honors $TMPDIR so macOS / systemd-private-tmp / CI sandboxes
        # land the artifact under the operator's tmpdir. mkdir is
        # fail-safe so a read-only /tmp does not mask the canonical
        # exit-2 code (under set -e, mkdir failure would otherwise
        # abort with exit 1 and lose the setup-error classification).
        : "${WORK_DIR:=${TMPDIR:-/tmp}/artlist_e2e_validation}"
        if ! mkdir -p "$WORK_DIR" 2>/dev/null; then
            printf >&2 '[WARN]  %s  could not mkdir %s (validation artifact skipped)\n' \
                "$ts" "$WORK_DIR"
            exit 2
        fi
        # Build the value array length-N faithfully: pipe the bash array
        # via NUL separator (the canonical CSV null-record delimiter per
        # IEEE Std 1003.1), slurp + split + map empty-to-null + slice
        # to ${#LIVE_QUERIES[@]} so the trailing empty from printf's
        # terminal NUL does NOT inflate the count. --argjson value
        # carries the full JSON array regardless of operator-supplied N,
        # so the artifact faithfully reports {slots:N, value:[s0, ..., sN-1]}.
        # Fail-safe wrapper around the jq pipeline: under set -euo
        # pipefail the value_json=$(...) compound would otherwise mask
        # the canonical exit-2 classification if jq exits non-zero
        # (same fail-mode as round-4 mkdir).
        if ! value_json=$(printf '%s\0' "${LIVE_QUERIES[@]}" | jq -Rs --argjson n "${#LIVE_QUERIES[@]}" \
            'split("\u0000") | map(if . == "" then null else . end) | .[:$n]'); then
            printf >&2 '[WARN]  %s  jq pipeline failed producing the value array (artifact dropped, exit 2 still enforced)\n' \
                "$ts"
            exit 2
        fi
        # Note asymmetry: the stderr FAIL line above uses
        # ${LIVE_QUERIES[*]} (space-joined) for human-readable
        # display; the JSON artifact carries the faithful length-N
        # array so consumers can read slot boundaries. The slot
        # count (${#LIVE_QUERIES[@]}) is the operator's primary
        # signal in either shape.
        jq -nc --arg ts "$ts" --argjson slots "${#LIVE_QUERIES[@]}" \
            --argjson value "$value_json" \
            '{event:"live_queries_validation_failed",ts:$ts,slots:$slots,value:$value}' \
            > "$WORK_DIR/live_queries_validation_failed.json"
        # exit 2 (canonical setup-error exit code per file header) hard-
        # terminates the run. Do NOT source this file unless you intend
        # to use only the helper functions in lib/common.sh and
        # lib/velox_domain.sh — the LIVE_QUERIES block will tear down
        # the sourcee shell.
        exit 2
    fi
elif [[ -n "${LIVE_QUERY_1:-}" && -n "${LIVE_QUERY_2:-}" && -n "${LIVE_QUERY_3:-}" ]]; then
    LIVE_QUERIES=("${LIVE_QUERY_1}" "${LIVE_QUERY_2}" "${LIVE_QUERY_3}")
else
    LIVE_QUERIES=(
        "business team working in modern office"
        "heavyweight boxer training in gym"
        "boxing arena crowd celebrating"
    )
fi
unset LIVE_QUERY_1 LIVE_QUERY_2 LIVE_QUERY_3

smoke_require curl jq

# Per-battery counters
PASS=0; WARN=0; FAIL=0
log_pass() { printf '[PASS]  %s %s\n' "$(date '+%H:%M:%S')" "$*"; PASS=$((PASS + 1)); }
log_warn() { printf '[WARN]  %s %s\n' "$(date '+%H:%M:%S')" "$*"; WARN=$((WARN + 1)); }
log_fail() { printf '[FAIL]  %s %s\n' "$(date '+%H:%M:%S')" "$*"; FAIL=$((FAIL + 1)); }
log_info() { printf '[INFO]  %s %s\n' "$(date '+%H:%M:%S')" "$*"; }

# ── Gate 3 — /api/artlist/search/live × 3 queries + 60s timeout ─────────
# DoD spec (July 2026): tre query semanticamente differenti (business,
# boxing-gym, boxing-arena). Per ogni query:
#   - HTTP 2xx OR explicit err=SEARCH_TIMEOUT if 60s elapses
#   - provider == 'artlist'
#   - ≥1 clip in .clips[]
#   - per-clip clip_id (ExternalID/ID) non-empty
#   - per-clip page_url on artlist.io
#   - per-clip title non-placeholder (≠ "Artlist", length>5, non-empty)
#   - query term NOT truncated by server (URL round-trip)
#   - no placeholder / no invented: RawMetadata present + Keywords[] non-empty
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
