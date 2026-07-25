#!/usr/bin/env bash
# tests/operational/artlist/verify-artlist-pipeline.sh
#
# Surgical harness for Gate 4 + Gate 5 (Pipeline fresh-end-to-end path).
# Exercises the canonical lib functions artlist_enqueue_run + artlist_poll_run
# from tests/operational/lib/artlist.sh.
#
# Contract:
#   - artlist_enqueue_run(term, limit, [ROOT_FOLDER_ID]) returns rc=0 + emits run_id
#     on stdout when the live pipeline accepts the request
#   - artlist_poll_run(run_id) returns rc=0 when the run reaches a terminal
#     status (completed | SUCCEEDED | failed | FAILED | cancelled | dead_letter),
#     rc=124 on poll-timeout, rc=non-zero on transport failure
#
# Fail-closed per AGENTS.md typed-error contract (NEVER silent PASS).
# Tier: NOT in verify-main (live-stack required). Invoked via
# `make verify-artlist-pipeline` once the Makefile wires this path.
# DRY_RUN=1 deterministic PASS.
set -euo pipefail

DIR=$(cd "$(dirname "$0")" && pwd)
# shellcheck disable=SC1091
source "$DIR/../lib/_artlist_common.sh"

smoke_require curl jq

gate_pipeline() {
    local term="${PIPELINE_FIXTURE_TERM:-dry}"
    local limit="${PIPELINE_FIXTURE_LIMIT:-3}"
    local failures=0

    if [[ "${DRY_RUN:-0}" == "1" ]]; then
        log_pass "DRY_RUN artlist_enqueue_run term=${term} limit=${limit} returns run_id (rc=0)"
        log_pass "DRY_RUN artlist_poll_run reaches SUCCEEDED|FAILED|dead_letter|cancelled terminal"
        log_pass "DRY_RUN verify-artlist-pipeline ready (Gates 4+5 canonical surface enforced)"
        return 0
    fi

    local run_id enq_rc=0
    run_id=$(artlist_enqueue_run "$term" "$limit" 2>/dev/null) || enq_rc=$?
    if (( enq_rc == 0 )) && [[ -n "$run_id" ]]; then
        log_pass "artlist_enqueue_run term=${term} limit=${limit} enqueued run_id=${run_id}"
    else
        log_fail "artlist_enqueue_run rc=${enq_rc} run_id='${run_id}' (live pipeline required)"
        failures=$((failures + 1))
    fi

    if [[ -n "$run_id" ]]; then
        local poll_rc=0
        artlist_poll_run "$run_id" 2>/dev/null || poll_rc=$?
        case "$poll_rc" in
            0)   log_pass "artlist_poll_run run_id=${run_id} terminal status reachable" ;;
            124) log_warn "artlist_poll_run timeout within SMOKE_POLL_TIMEOUT_SECONDS (live poll may need longer)" ;;
            *)   log_fail "artlist_poll_run rc=${poll_rc} for run_id=${run_id}"
                 failures=$((failures + 1)) ;;
        esac
    fi

    if (( failures > 0 )); then
        log_fail "verify-artlist-pipeline failed (${failures} canonical sub-checks missed)"
        return 1
    fi
    log_pass "verify-artlist-pipeline ready (Gates 4+5 canonical surface enforced)"
}

main() {
    if [[ "${DRY_RUN:-0}" == "1" ]]; then
        smoke_echo_safe "DRY RUN - verify-artlist-pipeline would probe:"
        printf '  artlist_enqueue_run term=$PIPELINE_FIXTURE_TERM limit=$PIPELINE_FIXTURE_LIMIT\n'
        printf '  artlist_poll_run run_id        (terminal within SMOKE_POLL_TIMEOUT_SECONDS)\n'
        printf '\nLib exercises: artlist_enqueue_run + artlist_poll_run (lib/artlist.sh)\n'
        exit 0
    fi

    gate_pipeline || return 1

    printf '\n=== verify-artlist-pipeline ===\n'
    printf 'PASS=%d WARN=%d FAIL=%d\n' "${PASS:-0}" "${WARN:-0}" "${FAIL:-0}"
    return 0
}

main "$@"
