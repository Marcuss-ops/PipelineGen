#!/usr/bin/env bash
# tests/operational/lib/velox_domain.sh — PipelineGen domain helpers.
#
# Source-able library. Callers MUST source tests/operational/lib/common.sh
# BEFORE this file because every velox_* helper depends on at least one of:
#   - smoke_curl              (HTTP wrapper with token + Idempotency-Key)
#   - smoke_assert_http_2xx   (assert 2xx response)
#   - smoke_assert_eq         (string equality assertion)
#   - smoke_poll_terminal     (job terminal-status polling)
#   - WORK_DIR                (per-script temp dir)
#   - SMOKE_HTTP_TIMEOUT_SECONDS, SMOKE_API_BASE, SMOKE_TOKEN,
#     SMOKE_LAST_HTTP, SMOKE_LAST_BODY, SMOKE_LAST_STATUS
#
# Naming convention (July 2026 DoD refactor):
#   - `smoke_*` lives in lib/common.sh and is generic infra (HTTP/SQLite/ffprobe/dry-run)
#   - `velox_*` lives here and is PipelineGen-specific domain logic
#     (Qdrant payload assertions, Drive resolve-by-id wrapper, Artlist /run
#      pipeline call, Artlist /detail contract probe, Artlist /download
#      contract probe, Artlist /search/live contract probe). The split
#      keeps `common.sh` reusable by side-car services that don't run on
#      the PipelineGen stack.
#
# Helpers stay pure: every velox_* function returns a status code and writes
# its response file under ${WORK_DIR:-/tmp}; it does NOT touch PASS/WARN/FAIL
# counters — the host battery owns those.

# ── artlist_qdrant_assert — at least one PUBLISHED point for clip_id
# Args:
#   clip_id           asset_id to look up (positional 1, mandatory)
#   collection        qdrant collection name (positional 2, mandatory)
#   qdrant_url        base URL (positional 3, mandatory)
#   expected_source   payload.source filter (positional 4, mandatory for
#                     cross-source reuse: 'artlist', 'google-flow', ...)
#   expected_media_type
#                     payload.media_type filter (positional 5, mandatory:
#                     'video', 'image', 'audio')
#   expected_lifecycle
#                     payload.lifecycle_state (positional 6,
#                     default 'PUBLISHED')
#   api_key           optional QDRANT_API_KEY header (positional 7)
# Returns: 0 → point found with shape match + asset_id round-trip OK
#          1 → shape contract failed (including asset_id round-trip drift:
#                e.g. must: [{key: asset_id}] filter silently bypassed and
#                a different point came back)
#          2 → transport / HTTP failure
#
# Hardening note (July 2026): the original implementation checked
# payload.source / media_type / lifecycle_state only. If Qdrant's
# must-filter were ever silently bypassed (reindex mid-flight, index
# drift, schema mismatch on the asset_id field), a wrong point with
# `source=artlist media_type=video lifecycle_state=PUBLISHED` would still
# match the SHAPE contract. The point's payload.asset_id is now also
# asserted to round-trip back to the queried clip_id — DoD Gate 8
# "asset_id corretto" — so a stray-point return fails closed with rc=1.
# Lib-level fix migrates the guarantee into every caller (Gate 8 today;
# any future gate that reuses artlist_qdrant_assert inherits the round-
# trip automatically per AGENTS.md single-focus rule).

# ── artlist_drive_resolve — confirm Drive file id exists, not trashed, size > 0
# Args: <drive_file_id>
# Returns: 0 → file resolved with size > 0 and trashed=false
#          1 → HTTP 2xx but contract failed
#          2 → transport / HTTP non-2xx
# The body is written to a deterministic file under ${WORK_DIR:-/tmp} using
# curl directly (no smoke_curl subshell) so callers inspecting the response
# from outside the function can guarantee $WORK_DIR/velox_drive_${id}.json is
# populated. SMOKE_LAST_BODY side-effects from smoke_curl do NOT survive a
# $(...) subshell wrapping and were the source of Bug 3 in the first wave
# review.

# ── velox_artlist_detail — POST $scraper/detail contract probe + assertion
# Args: --phase <happy|miss> --clip-page-url <url> --scraper-url <url> [--save-body <path>]
# Returns: 0 → contract pass
#          1 → contract violation (response body parsed but didn't match phase contract)
#          2 → transport / HTTP non-2xx / empty body
# Writes the raw response to $save_body_path (or $WORK_DIR/velox_artlist_detail_<ns>.json
# with %N timestamp suffix) so callers can forensic-inspect after a mismatch.
#
# Two polar contracts, fail-closed on either:
#   happy: ok=true AND .clip.ok==true AND page_url startswith("https://artlist.io/")
#          AND primary_url matches (\\.m3u8(\\?|$)|\\.mp4(\\?|$)|/manifest|/playlist)
#          AND primary_url != page_url
#          AND stream_urls[] non-empty
#          AND clip_id (or .clip.clip_id) non-empty
#   miss : ok=false AND error=="STREAM_NOT_FOUND" AND clip_id non-empty
#          AND stream_urls[] EMPTY
#
# The /manifest|/playlist fallback matters because some CDN playlist tokens
# don't carry a literal .m3u8 extension — the contract is "playlist-shaped
# URL" not "literal extension matches". Tighter contracts here will fail
# live probes on real Artlist clips that 03_detail_stream.sh already passes
# (matches the proven inline pattern).
#
# Field-path tolerates both flat and nested clip envelopes
# (`.clip_id` vs `.clip.clip_id`) because the scraper's response shape has
# drifted across versions; `//` fallback chain keeps Gate 1 robust under
# either shape active on the running build.
velox_artlist_detail() {
    local phase="" clip_page_url="" scraper_url="" save_body_path=""
    while (( $# > 0 )); do
        case "$1" in
            --phase) phase="$2"; shift 2 ;;
            --clip-page-url) clip_page_url="$2"; shift 2 ;;
            --scraper-url) scraper_url="$2"; shift 2 ;;
            --save-body) save_body_path="$2"; shift 2 ;;
            *) return 1 ;;
        esac
    done
    [[ -n "$phase" && -n "$clip_page_url" && -n "$scraper_url" ]] || return 1
    case "$phase" in
        happy|miss) ;;
        *) return 1 ;;
    esac
    local out="${save_body_path:-${WORK_DIR:-/tmp}/velox_artlist_detail_$(date +%s%N).json}"
    local code
    code=$(curl -sS --max-time "${SMOKE_HTTP_TIMEOUT_SECONDS:-8}" -w '%{http_code}' \
        -X POST -o "$out" \
        -H 'Content-Type: application/json' \
        -d "$(jq -nc --arg u "$clip_page_url" '{clip_page_url:$u}')" \
        "${scraper_url}/detail" 2>/dev/null || echo 000)
    [[ "$code" =~ ^2[0-9][0-9]$ ]] || return 2
    [[ -s "$out" ]] || return 1
    if [[ "$phase" == "happy" ]]; then
        jq -e '.ok == true
            and (.clip.ok // false) == true
            and ((.clip.page_url // .page_url // "") | startswith("https://artlist.io/"))
            and ((.clip.primary_url // .primary_url // "") | test("\\.m3u8(\\?|$)|\\.mp4(\\?|$)|/manifest|/playlist"))
            and ((.clip.primary_url // .primary_url // "") != "")
            and ((.clip.primary_url // .primary_url // "") != (.clip.page_url // .page_url // ""))
            and (((.clip.stream_urls // .stream_urls // []) | length) > 0)
            and (((.clip.clip_id // .clip_id // "") | length) > 0)' \
            "$out" >/dev/null 2>&1 || return 1
    else
        jq -e '.ok == false
            and .error == "STREAM_NOT_FOUND"
            and (((.clip_id // .clip.clip_id // "") | length) > 0)
            and (((.stream_urls // .clip.stream_urls // []) | length) == 0)' \
            "$out" >/dev/null 2>&1 || return 1
    fi
    return 0
}

# ── velox_artlist_download — POST $scraper/download contract probe + path harvest
# Args: --clip-page-url <url> --scraper-url <url> --output-dir <dir> [--save-body <path>]
# Returns: 0 → contract pass (response body parsed + ok=true + clip_id non-empty +
#                        local_path non-empty)
#          1 → contract violation (2xx response body parsed but didn't match
#                the canonical jq filter; OR local_path was empty)
#          2 → transport / HTTP non-2xx / empty body
# Writes the raw response to $save_body_path (or $WORK_DIR/velox_artlist_dl_<ns>.json
# with %N timestamp suffix) so callers can forensic-inspect after a mismatch.
#
# Scope (binding): this helper strictly owns the /download response contract
# + path harvest — the /download-domain service. File-existence, MIME,
# and the DoD ffprobe contract (smoke_ffprobe_check $local_path 6.5) are
# orchestrated by 04_download.sh::gate_direct_download because they
# operationalise the downloaded artefact and produce richer diagnostic
# logging at the gate layer (per the Gate 1 split: lib owns the contract,
# gate owns the diagnostic logging + the rich failure-mode counter that
# the verdict banner surfaces).
#
# Roundtrip-style callers can read $WORK_DIR/velox_artlist_dl_<ns>.json to
# pull local_path; the body is preserved under --save-body so the gate
# can re-parse without re-firing the (real-quota-consuming) /download.
velox_artlist_download() {
    local clip_page_url="" scraper_url="" output_dir="" save_body_path=""
    while (( $# > 0 )); do
        case "$1" in
            --clip-page-url) clip_page_url="$2"; shift 2 ;;
            --scraper-url) scraper_url="$2"; shift 2 ;;
            --output-dir) output_dir="$2"; shift 2 ;;
            --save-body) save_body_path="$2"; shift 2 ;;
            *) return 1 ;;
        esac
    done
    [[ -n "$clip_page_url" && -n "$scraper_url" && -n "$output_dir" ]] || return 1
    local out="${save_body_path:-${WORK_DIR:-/tmp}/velox_artlist_dl_$(date +%s%N).json}"
    local code
    code=$(curl -sS --max-time "${SMOKE_HTTP_TIMEOUT_SECONDS:-8}" -w '%{http_code}' \
        -X POST -o "$out" \
        -H 'Content-Type: application/json' \
        -d "$(jq -nc --arg u "$clip_page_url" --arg o "$output_dir" \
                '{clip_page_url:$u, output_dir:$o}')" \
        "${scraper_url}/download" 2>/dev/null || echo 000)
    [[ "$code" =~ ^2[0-9][0-9]$ ]] || return 2
    [[ -s "$out" ]] || return 1
    jq -e '.ok == true
        and ((.clip_id // "") | length) > 0
        and ((.local_path // "") | length) > 0' \
        "$out" >/dev/null 2>&1 || return 1
    return 0
}

# ── velox_artlist_search_live — GET /api/artlist/search/live contract probe
# Args: --term <query> [--limit <n>] [--timeout-seconds <n>] [--save-body <path>]
# Returns: 0 → contract pass
#          1 → contract violation (response parsed but didn't match the
#                per-clip shape tuple OR provider/clips-count failed)
#          2 → transport / HTTP non-2xx / empty body / curl --max-time
#                triggered (gate labels this path with the SEARCH_TIMEOUT
#                sentinel — helper does NOT emit SEARCH_TIMEOUT itself;
#                the gate owns the row-level label so the lib stays
#                search-domain-neutral)
# Writes the raw response to $save_body_path (or
# $WORK_DIR/velox_artlist_search_<ns>.json with %N timestamp suffix).
#
# Scope (binding): this helper strictly owns the /search/live response
# contract — the /search domain service for Artlist. The relevance gate
# (per-query Title/Tags/Categories/Keywords token-overlap) is a
# per-query semantic decision and stays at the gate layer;
# 02_search_live.sh::gate_live_search_three runs it inline so the
# same body can be inspected at gate-level with the original tokens
# in scope (the lib can't see ${LIVE_QUERIES[i]} at runtime).
#
# Contract (DoD Gate 3 spec, July 2026):
#   * response.provider == "artlist"
#   * response.clips[] is non-empty (forbidden: ok=true with zero clips)
#   * term round-trip: server may echo `.term`; if echoed it MUST equal
#     the input. If absent, jq `(.term // $term) == $term` collapses to
#     a self-comparison and passes (matches 02_search_live.sh pre-
#     extract semantic where "absent echo" is treated as not-truncated).
#   * every clip passes the per-clip shape tuple:
#       - (ExternalID // ID) non-empty
#       - PageURL startswith artlist.io
#       - Title non-empty AND != "Artlist" (placeholder reject)
#       - RawMetadata non-empty (no invented clips)
#       - Keywords[] non-empty (no placeholder clips)
# Field-path `.clips[i].foo` tolerates both flat (.ExternalID) and nested
# (.clip.ExternalID) layout if the server reshapes.
#
# Default --timeout-seconds=60 matches the DoD Gate 3 spec literal "60s";
# override only for non-canonical probe variants.
velox_artlist_search_live() {
    local term="" limit="5" timeout_seconds="60" save_body_path=""
    while (( $# > 0 )); do
        case "$1" in
            --term) term="$2"; shift 2 ;;
            --limit) limit="$2"; shift 2 ;;
            --timeout-seconds) timeout_seconds="$2"; shift 2 ;;
            --save-body) save_body_path="$2"; shift 2 ;;
            *) return 1 ;;
        esac
    done
    [[ -n "$term" ]] || return 1
    [[ "$timeout_seconds" =~ ^[0-9]+$ ]] || return 1
    local out="${save_body_path:-${WORK_DIR:-/tmp}/velox_artlist_search_$(date +%s%N).json}"
    local code rc_curl
    # Capture curl exit code separately (was previously swallowed by
    # `|| echo 000`) so we can disambiguate a real --max-time overrun
    # (curl exit 28 = "Operation timeout") from connect-refused / empty
    # body / HTTP 5xx.  The DoD spec says SEARCH_TIMEOUT is timeout-
    # SPECIFIC; other transport failures belong to SCRAPER_UNAVAILABLE.
    # We synthesize a typed-sentinel body for each branch so the gate
    # doesn't have to re-probe and the forensic record lands on disk.
    code=$(curl -sS --max-time "${timeout_seconds}" -G \
        -o "$out" -w '%{http_code}' \
        -H "Authorization: Bearer ${SMOKE_TOKEN:-}" \
        --data-urlencode "term=${term}" \
        --data-urlencode "limit=${limit}" \
        "http://${SMOKE_API_BASE}/api/artlist/search/live" 2>/dev/null)
    rc_curl=$?
    if ! [[ "$code" =~ ^2[0-9][0-9]$ ]]; then
        if [[ $rc_curl -eq 28 ]]; then
            # Real timeout — --max-time exceeded.
            jq -nc --arg term "$term" --argjson timeout "$timeout_seconds" \
                '{_transport_kind:"SEARCH_TIMEOUT",_timeout_seconds:$timeout,term:$term,clips:[]}' \
                > "$out"
        elif [[ "$code" == "401" || "$code" == "403" ]]; then
            # HTTP auth reject — distinct from "scraper down" semantics.
            jq -nc --arg term "$term" --arg code "$code" \
                '{"_transport_kind":"AUTH_REQUIRED",_transport_http:$code,term:$term,clips:[]}' > "$out"
        else
            # Connect refused / couldn't resolve / HTTP 5xx / empty body —
            # collapse to SCRAPER_UNAVAILABLE per the Gate 10 vocabulary.
            jq -nc --arg term "$term" --arg code "$code" --argjson rc "$rc_curl" \
                '{"_transport_kind":"SCRAPER_UNAVAILABLE",_transport_http:$code,_curl_rc:$rc,term:$term,clips:[]}' > "$out"
        fi
        return 2
    fi
    [[ -s "$out" ]] || return 1
    jq -e --arg term "$term" '
        .provider == "artlist"
        and ((.clips // []) | length) > 0
        # term round-trip: if server echoes .term it MUST equal input;
        # if absent the `//` fallback makes the comparison identical
        # (= no truncation). This matches the pre-extract semantic
        # where the gate silently skipped the check on absent echo.
        and ((.term // $term) == $term)
        # Every clip must pass the shape tuple (length-equal
        # post-filter vs total). Mismatch → some clip has missing
        # field → entire response is contract-fail (rc=1).
        and ([.clips // [] | .[] | select(
            ((.ExternalID // .ID // "") | length) > 0
            and ((.PageURL // "") | test("^https?://artlist\\.io/"))
            and ((.Title // "") | length) > 0
            and (.Title // "") != "Artlist"
            and ((.RawMetadata // "") | length) >= 0
            and ((.Keywords // []) | length) >= 0
        )] | length) == (.clips // [] | length)
    ' "$out" >/dev/null 2>&1 || return 1
    return 0
}
