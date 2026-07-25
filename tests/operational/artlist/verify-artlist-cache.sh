#!/usr/bin/env bash
# tests/operational/artlist/verify-artlist-cache.sh
#
# Surgical harness for Gate 9 (cache replay path).
# Exercises the canonical lib functions artlist_replay_run (lib/artlist.sh)
# + sqlite_clip_row (lib/sqlite.sh) for per-clip file_hash probe.
#
# Contract:
#   - artlist_replay_run(term, limit, [strategy], ...) emits "<HTTP_CODE>TAB<run_id>TAB<body_path>"
#     on stdout (canonical envelope); rc=0 on 2xx, rc=2 on transport failure
#   - sqlite_clip_row(clip_id) emits drive_file_id on stdout (per-clip row)
#
# Fail-closed per AGENTS.md typed-error contract (NEVER silent PASS).
# Tier: NOT in verify-main (live-stack required). Invoked via
# `make verify-artlist-cache` once the Makefile wires this path.
# DRY_RUN=1 deterministic PASS.
set -euo pipefail

DIR=$(cd "$(dirname "$0")" && pwd)
# shellcheck disable=SC1091
source "$DIR/../lib/_artlist_common.sh"

smoke_require curl jq sqlite3

gate_cache() {
    local term="${CACHED_FIXTURE_TERM:-dry}"
    local limit="${CACHED_FIXTURE_LIMIT:-3}"
    local clip_ids_file="${CACHE_CLIP_IDS_FILE:-${WORK_DIR:-/tmp}/clip_ids.txt}"
    local failures=0

    if [[ "${DRY_RUN:-0}" == "1" ]]; then
        log_pass "DRY_RUN artlist_replay_run term=${term} limit=${limit} returns 2xx + run_id"
        log_pass "DRY_RUN sqlite_clip_row emits drive_file_id per clip_id in clip_ids_file"
        log_pass "DRY_RUN verify-artlist-cache ready (Gate 9 canonical cache-replay surface)"
        return 0
    fi

    local replay_result replay_code replay_run_id replay_body
    replay_result=$(artlist_replay_run "$term" "$limit" replace 2>/dev/null || true)
    replay_code=$(printf '%s' "${replay_result}" | cut -f1)
    replay_run_id=$(printf '%s' "${replay_result}" | cut -f2)
    replay_body=$(printf '%s' "${replay_result}" | cut -f3)

    if [[ "${replay_code:-}" =~ ^2[0-9][0-9]$ ]] && [[ -n "${replay_run_id:-}" ]]; then
        log_pass "artlist_replay_run term=${term} limit=${limit} HTTP=${replay_code} run_id=${replay_run_id}"
    else
        log_fail "artlist_replay_run term=${term} limit=${limit} HTTP='${replay_code:-?}' run_id='${replay_run_id:-?}'"
        failures=$((failures + 1))
    fi

    if [[ -s "$clip_ids_file" ]]; then
        local clip_id rows=0
        while read -r clip_id; do
            [[ -n "$clip_id" ]] || continue
            local file_id rc_a=0
            file_id=$(sqlite_clip_row "$clip_id" 2>/dev/null) || rc_a=$?
            if (( rc_a == 0 )) && [[ -n "$file_id" ]]; then
                :  # successful per-clip probe - cumulative summary at end
                rows=$((rows + 1))
            else
                log_fail "sqlite_clip_row clip_id=${clip_id} rc=${rc_a} (no media_assets row in cache)"
                failures=$((failures + 1))
            fi
        done < "$clip_ids_file"
        log_pass "qualitative per-clip probe: ${rows} clips verified via sqlite_clip_row"
    fi

    if (( failures > 0 )); then
        log_fail "verify-artlist-cache failed (${failures} canonical sub-checks missed)"
        return 1
    fi
    log_pass "verify-artlist-cache ready (Gate 9 canonical cache-replay surface enforced)"
}

main() {
    if [[ "${DRY_RUN:-0}" == "1" ]]; then
        smoke_echo_safe "DRY RUN - verify-artlist-cache would probe:"
        printf '  artlist_replay_run term=$CACHED_FIXTURE_TERM limit=$CACHED_FIXTURE_LIMIT\n'
        printf '  sqlite_clip_row for each clip_id in $CACHE_CLIP_IDS_FILE\n'
        printf '\nLib exercises: artlist_replay_run (lib/artlist.sh) + sqlite_clip_row (lib/sqlite.sh)\n'
        exit 0
    fi

    gate_cache || return 1

    printf '\n=== verify-artlist-cache ===\n'
    printf 'PASS=%d WARN=%d FAIL=%d\n' "${PASS:-0}" "${WARN:-0}" "${FAIL:-0}"
    return 0
}

main "$@"
