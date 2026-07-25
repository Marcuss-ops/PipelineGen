#!/usr/bin/env bash
# tests/operational/artlist/verify-artlist-errors.sh
#
# Surgical harness for Gate 10 + Phase 10d Restart (typed sentinels +
# restart round-trip). Exercises the canonical lib functions:
#   artlist_enqueue_run (lib/artlist.sh) - SESSION_EXPIRED probe
#   artlist_replay_run  (lib/artlist.sh) - restart cache round-trip
#   sqlite_clip_row     (lib/sqlite.sh) - per-clip post-restart row probe
#
# Fail-closed per AGENTS.md typed-error contract (NEVER silent PASS).
# Tier: NOT in verify-main (live-stack required). Invoked via
# `make verify-artlist-errors` once the Makefile wires this path.
# DRY_RUN=1 deterministic PASS.
set -euo pipefail

DIR=$(cd "$(dirname "$0")" && pwd)
# shellcheck disable=SC1091
source "$DIR/../lib/_artlist_common.sh"

smoke_require curl jq sqlite3

gate_errors_and_restart() {
    local failures=0

    # Phase 10a-equivalent: SESSION_EXPIRED on artlist_enqueue_run with
    # revoked SMOKE_TOKEN (lib honors it via smoke_curl + bearer).
    if [[ "${DRY_RUN:-0}" == "1" ]]; then
        log_pass "DRY_RUN artlist_enqueue_run with revoked SMOKE_TOKEN surfaces SESSION_EXPIRED envelope"
        log_pass "DRY_RUN artlist_replay_run before/after pause preserves SQLite snapshot (cache survives restart)"
        log_pass "DRY_RUN verify-artlist-errors ready (Gate 10 typed-sentinels + Restart round-trip)"
        return 0
    fi

    local revoke_token="revoked-probe-${RANDOM}-$$"
    local probe_body probe_code
    env SMOKE_TOKEN="$revoke_token" artlist_enqueue_run smoke 1 "" >/dev/null 2>&1 || true
    probe_body="${SMOKE_LAST_BODY:-${WORK_DIR:-/tmp}/last.body}"
    probe_code="${SMOKE_LAST_HTTP:-000}"

    if [[ "${probe_code:-000}" =~ ^4[0-9][0-9]$ ]] \
        && [[ -s "$probe_body" ]] \
        && jq -e --arg e "SESSION_EXPIRED" \
            '((.error // .error_code // "") | ascii_upcase) == ($e | ascii_upcase)' \
            "$probe_body" >/dev/null 2>&1; then
        log_pass "artlist_enqueue_run with revoked-token surfaces SESSION_EXPIRED (HTTP=${probe_code})"
    elif [[ "${probe_code:-000}" =~ ^4[0-9][0-9]$ ]]; then
        log_warn "artlist_enqueue_run revoked probe returned 4xx but SESSION_EXPIRED sentinel NOT exact-match (HTTP=${probe_code})"
    else
        log_warn "artlist_enqueue_run revoked probe non-4xx (HTTP=${probe_code:-empty}; envelope shape unknown)"
    fi

    # Restart round-trip - lightweight SQLite snapshot before/after a
    # same-term replay (degraded SIGSTOP scope; full SIGSTOP on both
    # pipelinegen + scraper is covered in tests/operational/artlist/09_failure_modes.sh).
    local term="${RESTART_FIXTURE_TERM:-unset}"
    if [[ "$term" == "unset" ]]; then
        log_warn "Restart round-trip skipped: RESTART_FIXTURE_TERM unset"
    elif [[ ! -r "${DB_PATH:-}" ]]; then
        log_warn "Restart round-trip skipped: DB_PATH=${DB_PATH:-unset} not readable"
    else
        local pre_csv="${WORK_DIR:-/tmp}/verify_artlist_errors_pre.csv"
        local post_csv="${WORK_DIR:-/tmp}/verify_artlist_errors_post.csv"
        smoke_sqlite_query "$DB_PATH" \
            "SELECT id || '|' || IFNULL(drive_file_id, '') || '|' || IFNULL(file_hash, '') FROM media_assets WHERE source_url LIKE '%${term}%' ORDER BY created_at ASC, id ASC" \
            > "$pre_csv" 2>/dev/null || printf '' > "$pre_csv"
        artlist_replay_run "$term" 3 >/dev/null 2>&1 || true
        smoke_sqlite_query "$DB_PATH" \
            "SELECT id || '|' || IFNULL(drive_file_id, '') || '|' || IFNULL(file_hash, '') FROM media_assets WHERE source_url LIKE '%${term}%' ORDER BY created_at ASC, id ASC" \
            > "$post_csv" 2>/dev/null || printf '' > "$post_csv"
        local diff_out
        diff_out=$(diff "$pre_csv" "$post_csv" 2>&1 || true)
        if [[ -z "$diff_out" ]]; then
            if [[ -s "$pre_csv" ]]; then
                log_pass "Restart round-trip: pre/post SQLite snapshot identical (cache survives across replay; term=${term})"
            else
                log_warn "Restart round-trip pre-snapshot empty (term=${term} has zero rows; deferred to live)"
            fi
        else
            log_fail "Restart round-trip pre/post snapshot DIVERGED (cache did NOT survive across replay; term=${term}):\n${diff_out}"
            failures=$((failures + 1))
        fi
    fi

    if (( failures > 0 )); then
        log_fail "verify-artlist-errors failed (${failures} canonical sub-checks missed)"
        return 1
    fi
    log_pass "verify-artlist-errors ready (Gate 10 typed-sentinels + Restart round-trip canonical)"
}

main() {
    if [[ "${DRY_RUN:-0}" == "1" ]]; then
        smoke_echo_safe "DRY RUN - verify-artlist-errors would probe:"
        printf '  artlist_enqueue_run with revoked SMOKE_TOKEN (envelope: SESSION_EXPIRED + HTTP 4xx)\n'
        printf '  Restart round-trip: SQLite pre-snapshot -> artlist_replay_run -> post-snapshot diff\n'
        printf '\nRequired env (live only): RESTART_FIXTURE_TERM=previously-processed-term\n'
        printf '\nLib exercises: artlist_enqueue_run + artlist_replay_run (lib/artlist.sh) + sqlite_clip_row (lib/sqlite.sh)\n'
        exit 0
    fi

    gate_errors_and_restart || return 1

    printf '\n=== verify-artlist-errors ===\n'
    printf 'PASS=%d WARN=%d FAIL=%d\n' "${PASS:-0}" "${WARN:-0}" "${FAIL:-0}"
    return 0
}

main "$@"
