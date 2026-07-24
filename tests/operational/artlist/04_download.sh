#!/usr/bin/env bash
# tests/operational/artlist/04_download.sh — Artlist DoD Gate 2 (POST /download + ffprobe hard gate).
#
# Reorg (July 2026): split out of tests/operational/artlist_e2e.sh (now a thin shim).
# DoD spec (July 2026): `Finché detail e download diretto non passano, non si
# lancia /api/artlist/run`. Hard-gate checks (fail-closed on miss):
#   - HTTP 2xx
#   - response: ok=true + clip_id non-empty + local_path non-empty
#   - local file exists at local_path
#   - file size > 0
#   - MIME == video/mp4
#   - ffprobe reads the file with the canonical DoD command and produces
#     format.duration > 0, format.size > 0, at least one stream with
#     width > 0 and height > 0.
#
# Implementation notes:
#   * /download consumes real Artlist quota. We isolate the artifact under
#     $WORK_DIR/gate2_dl/ so the existing smoke_cleanup trap on WORK_DIR
#     reaps the file when the battery exits.
#   * clip_page_url is sampled live from /api/artlist/search/live (same
#     pattern as Gate 1) so the test always exercises a real Artlist URL.
#   * Raw curl against $SCRAPER_URL (node-scraper does not speak the
#     PipelineGen bearer token / Idempotency-Key contract).
#   * Future refactor (post-reorg): /download probe delegates to
#     lib/artlist.sh::artlist_download once the helper wraps the ffprobe check.

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
SCRAPER_URL="${VELOX_ARTLIST_SCRAPER_SERVER_URL:-http://127.0.0.1:9123}"

if [[ -n "${LIVE_QUERIES:-}" ]]; then
    IFS='|' read -ra LIVE_QUERIES <<<"${LIVE_QUERIES}"
    if [[ ${#LIVE_QUERIES[@]} -ne 3 \
       || -z "${LIVE_QUERIES[0]:-}" \
       || -z "${LIVE_QUERIES[1]:-}" \
       || -z "${LIVE_QUERIES[2]:-}" ]]; then
        ts="$(date '+%Y-%m-%dT%H:%M:%S')"
        printf >&2 '[FAIL]  %s  LIVE_QUERIES env override must yield exactly 3 non-empty pipe-delimited terms; got %d slot(s): "%s"\n' \
            "$ts" "${#LIVE_QUERIES[@]}" "${LIVE_QUERIES[*]}"
        : "${WORK_DIR:=${TMPDIR:-/tmp}/artlist_e2e_validation}"
        if ! mkdir -p "$WORK_DIR" 2>/dev/null; then
            printf >&2 '[WARN]  %s  could not mkdir %s (validation artifact skipped)\n' \
                "$ts" "$WORK_DIR"
            exit 2
        fi
        if ! value_json=$(printf '%s\0' "${LIVE_QUERIES[@]}" | jq -Rs --argjson n "${#LIVE_QUERIES[@]}" \
            'split("\u0000") | map(if . == "" then null else . end) | .[:$n]'); then
            printf >&2 '[WARN]  %s  jq pipeline failed producing the value array (artifact dropped, exit 2 still enforced)\n' \
                "$ts"
            exit 2
        fi
        jq -nc --arg ts "$ts" --argjson slots "${#LIVE_QUERIES[@]}" \
            --argjson value "$value_json" \
            '{event:"live_queries_validation_failed",ts:$ts,slots:$slots,value:$value}' \
            > "$WORK_DIR/live_queries_validation_failed.json"
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

smoke_require curl jq file ffprobe

# Per-battery counters
PASS=0; WARN=0; FAIL=0
log_pass() { printf '[PASS]  %s %s\n' "$(date '+%H:%M:%S')" "$*"; PASS=$((PASS + 1)); }
log_warn() { printf '[WARN]  %s %s\n' "$(date '+%H:%M:%S')" "$*"; WARN=$((WARN + 1)); }
log_fail() { printf '[FAIL]  %s %s\n' "$(date '+%H:%M:%S')" "$*"; FAIL=$((FAIL + 1)); }
log_info() { printf '[INFO]  %s %s\n' "$(date '+%H:%M:%S')" "$*"; }

# ── Gate 2 — POST /download + ffprobe hard gate ───────────────────────
# DoD spec (July 2026): `Finché detail e download diretto non passano,
# non si lancia /api/artlist/run`. Hard-gate checks (fail-closed on miss):
#   - HTTP 2xx
#   - response: ok=true + clip_id non-empty + local_path non-empty
#   - local file exists at local_path
#   - file size > 0
#   - MIME == video/mp4
#   - ffprobe reads the file with the canonical DoD command and produces
#     format.duration > 0, format.size > 0, at least one stream with
#     width > 0 and height > 0.
#
# Implementation notes:
#   * /download consumes real Artlist quota. We isolate the artifact under
#     $WORK_DIR/gate2_dl/ so the existing smoke_cleanup trap on WORK_DIR
#     reaps the file when the battery exits.
#   * clip_page_url is sampled live from /api/artlist/search/live (same
#     pattern as Gate 1) so the test always exercises a real Artlist URL.
#   * Raw curl against $SCRAPER_URL (node-scraper does not speak the
#     PipelineGen bearer token / Idempotency-Key contract).
gate_direct_download() {
    smoke_log_section "Gate 2 — POST /download + ffprobe hard gate"
    local failures=0
    local out_dir="$WORK_DIR/gate2_dl"
    mkdir -p "$out_dir"

    # ── Phase 1: source a real clip_page_url from the live-search surface
    smoke_curl GET "/api/artlist/search/live?term=${LIVE_QUERIES[0]}&limit=5" >/dev/null
    if [[ ! "${SMOKE_LAST_HTTP:-}" =~ ^2[0-9][0-9]$ ]] \
       || ! jq -e '.clips // [] | length > 0' "${SMOKE_LAST_BODY:-/dev/null}" >/dev/null 2>&1; then
        log_fail "live search probe for /download failed (HTTP=${SMOKE_LAST_HTTP:-empty})"
        return 1
    fi
    local real_page_url
    real_page_url=$(jq -r '.clips[0].PageURL // empty' "${SMOKE_LAST_BODY:-/dev/null}")
    if [[ -z "$real_page_url" || ! "$real_page_url" =~ ^https://artlist\.io/ ]]; then
        log_fail "first live clip PageURL invalid: '$real_page_url'"
        return 1
    fi

    # ── Phase 2: POST /download (consumes Artlist quota)
    local dl_body="$WORK_DIR/gate2_download.json"
    local code
    code=$(curl -sS --max-time "$SMOKE_HTTP_TIMEOUT_SECONDS" \
        -X POST -H 'Content-Type: application/json' \
        -d "$(jq -nc --arg u "$real_page_url" --arg o "$out_dir" '{clip_page_url:$u, output_dir:$o}')" \
        "$SCRAPER_URL/download" -o "$dl_body" -w '%{http_code}' 2>/dev/null || echo 000)

    if [[ ! "$code" =~ ^2[0-9][0-9]$ ]]; then
        log_fail "POST /download HTTP=$code (expected 2xx) for $real_page_url"
        smoke_echo_safe "$(head -c 600 "$dl_body" 2>/dev/null || true)" >&2
        return 1
    fi

    if ! jq -e '.ok == true
        and ((.clip_id // "") | length) > 0
        and ((.local_path // "") | length) > 0' "$dl_body" >/dev/null 2>&1; then
        log_fail "/download response contract failed (want ok=true + clip_id non-empty + local_path non-empty)"
        smoke_echo_safe "$(head -c 800 "$dl_body" 2>/dev/null || true)" >&2
        failures=$((failures + 1))
    else
        log_pass "/download response contract: ok=true, clip_id+local_path present"
    fi

    # ── Phase 3: local-file + ffprobe assertions
    local local_path file_size mime_type
    local_path=$(jq -r '.local_path // empty' "$dl_body")
    if [[ -z "$local_path" || ! -f "$local_path" ]]; then
        log_fail "/download local file missing: '$local_path'"
        failures=$((failures + 1))
    else
        file_size=$(stat -c%s "$local_path" 2>/dev/null || echo 0)
        if [[ "$file_size" -le 0 ]]; then
            log_fail "/download local file size=$file_size (want >0) at $local_path"
            failures=$((failures + 1))
        else
            log_pass "/download local file size=${file_size}B at $local_path"
        fi

        mime_type=$(file -b --mime-type "$local_path" 2>/dev/null || true)
        if [[ "$mime_type" != "video/mp4" ]]; then
            log_fail "/download MIME=$mime_type (want video/mp4) at $local_path"
            failures=$((failures + 1))
        else
            log_pass "/download MIME=video/mp4"
        fi

        # DoD-exact ffprobe command: produces JSON with format.duration,
        # format.size, and streams[] each carrying codec_name/width/height.
        local ffprobe_json
        ffprobe_json=$(ffprobe -v error \
            -show_entries format=duration,size \
            -show_entries stream=codec_name,width,height \
            -of json "$local_path" 2>/dev/null || true)
        if [[ -z "$ffprobe_json" ]] || ! jq -e '
            (.format.duration // 0 | tonumber) > 0
            and (.format.size // 0 | tonumber) > 0
            and ([.streams[]?
                  | select((.width // 0 | tonumber) > 0 and (.height // 0 | tonumber) > 0)]
                 | length) >= 1' <<<"$ffprobe_json" >/dev/null 2>&1; then
            log_fail "ffprobe did not return duration>0+size>0+width>0+height>0 for $local_path"
            smoke_echo_safe "$(head -c 800 <<<"$ffprobe_json" 2>/dev/null || true)" >&2
            failures=$((failures + 1))
        else
            local duration size width height
            duration=$(jq -r '.format.duration // 0' <<<"$ffprobe_json")
            size=$(jq -r '.format.size // 0' <<<"$ffprobe_json")
            width=$(jq -r '[.streams[]?.width // 0 | tonumber] | max' <<<"$ffprobe_json")
            height=$(jq -r '[.streams[]?.height // 0 | tonumber] | max' <<<"$ffprobe_json")
            log_pass "ffprobe OK: duration=${duration}s size=${size}B largestStream=${width}x${height}"
        fi
    fi

    if (( failures > 0 )); then
        log_fail "Gate 2 /download + ffprobe hard gate failed (${failures} sub-checks)"
        return 1
    fi
    log_pass "Gate 2 /download + ffprobe hard gate clean"
}

main() {
    if [[ "$DRY_RUN" == "1" ]]; then
        smoke_echo_safe "DRY RUN — /download probes (Gate 2):"
        printf '  POST %s/detail (clip_page_url from LIVE_QUERIES[0], output_dir=$WORK_DIR/gate2_dl)\n' "$SCRAPER_URL"
        printf '  file --mime-type <local_path> (want video/mp4)\n'
        printf '  ffprobe -show_entries format=duration,size:stream=codec_name,width,height\n'
        exit 0
    fi
    gate_direct_download || return 1

    printf '\n============================================\n'
    printf '  04_download\n'
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
