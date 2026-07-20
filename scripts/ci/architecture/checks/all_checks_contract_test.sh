#!/usr/bin/env bash
# scripts/ci/architecture/checks/all_checks_contract_test.sh — thin
# CI wrapper that invokes the dispatcher's `--contract-check` flag.
#
# Rationale: the dispatcher at scripts/ci/architecture/checks/
# all_checks.sh owns the contract-test logic. This wrapper exists
# as a CI-friendly entry-point that is grep-discoverable from
# external CI machinery (which often invokes a path-stable
# `scripts/…/<name>_test.sh` pattern rather than threading CLI
# flags). Self-resolves SCRIPT_DIR from BASH_SOURCE[0] so it works
# regardless of CWD and regardless of being sourced vs invoked.
#
# Exit-code contract (failure modes documented inline):
#   0  — all 3 contract conditions hold (header↔FS sync,
#        sourceable-without-crash, sort-key uniqueness).
#   1  — at least one condition violated (drift, crash, duplicate
#        sort keys). The dispatcher prints the reason before exit;
#        CI surfaces it via its standard log capture.
#
# Usage:
#   bash scripts/ci/architecture/checks/all_checks_contract_test.sh
#   # OR
#   source scripts/ci/architecture/checks/all_checks_contract_test.sh
#   (the latter just forwards into the dispatcher's contract check
#    without exiting the caller's shell — useful for ad-hoc local
#    sanity from a REPO_ROOT shell).

# ── Self-resolution ──────────────────────────────────────────────
# Resolve SCRIPT_DIR from BASH_SOURCE[0]; BASH_SOURCE[0] equals
# the path of THIS file under both `bash` (direct) and `source`
# (sourced frames push a new BASH_SOURCE[0]) invocation modes.
if [ -n "${BASH_SOURCE[0]:-}" ]; then
    SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
else
    echo "CI: cannot resolve wrapper directory from BASH_SOURCE[0]="${BASH_SOURCE[0]:-} >&2
    exit 1
fi

# ── Forward to dispatcher's contract check ───────────────────────
# The dispatcher IS the canonical owner of the contract test logic;
# this thin wrapper is just an entry-point convention. Pass-through
# without changing CLI semantics.
exec bash "${SCRIPT_DIR}/all_checks.sh" --contract-check
