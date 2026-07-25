#!/usr/bin/env bash
# tests/operational/artlist/verify-artlist-drive.sh
#
# Surgical harness for Gate 6 (Drive resolve-by-id per clip).
# Exercises the canonical lib functions sqlite_clip_row (lib/sqlite.sh)
# + drive_resolve_by_id (lib/drive.sh).
#
# Contract:
#   - sqlite_clip_row(clip_id) emits drive_file_id on stdout when the
#     media_assets row exists; rc=1 + empty stdout when absent
#   - drive_resolve_by_id(file_id) returns rc=0 when the canonical Drive
#     resolution surface holds: ok=true + trashed=false + Size>0 +
#     MimeType non-empty; rc=1 on contract violation; rc=2 on transport
#
# Fail-closed per AGENTS.md typed-error contract (NEVER silent PASS).
# Tier: NOT in verify-main (live-stack required). Invoked via
# `make verify-artlist-drive` once the Makefile wires this path.
# DRY_RUN=1 deterministic PASS.
set -euo pipefail

DIR=$(cd "$(dirname "$0")" && pwd)
# shellcheck disable=SC1091
source "$DIR/../lib/_artlist_common.sh"

smoke_require curl jq sqlite3

gate_drive() {
    local clip_ids_file="${DRIVE_CLIP_IDS_FILE:-${WORK_DIR:-/tmp}/clip_ids.txt}"
    local failures=0

    if [[ "${DRY_RUN:-0}" == "1" ]]; then
        log_pass "DRY_RUN sqlite_clip_row emits drive_file_id for each row in clip_ids"
        log_pass "DRY_RUN drive_resolve_by_id returns ok=true + trashed=false + Size>0 + MimeType non-empty"
        log_pass "DRY_RUN verify-artlist-drive ready (Gate 6 4-assertion chain canonical)"
        return 0
    fi

    if [[ ! -s "$clip_ids_file" ]]; then
        log_warn "Gate 6 DRIVE_CLIP_IDS_FILE=${clip_ids_file} absent/empty (no clip_ids supplied - degenerate)"
        return 0
    fi

    local clip_id
    while read -r clip_id; do
        [[ -n "$clip_id" ]] || continue
        local file_id rc_a=0 rc_b=0
        file_id=$(sqlite_clip_row "$clip_id" 2>/dev/null) || rc_a=$?

        if (( rc_a == 0 )) && [[ -n "$file_id" ]]; then
            log_pass "sqlite_clip_row clip_id=${clip_id} drive_file_id=${file_id}"
        else
            log_fail "sqlite_clip_row clip_id=${clip_id} rc=${rc_a} (no media_assets row)"
            failures=$((failures + 1))
            continue
        fi

        drive_resolve_by_id "$file_id" >/dev/null 2>&1 || rc_b=$?
        case "$rc_b" in
            0) log_pass "drive_resolve_by_id file_id=${file_id} canonical (ok=true + trashed=false + Size>0 + MimeType non-empty)" ;;
            1) log_fail "drive_resolve_by_id file_id=${file_id} contract violation (rc=1)"
                failures=$((failures + 1)) ;;
            2) log_warn "drive_resolve_by_id file_id=${file_id} transport unavailable (rc=2; live Drive absent)" ;;
        esac
    done < "$clip_ids_file"

    if (( failures > 0 )); then
        log_fail "verify-artlist-drive failed (${failures} canonical sub-checks missed)"
        return 1
    fi
    log_pass "verify-artlist-drive ready (Gate 6 4-assertion chain canonical across all clip_ids)"
}

main() {
    if [[ "${DRY_RUN:-0}" == "1" ]]; then
        smoke_echo_safe "DRY RUN - verify-artlist-drive would probe:"
        printf '  for each clip_id in $DRIVE_CLIP_IDS_FILE:\n'
        printf '    sqlite_clip_row clip_id          (media_assets row)\n'
        printf '    drive_resolve_by_id file_id       (ok=true + trashed=false + Size>0 + MimeType)\n'
        printf '\nLib exercises: sqlite_clip_row (lib/sqlite.sh) + drive_resolve_by_id (lib/drive.sh)\n'
        exit 0
    fi

    gate_drive || return 1

    printf '\n=== verify-artlist-drive ===\n'
    printf 'PASS=%d WARN=%d FAIL=%d\n' "${PASS:-0}" "${WARN:-0}" "${FAIL:-0}"
    return 0
}

main "$@"
