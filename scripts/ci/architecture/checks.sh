#!/usr/bin/env bash
# checks.sh — orchestrator for the architectural checks suite.
#
# Invoked by CI in place of the legacy scripts/ci-architectural-checks.sh.
# The .github/workflows/ci.yml switch to this entrypoint is staged in
# PR4 (separate followup) so PR1-N can be validated piecemeal without
# perturbing the canonical CI gate.
#
# Modes:
#   --self-check    regex pattern validation against tests/fixtures/zero_legacy
#   (no flag)       production gate: dispatch to checks/check_*.sh
#
# Exit codes:
#   0  all checks pass / self-check green
#   1  at least one check failed (or self-check failed)
#
# Property is preserved bit-perfect with the legacy single-fail-exit
# behaviour: each check exits 1 on its own failure and the orchestrator
# halts at the FIRST non-zero exit. The e2e harness scripts/ci-archcheck-e2e.sh
# only verifies exit-code (it grep-matches on canonical error strings,
# not order-of-failure), so this commitment + the legacy are equivalent
# to the e2e harness contract.
set -euo pipefail

# REPO_ROOT resolution: the legacy script refuses to silently misroute
# when invoked under process substitution. Mirror the same policy.
if [ -n "${BASH_SOURCE[0]:-}" ]; then
  SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
else
  echo "CI: cannot resolve script directory from BASH_SOURCE[0]=" >&2
  echo "    (process substitution / bash -c \"source ...\" invocation)." >&2
  echo "    Run as: bash scripts/ci/architecture/checks.sh" >&2
  exit 1
fi
REPO_ROOT="$(cd "${SCRIPT_DIR}/../../.." && pwd)"

# Export REPO_ROOT so child check_*.sh processes resolve it without
# having to recompute. Each check is launched as a fresh bash sub-process
# (`bash "${check_file}"` below) and would otherwise redo the cd dance.
export REPO_ROOT

# Source libraries (function definitions only — no side-effects, no
# `set -e` propagation). The libs are NOT executable scripts; they
# MUST be sourced.
. "${SCRIPT_DIR}/lib/allowlist.sh"
. "${SCRIPT_DIR}/lib/ripgrep.sh"
. "${SCRIPT_DIR}/lib/report.sh"

# Self-check mode — validate regex patterns against fixtures.
# `exec` replaces the current process so the selfcheck exit code
# propagates WITHOUT falling through into the production gate
# (a `bash … ; exit $?` pattern would still work, but exec is
# strictly cleaner: no opportunity for a future contributor to
# introduce a `set +e` block between the dispatch and the exit
# that swallows the failure).
if [ "${1:-}" = "--self-check" ]; then
    exec bash "${SCRIPT_DIR}/selfcheck.sh"
fi

# Production gate: dispatch to every check_*.sh under checks/. The
# directory MAY be empty (during incremental PR expansion); treat
# that as "all 0 checks passed".
NUM_CHECKS=0
for check_file in "${SCRIPT_DIR}/checks"/check_*.sh; do
    [ -e "${check_file}" ] || continue  # glob yielded nothing
    NUM_CHECKS=$((NUM_CHECKS + 1))
    echo "--- $(basename "${check_file}") ---"
    bash "${check_file}" || {
        echo "" >&2
        echo "Suite: $(basename "${check_file}") FAILED (halt at first failure)" >&2
        exit 1
    }
done

echo "Suite: ${NUM_CHECKS} check(s) PASSED"
