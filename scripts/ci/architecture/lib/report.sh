#!/usr/bin/env bash
# lib/report.sh — Report formatters for the ci/architecture suite.
#
# Editorial wrappers around printf so every check has consistent
# headers and OK/FAIL lines. Sourced by checks/*.sh and checks.sh —
# does NOT execute `set -e` (would propagate to caller).
#
# Functions:
#   report_check_header <heading>    emit "=== <heading> ==="
#   report_ok <message>              emit "OK: <message>" (stdout)
#   report_fail <message>            emit "FAIL: <message>" (stderr)
#   report_info <message>            emit "INFO: <message>" (stdout)
#
# Exit-code aggregation is the caller's responsibility — each check
# itself emits "exit 1" on failure, and checks.sh halts at first
# non-zero (bit-perfect with the legacy single-fail-exit behaviour).

# report_check_header <heading>
report_check_header() {
    printf '=== %s ===\n' "${*}"
}

# report_ok <message>
report_ok() {
    printf 'OK: %s\n' "${*}"
}

# report_fail <message> — to stderr (so a CI log distinguishes
# failure from passing checksum output).
report_fail() {
    printf 'FAIL: %s\n' "${*}" >&2
}

# report_info <message>
report_info() {
    printf 'INFO: %s\n' "${*}"
}
