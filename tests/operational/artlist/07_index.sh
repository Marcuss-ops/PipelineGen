#!/usr/bin/env bash
# tests/operational/artlist/07_index.sh — Artlist DoD Gates 7 + 8 (SQLite outbox + Qdrant indexing).
#
# Reorg (July 2026): split out of tests/operational/artlist_e2e.sh (now a thin shim).
# Bundles two gates that probe the SQLite → Qdrant indexing pipeline, since
# they share the same per-clip row surface:
#   Gate 7 — SQLite single-row + outbox completed/superseded
#   Gate 8 — Qdrant point + POST /api/media/search returns the clip
#
# Implementation (next PR) delegates to lib/sqlite.sh::sqlite_outbox_terminal
# and lib/sqlite.sh::sqlite_clip_row for Gate 7, and lib/qdrant.sh::qdrant_point_exists
# for Gate 8. This sub-script just declares the surface so make verify-artlist-index
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
source "$DIR/../lib/sqlite.sh"
# shellcheck disable=SC1091
source "$DIR/../lib/qdrant.sh"



smoke_require curl jq sqlite3



# ── Gate 7 — SQLite + outbox integrity ──────────────────────────────────
# Spec (July 2026 DoD):
#   - per clip_id from Gate 4:
#       sqlite_clip_row (lib/sqlite.sh) returns a non-empty assets row
#   - per clip_id:
#       sqlite_outbox_terminal classifies the outbox chain as COMPLETED
#       (no DEAD_LETTER, no SUPERSEDED waiting on reindex)
gate_sqlite_outbox() {
    smoke_log_section "Gate 7 — SQLite + outbox integrity"
    log_info "[STUB] Gate 7 — implement next (will use lib/sqlite.sh::sqlite_outbox_terminal + sqlite_clip_row)"
}

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
        smoke_echo_safe "DRY RUN — index probes (Gates 7 + 8):"
        printf '  sqlite3 %s (assets row + outbox chain)\n' "$DB_PATH"
        printf '  POST %s/collections/%s/points/<clip_id> (Qdrant)\n' "$QDRANT_URL" "$COLLECTION"
        printf '  POST %s/api/media/search (returns clip_id in .results[])\n' "$BASE_URL"
        exit 0
    fi
    gate_sqlite_outbox || return 1
    gate_qdrant_search || return 1

    printf '\n============================================\n'
    printf '  07_index (Gates 7 + 8)\n'
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
