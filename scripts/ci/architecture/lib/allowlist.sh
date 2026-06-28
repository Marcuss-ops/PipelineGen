#!/usr/bin/env bash
# lib/allowlist.sh — Per-file allowlist parsing helper for the
# scripts/ci/architecture suite.
#
# Mirrors the convention used by scripts/ci-bypass-audit.sh and the
# per-bucket docs/migrations/*.txt: paths are one per line, comments
# + blanks stripped, sorted-unique. This file is sourced by checks/*.sh
# — it does NOT execute `set -e` (sourcing would propagate to caller).
#
# Functions:
#   allowlist_load <file>
#       Echo sorted-unique non-comment paths from <file>. Returns non-zero
#       with a message on STDERR if <file> is missing AND
#       ALLOWLIST_REQUIRED=1 in the caller environment.
#   allowlist_comm_diff <allowed_var> <actual_var>
#       Computes the symmetric diff: paths in <actual_var> NOT in
#       <allowed_var> (the "violations"). Both args are the NAMES of
#       shell variables holding the path lists (NOT the lists themselves).
#       Empty stdout = clean.

# allowlist_load <path>
allowlist_load() {
    local f="${1}"
    if [ ! -f "${f}" ]; then
        if [ "${ALLOWLIST_REQUIRED:-0}" = "1" ]; then
            echo "CI: required allowlist not found at ${f}" >&2
            echo "    This file is the contract for what the gate exempts." >&2
            echo "    Restoring it from git history is the only recovery path." >&2
            return 1
        fi
        return 0  # silently empty
    fi
    # Strip leading `#`-comments and blank lines, then sort + dedupe.
    grep -vE '^\s*(#|$)' "${f}" 2>/dev/null | sort -u || true
}

# allowlist_comm_diff <allowed_var> <actual_var>
# Prints paths in <actual_var> that are NOT in <allowed_var>. Variable
# names are passed (not values) so callers can keep the lists in scope.
allowlist_comm_diff() {
    local allowed_var="${1}"
    local actual_var="${2}"
    comm -13 \
        <(printf '%s\n' "${!allowed_var}" | grep . || true) \
        <(printf '%s\n' "${!actual_var}" | grep . || true) \
        || true
}
