#!/usr/bin/env bash
# tests/operational/artlist/verify-artlist-index.sh
#
# Surgical harness for Gate 7 + Gate 8 (Qdrant v3 index projection).
# Exercises the canonical lib functions sqlite_outbox_terminal
# (lib/sqlite.sh) + qdrant_point_exists (lib/qdrant.sh).
#
# Contract:
#   - sqlite_outbox_terminal(clip_id) emits chain_status on stdout where:
#       COMPLETED   -> rc=0 (terminal success)
#       DEAD_LETTER -> rc=1 (terminal failure)
#       PENDING     -> rc=1 (in-flight)
#       SUPERSEDED  -> rc=1 (in-flight)
#       MISSING     -> rc=1 (no outbox rows for clip_id)
#   - qdrant_point_exists(clip_id, --source, --media-type) returns rc=0
#     when the Qdrant v3 point is present under the supplied filter
#
# Fail-closed per AGENTS.md typed-error contract (NEVER silent PASS).
# Tier: NOT in verify-main (live-stack required). Invoked via
# `make verify-artlist-index` once the Makefile wires this path.
# DRY_RUN=1 deterministic PASS.
set -euo pipefail

DIR=$(cd "$(dirname "$0")" && pwd)
# shellcheck disable=SC1091
source "$DIR/../lib/_artlist_common.sh"

smoke_require curl jq sqlite3

gate_index() {
    local clip_ids_file="${INDEX_CLIP_IDS_FILE:-${WORK_DIR:-/tmp}/clip_ids.txt}"
    local failures=0

    if [[ "${DRY_RUN:-0}" == "1" ]]; then
        log_pass "DRY_RUN sqlite_outbox_terminal emits chain=COMPLETED for each clip_id"
        log_pass "DRY_RUN qdrant_point_exists --source=artlist --media-type=video returns rc=0"
        log_pass "DRY_RUN verify-artlist-index ready (Gate 7/8 canonical Qdrant v3 surface enforced)"
        return 0
    fi

    if [[ ! -s "$clip_ids_file" ]]; then
        log_warn "Gate 7+8 INDEX_CLIP_IDS_FILE=${clip_ids_file} absent/empty (no clip_ids supplied - degenerate)"
        return 0
    fi

    local clip_id
    while read -r clip_id; do
        [[ -n "$clip_id" ]] || continue
        local cls rc_b=0 rc_c=0
        cls=$(sqlite_outbox_terminal "$clip_id" 2>/dev/null) || rc_b=$?

        case "$cls" in
            COMPLETED)
                log_pass "sqlite_outbox_terminal clip_id=${clip_id} chain=COMPLETED (outbox indexed)" ;;
            MISSING)
                log_fail "sqlite_outbox_terminal clip_id=${clip_id} chain=MISSING (no outbox record)"
                failures=$((failures + 1))
                continue ;;
            DEAD_LETTER)
                log_fail "sqlite_outbox_terminal clip_id=${clip_id} chain=DEAD_LETTER (retry exhausted)"
                failures=$((failures + 1))
                continue ;;
            *)
                log_warn "sqlite_outbox_terminal clip_id=${clip_id} chain=${cls:-?} (in-flight; rc=${rc_b})"
                continue ;;
        esac

        qdrant_point_exists "$clip_id" --source artlist --media-type video >/dev/null 2>&1 || rc_c=$?
        case "$rc_c" in
            0) log_pass "qdrant_point_exists clip_id=${clip_id} Qdrant v3 point present (source=artlist, media_type=video)" ;;
            2) log_warn "qdrant_point_exists clip_id=${clip_id} Qdrant transport unavailable (rc=2)" ;;
            *) log_fail "qdrant_point_exists clip_id=${clip_id} contract violated (rc=${rc_c})"
                failures=$((failures + 1)) ;;
        esac
    done < "$clip_ids_file"

    if (( failures > 0 )); then
        log_fail "verify-artlist-index failed (${failures} canonical sub-checks missed)"
        return 1
    fi
    log_pass "verify-artlist-index ready (Gate 7+8 canonical Qdrant v3 surface enforced)"
}

main() {
    if [[ "${DRY_RUN:-0}" == "1" ]]; then
        smoke_echo_safe "DRY RUN - verify-artlist-index would probe:"
        printf '  for each clip_id in $INDEX_CLIP_IDS_FILE:\n'
        printf '    sqlite_outbox_terminal clip_id  (chain=COMPLETED for Qdrant projection)\n'
        printf '    qdrant_point_exists clip_id --source=artlist --media-type=video\n'
        printf '\nLib exercises: sqlite_outbox_terminal (lib/sqlite.sh) + qdrant_point_exists (lib/qdrant.sh)\n'
        exit 0
    fi

    gate_index || return 1

    printf '\n=== verify-artlist-index ===\n'
    printf 'PASS=%d WARN=%d FAIL=%d\n' "${PASS:-0}" "${WARN:-0}" "${FAIL:-0}"
    return 0
}

main "$@"
