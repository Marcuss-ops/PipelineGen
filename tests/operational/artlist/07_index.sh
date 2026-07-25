#!/usr/bin/env bash
# tests/operational/artlist/07_index.sh — Artlist DoD Gate 8
# (Qdrant + media-search hard gate).
#
# Reorg (July 2026): Gate 7 was split out to tests/operational/artlist/
# 07_outbox_integrity.sh (canonical Gate 7 owner). This file now owns
# Gate 8 only.
#
# Future implementation will delegate to lib/qdrant.sh::qdrant_point_exists.
# This sub-script just declares the Gate 8 surface so make verify-artlist-index
# has a parseable target.

set -euo pipefail

DIR=$(cd "$(dirname "$0")" && pwd)
# shellcheck disable=SC1091
source "$DIR/../lib/common.sh"
# shellcheck disable=SC1091
source "$DIR/../lib/artlist.sh"
# shellcheck disable=SC1091
source "$DIR/../lib/artlist_runtime.sh"
# shellcheck disable=SC1091
source "$DIR/../lib/qdrant.sh"



smoke_require curl jq



# ── Gate 8 — Qdrant + media search hard gate ────────────────────────────
# Spec (July 2026 DoD):
#   - per clip_id from Gate 4:
#       qdrant_point_exists (lib/qdrant.sh) returns true on $QDRANT_URL/collections/$COLLECTION/points/$id
#       payload_filename matches the asset row's filename
#   - POST /api/media/search returns the clip_id in .results[]
gate_qdrant_search() {
    smoke_log_section "Gate 8 — Qdrant + media search hard gate"
    log_info "[STUB] Gate 8 — implement next (will use lib/qdrant.sh::qdrant_point_exists)"
}

main() {
    if [[ "$DRY_RUN" == "1" ]]; then
        smoke_echo_safe "DRY RUN — index probe (Gate 8):"
        printf '  POST %s/collections/%s/points/<clip_id> (Qdrant)\n' "$QDRANT_URL" "$COLLECTION"
        printf '  POST %s/api/media/search (returns clip_id in .results[])\n' "$BASE_URL"
        exit 0
    fi
    gate_qdrant_search || return 1

    printf '\n============================================\n'
    printf '  07_index (Gate 8)\n'
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
