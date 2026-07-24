#!/usr/bin/env bash
# tests/operational/lib/qdrant.sh — Qdrant reachability + point-existence
# helpers.
#
# Source-able. Sourced by tests/operational/artlist/07_index.sh per the
# runtime.sh refactor's source-order contract.
#
# Contract (July 2026, post-verify-* split):
#   - Exposes qdrant_point_exists / qdrant_collection_reachable. Both
#     are THIN STUBS as of this commit; full implementations ship in
#     subsequent followups.
#   - Targets $QDRANT_URL / $QDRANT_COLLECTION (inherited from
#     artlist_runtime.sh; if absent, stubs operate in pure-logical mode
#     and return success under dry-run only).
#
# Stub semantics (per AGENTS.md "fail closed" rule):
#   - Each helper validates its required args; missing args -> exit 2
#     with a [FATAL] stderr line.
#   - Under SMOKE_DRY_RUN=1: silently returns 0 (treats unreachable
#     collection as "would be checked, deferring"). Under non-dry-run:
#     emits [STUB] + return 1.

set -euo pipefail

# ── ANSI color defaults (defensive under `set -u` in dry-source) ─────────
# Sub-scripts source common.sh first which exports $RED / $YELLOW /
# $RESET. Stub libs may also be sourced in isolation (e.g. by a future
# regression net); defaulting here keeps the printf lines below from
# tripping set -u on $YELLOW-/- $RESET with no common.sh ancestor.
: "${YELLOW:=\033[33m}"
: "${RESET:=\033[0m}"
: "${RED:=\033[31m}"

# ── Internal arg-count guard ────────────────────────────────────────────
qdrant_required_args() {
    local need=$1; shift
    local got=0
    local _arg
    for _arg in "$@"; do
        [[ -n "$_arg" ]] && got=$((got + 1))
    done
    if (( got < need )); then
        printf '%s[FATAL]%s qdrant lib: %d required arg(s), got %d\n' \
            "$RED" "$RESET" "$need" "$got" >&2
        exit 2
    fi
}

# ── qdrant_point_exists — point lookup against $QDRANT_URL/.../points/- ───
# qdrant_point_exists POINT_ID [COLLECTION]
#   POINT_ID    qdrant point id (often = clip_id from the asset row)
#   COLLECTION  (optional, default $QDRANT_COLLECTION)
# Returns 0 if point exists, 1 if not (or not yet implemented).
qdrant_point_exists() {
    qdrant_required_args 1 "$@"
    local point_id="$1" collection="${2:-${QDRANT_COLLECTION:-}}"
    if [[ "${DRY_RUN:-0}" == "1" ]]; then
        return 0
    fi
    printf '%s[STUB]%s qdrant_point_exists(point_id=%q, collection=%q) — not yet implemented\n' \
        "$YELLOW" "$RESET" "$point_id" "$collection" >&2
    return 1
}

# ── qdrant_collection_reachable — collection existence + liveness ────────
# qdrant_collection_reachable [COLLECTION]
#   COLLECTION  (optional, default $QDRANT_COLLECTION)
# Returns 0 if collection is reachable AND exists, 1 if either fails.
qdrant_collection_reachable() {
    local collection="${1:-${QDRANT_COLLECTION:-}}"
    qdrant_required_args 1 "$collection"
    if [[ "${DRY_RUN:-0}" == "1" ]]; then
        return 0
    fi
    printf '%s[STUB]%s qdrant_collection_reachable(collection=%q) — not yet implemented\n' \
        "$YELLOW" "$RESET" "$collection" >&2
    return 1
}
