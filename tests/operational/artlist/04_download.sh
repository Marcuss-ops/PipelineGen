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
#     format.duration > 0, format.size > 0, .streams[0].width > 0 and
#     .streams[0].height > 0 (DoD-exact jq contract on the FIRST stream).
#
# Implementation notes:
#   * /download consumes real Artlist quota. We isolate the artifact under
#     $WORK_DIR/gate2_dl/ so the existing smoke_cleanup trap on WORK_DIR
#     reaps the file when the battery exits.
#   * clip_page_url is sampled live from /api/artlist/search/live (same
#     pattern as Gate 1) so the test always exercises a real Artlist URL.
#   * Raw curl against $SCRAPER_URL (node-scraper does not speak the
#     PipelineGen bearer token / Idempotency-Key contract).
#   * DoD-exact ffprobe command (verbatim from `tests/operational/artlist_gates.md`):
#         ffprobe -v error \
#           -show_entries format=duration,size \
#           -show_entries stream=codec_name,width,height \
#           -of json "$LOCAL_PATH"
#   * shellcheck disable=SC1091 is consumed by the lib/ sources below.

set -euo pipefail

DIR=$(cd "$(dirname "$0")" && pwd)
# shellcheck disable=SC1091
source "$DIR/../lib/common.sh"
# shellcheck disable=SC1091
source "$DIR/../lib/artlist.sh"
# shellcheck disable=SC1091
source "$DIR/../lib/artlist_runtime.sh"



# Canonical LIVE_QUERIES resolution (lib/artlist_runtime.sh). The helpers
# below replace ~40 lines of inline validation copy-pasted from
# artlist_live_queries_validate — reusing them keeps the failure-mode
# semantics, the WORK_DIR artifact, and the canonical 3-term defaults
# from drifting between sub-scripts. Fail-closed on malformed override.
artlist_live_queries_validate
artlist_live_queries_default

smoke_require curl jq file ffprobe



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
    # Migrated from inline curl+jq to lib/artlist.sh::artlist_download
    # (DoD refactor July 2026).  The helper owns the canonical /download
    # response contract (ok=true + clip_id non-empty + local_path non-empty)
    # and returns 0/1/2 (pass / contract / transport).  The file-existence,
    # MIME, and ffprobe probes stay at the gate layer for richer diagnostic
    # logging + the per-clip metric counters the verdict banner surfaces.
    local dl_body="$WORK_DIR/gate2_download.json"
    local rc=0
    artlist_download --clip-page-url "$real_page_url" \
        --scraper-url "$SCRAPER_URL" \
        --output-dir "$out_dir" \
        --save-body "$dl_body" || rc=$?
    case "$rc" in
        0)
            log_pass "/download response contract: ok=true, clip_id+local_path present (artlist_download)"
            ;;
        2)
            log_fail "/download transport/HTTP error (rc=2) for $real_page_url (artlist_download)"
            smoke_echo_safe "$(head -c 600 "$dl_body" 2>/dev/null || true)" >&2
            failures=$((failures + 1))
            ;;
        *)
            log_fail "/download response contract violated for $real_page_url (artlist_download)"
            smoke_echo_safe "$(head -c 800 "$dl_body" 2>/dev/null || true)" >&2
            failures=$((failures + 1))
            ;;
    esac

    # ── Phase 3: local-file + ffprobe assertions
    local local_path file_size mime_type
    local_path=$(jq -r '.local_path // empty' "$dl_body" 2>/dev/null)
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

        # DoD-exact ffprobe contract via canonical lib helper.
        # smoke_ffprobe_check (lib/common.sh) already enforces:
        #   duration >= $min_dur + size > 0 + ≥1 video stream with width>0 && height>0.
        # Pass min_dur=0.5 so duration=0/0.5s files fail closed.  Per-clip
        # width/height diagnostic is dropped (was inline before this
        # migration) — the helper returns 0/1 boolean; forensic dump of
        # the ffprobe JSON is intentionally not surfaced to keep log
        # noise below threshold.
        if ! smoke_ffprobe_check "$local_path" 0.5; then
            log_fail "ffprobe contract violated for $local_path (smoke_ffprobe_check; want duration≥0.5s + size>0 + ≥1 valid video stream)"
            failures=$((failures + 1))
        else
            log_pass "ffprobe OK: duration≥0.5s + size>0 + ≥1 valid video stream (smoke_ffprobe_check)"
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
