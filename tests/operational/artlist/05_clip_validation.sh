#!/usr/bin/env bash
# tests/operational/artlist/05_clip_validation.sh — Artlist DoD Gate 5
# (per-clip DB + file validation).
#
# Reorg (July 2026): extraction target per the DoD "estrazione futura"
# consolidation. The Gate 5 sub-invariants currently live inline inside
# tests/operational/artlist/05_pipeline_fresh.sh (which bundles Gates
# 4-9 ~900 lines). This file is the canonical home for Gate 5 once the
# extraction is performed; until then it's a forward-pointing stub
# that signals the per-gate file structure is in place.
#
# Spec (artlist_gates.md|Gate 5 verbatim):
#   per clip_id: smoke_sqlite_query → 18-invariant composite check
#   (source=artlist, media_type=video, lifecycle_state=PUBLISHED,
#    index_state=INDEXED, duration 6.5-8.5s, width=1920, height=1080,
#    drive_*, file_hash, source_provider, source_version,
#    metadata_origin=artlist, provider_tags, provider_categories,
#    discovered_by_queries ALL non-empty), then smoke_ffprobe_check
#    on local_path, then inline codec/container check.

set -euo pipefail

DIR=$(cd "$(dirname "$0")" && pwd)
# Source the umbrella (godlike/06 SSOT canonical import contract).
# shellcheck disable=SC1091
source "$DIR/../lib/_artlist_common.sh"

smoke_require curl jq sqlite3 ffprobe

# ── Gate 5 — Per-clip DB + file validation ──────────────────────────────
gate_clip_validation() {
    # TODO (extraction followup): lift the per-clip 18-invariant
    # composite from 05_pipeline_fresh.sh::gate_clip_validation_inline
    # (which is the current canonical owner until extraction lands).
    # Expected invocation surface:
    #   - consume ${WORK_DIR}/clip_ids.txt (Gate 4 hand-off, shared
    #     $WORK_DIR across all per-clip gates in run_all.sh)
    #   - per clip_id: smoke_sqlite_query $DB_PATH -json <composite SELECT>
    #     → jq -e on 18 invariants, exit 0/1
    #   - per clip_id: smoke_ffprobe_check $local_path 0
    #   - per clip_id: inline ffprobe codec/container check (h264 +
    #     mp4/mov/m4a container)
    #   - log_pass/log_fail cascade, all-clips-must-pass.
    log_info "[STUB] Gate 5 — extract per-clip 18-invariant composite + smoke_ffprobe_check from 05_pipeline_fresh.sh"
    return 0
}

main() {
    if [[ "$DRY_RUN" == "1" ]]; then
        smoke_echo_safe "DRY RUN — per-clip DB + file validation (Gate 5):"
        printf '  consume hand-off %s/clip_ids.txt from Gate 4\n' "$WORK_DIR"
        printf '  per clip_id: 18-invariant composite (source=artlist media_type=video lifecycle_state=PUBLISHED index_state=INDEXED duration 6.5-8.5s width=1920 height=1080 drive_* file_hash source_provider source_version metadata_origin=artlist provider_tags provider_categories discovered_by_queries)\n'
        printf '  per clip_id: smoke_ffprobe_check local_path 0 + inline codec/container check (h264 + mp4/mov/m4a)\n'
        printf '  ALL clips MUST pass.\n'
        exit 0
    fi
    gate_clip_validation || return 1

    printf '\n============================================\n'
    printf '  05_clip_validation\n'
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
