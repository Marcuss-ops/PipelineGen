#!/usr/bin/env bash
# tests/operational/artlist/06_drive.sh — Artlist DoD Gate 6 (Drive resolve × N).
#
# Reorg (July 2026, Gate 6 lib/ migration): replaces the prior operator-facing
# folder-routing placeholder with the per-clip_id /api/drive/resolve-by-id
# 4-assertion gate, sourced per the lib/ reorg directive (sqlite_clip_row +
# drive_resolve_by_id as canonical lib helpers). The previous 6a (folder
# routing via velox_drive_resolve + ARTLIST_ROOT_FOLDER match) and 6b
# (drive.google.com URL round-trip) surface was a DIFFERENT contract and is
# replaced wholesale; the legacy gate_drive_resolve surface is removed.
#
# Workflow (matches the per-clip 4-assertion spec):
#   1. Read clip_id list from $CLIP_IDS_FILE (default
#      $WORK_DIR/expected_clip_ids.txt — the Gate 4 output convention).
#      Under DRY_RUN=1 the file is auto-seeded with a synthetic clip id
#      so the loop iterates reproducibly without a live Gate 4.
#   2. Per clip_id:
#      a. sqlite_clip_row   → SELECT drive_file_id FROM media_assets
#          (fail-closed if no drive_file_id present in the row).
#      b. drive_resolve_by_id → POST /api/drive/resolve-by-id
#          (forwarder to artlist_drive_resolve; body written to canonical
#          $WORK_DIR/artlist_drive_<file_id>.json.  DRY_RUN shape-
#          passthrough emits a synthetic body that PASSES the 4-assertion
#          contract so dev dry-runs gate without touching the network).
#      c. Assert the 4 DoD Gate 6 invariants on the response body:
#          (i)   id round-trip       .resolved[0].id == drive_file_id
#          (ii)  trashed=false       .resolved[0].trashed  eq literal false
#          (iii) mimeType non-void   .resolved[0].mimeType || .MimeType != ""
#          (iv)  size > 0            .resolved[0].size    > 0 (pure integer)
#
# Library: tests/operational/lib/_artlist_common.sh — canonical umbrella
# import (sources 7 lib files including the new real sqlite_clip_row +
# drive_resolve_by_id helpers, plus artlist.sh::artlist_drive_resolve
# which is the canonical SSOT for the curl chain).
#
# Fail-closed: any failing sub-step exits non-zero and aborts the gate.
# Tier: NOT in `make verify-main` (requires live PipelineGen + Drive +
# SQLite + populated media_assets table).  Live-stack at
# `make verify-artlist-drive` (this surgical gate) or
# `make verify-artlist-live` (all 10 gates via run_all.sh).
set -euo pipefail

DIR=$(cd "$(dirname "$0")" && pwd)
# shellcheck disable=SC1091
source "$DIR/../lib/_artlist_common.sh"

smoke_require curl jq sqlite3

# gate_drive_resolve_per_clip — per-clip 4-assertion chain described above.
gatedrive_resolve_per_clip() {
    smoke_log_section "Gate 6 — Drive resolve-by-id × N (id round-trip + trashed + mimeType + size)"
    local failures=0
    local clip_ids_file="${CLIP_IDS_FILE:-${WORK_DIR:-/tmp}/expected_clip_ids.txt}"

    # Phase 6.0: source-prepared `clip_ids_file` may be empty under DRY_RUN.
    # Auto-seed with a synthetic placeholder so dev dry-runs of
    # `make verify-artlist-drive` exercise the gate verdict determination
    # path without a live Gate 4 output.
    if [[ ! -s "$clip_ids_file" ]]; then
        if [[ "${DRY_RUN:-0}" == "1" ]]; then
            mkdir -p "$(dirname "$clip_ids_file")"
            printf 'dry-run-clip\n' > "$clip_ids_file"
            log_info "Gate 6 DRY_RUN: seeded $clip_ids_file with 'dry-run-clip'"
        else
            log_fail "Gate 6 cannot find clip_ids at $clip_ids_file (run Gate 4 first or set CLIP_IDS_FILE)"
            return 1
        fi
    fi
    local total
    total=$(wc -l < "$clip_ids_file" | tr -d ' ')

    local idx=0
    local clip_id drive_file_id rc asset_id trashed mime_type size body
    while IFS= read -r clip_id; do
        [[ -n "$clip_id" ]] || continue

        # Phase 6.1: extract drive_file_id from media_assets via sqlite_clip_row.
        # Fail-closed on empty result: a clip without a drive_file_id row is a
        # DoD Gate 6 violation (media_assets must have drive_file_id populated
        # for every clip that Gate 4 enqueued).
        drive_file_id=$(sqlite_clip_row "$clip_id" 2>/dev/null || true)
        if [[ -z "$drive_file_id" ]]; then
            log_fail "clip $clip_id → no drive_file_id from media_assets (sqlite_clip_row)"
            failures=$((failures + 1))
            idx=$((idx + 1))
            continue
        fi

        # Phase 6.2: drive_resolve_by_id POST + body written to canonical path.
        body="${WORK_DIR:-/tmp}/artlist_drive_${drive_file_id}.json"
        rc=0
        drive_resolve_by_id "$drive_file_id" 2>/dev/null || rc=$?
        if (( rc != 0 )); then
            case "$rc" in
                1) log_fail "clip $clip_id → drive_resolve_by_id contract violated (rc=1; see $body)" ;;
                2) log_fail "clip $clip_id → drive_resolve_by_id transport/HTTP failure (rc=2; see $body)" ;;
                *) log_fail "clip $clip_id → drive_resolve_by_id returned rc=$rc (see $body)" ;;
            esac
            failures=$((failures + 1))
            idx=$((idx + 1))
            continue
        fi

        # Phase 6.3: 4 DoD Gate 6 assertions on the resolved body.
        # Strict string eq on trashed (literal JSON false, not "false" wrapped);
        # tolerant field-name fallback for mimeType (Drive API uses both
        # `mimeType` canonical + `MimeType` legacy shape).
        asset_id=$(jq -r '.resolved[0].id // empty' "$body" 2>/dev/null || echo "")
        trashed=$(jq -r '.resolved[0].trashed // "?"' "$body" 2>/dev/null || echo "?")
        mime_type=$(jq -r '.resolved[0].mimeType // .resolved[0].MimeType // empty' "$body" 2>/dev/null || echo "")
        size=$(jq -r '.resolved[0].size // 0' "$body" 2>/dev/null || echo "0")
        if [[ -z "$asset_id" || "$asset_id" != "$drive_file_id" ]]; then
            log_fail "clip $clip_id → id round-trip violated: response=${asset_id:-empty} query=${drive_file_id}"
            failures=$((failures + 1))
        elif [[ "$trashed" != "false" ]]; then
            log_fail "clip $clip_id → trashed=${trashed} (want literal JSON false)"
            failures=$((failures + 1))
        elif [[ -z "$mime_type" || "$mime_type" == "null" ]]; then
            log_fail "clip $clip_id → mimeType empty/null (response.mimeType or response.MimeType required)"
            failures=$((failures + 1))
        elif ! [[ "$size" =~ ^[0-9]+$ ]] || (( size <= 0 )); then
            log_fail "clip $clip_id → size=${size} (want pure integer > 0)"
            failures=$((failures + 1))
        else
            log_pass "clip $clip_id → id=${asset_id} trashed=false mimeType=${mime_type} size=${size}B"
        fi
        idx=$((idx + 1))
    done < "$clip_ids_file"

    if (( failures > 0 )); then
        log_fail "Gate 6 /drive/resolve-by-id × ${total} failed (${failures} sub-checks missed)"
        return 1
    fi
    log_pass "Gate 6 /drive/resolve-by-id × ${total} clean (id round-trip + trashed + mimeType + size all green)"
}

main() {
    if [[ "${DRY_RUN:-0}" == "1" ]]; then
        smoke_echo_safe "DRY RUN — Gate 6 (per-clip 4-assertion chain):"
        printf '  clip_ids_file=%s (auto-seeded with "dry-run-clip" if empty under DRY_RUN)\n' "${CLIP_IDS_FILE:-${WORK_DIR:-/tmp}/expected_clip_ids.txt}"
        printf '  for clip_id in <file>:\n'
        printf '    6.1  sqlite_clip_row       SELECT drive_file_id FROM media_assets WHERE id=?\n'
        printf '    6.2  drive_resolve_by_id   POST /api/drive/resolve-by-id (forwarder to artlist_drive_resolve; DRY_RUN shape-passthrough)\n'
        printf '    6.3  assertions: id round-trip, trashed=false, mimeType non-void, size>0\n'
        printf '\nLib helpers exercised:\n'
        printf '  sqlite_clip_row       (lib/sqlite.sh :: SELECT single column drive_file_id)\n'
        printf '  drive_resolve_by_id   (lib/drive.sh :: forwarder + DRY_RUN shape)\n'
        printf '  artlist_drive_resolve (lib/artlist.sh canonical impl; do not edit)\n'
        exit 0
    fi

    gatedrive_resolve_per_clip || return 1

    printf '\n============================================\n'
    printf '  06_drive (per-clip 4-assertion chain)\n'
    printf '  PASS=%d  WARN=%d  FAIL=%d\n' "$PASS" "$WARN" "$FAIL"
    printf '============================================\n'
    if [[ "${FAIL:-0}" -gt 0 ]]; then
        printf 'VERDICT: FAIL\n'
        return 1
    fi
    printf 'VERDICT: PASS\n'
    return 0
}

main "$@"
