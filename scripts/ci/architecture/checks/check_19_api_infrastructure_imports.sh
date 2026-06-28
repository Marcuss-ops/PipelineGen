#!/usr/bin/env bash
# check_19_api_infrastructure_imports.sh — forbid infrastructure imports
# in the API layer.
#
# Scans internal/api/ for production Go files that import any package
# under github.com/Marcuss-ops/PipelineGen/internal/infrastructure/ and
# fails on any file NOT listed in the per-file allowlist at
# docs/migrations/api-infrastructure-imports-allowlist.txt.
#
# Symmetric comparison: both non-allowlisted imports AND stale allowlist
# entries with no matching import fail the gate (zero-baseline rule per
# AGENTS.md §8 ARCHITECTURE-CI-GATES).
#
# Mirrors scripts/ci-architectural-checks.sh::Check 19 verbatim so the
# canonical error signature ("forbidden infrastructure imports detected
# in API layer") is preserved bit-for-bit for scripts/ci-archcheck-e2e.sh
# (the E2E harness hard-codes a grep on this string and fails if missing).
#
# This is the canonical CI gate (cmd/archcheck --strict reads the same
# allowlist indirectly via Check 19); wearing it as the FIRST check in
# scripts/ci/architecture/checks/ guarantees the verification spec
# (a) clean repo exit 0; (b) sentinel import exit 1.
set -euo pipefail

# Source libs relative to this file's location.
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
. "${SCRIPT_DIR}/../lib/allowlist.sh"
. "${SCRIPT_DIR}/../lib/ripgrep.sh"
. "${SCRIPT_DIR}/../lib/report.sh"

REPO_ROOT="${REPO_ROOT:-$(cd "$(dirname "${BASH_SOURCE[0]}")/../../../.." && pwd)}"
ALLOWLIST_FILE="${REPO_ROOT}/docs/migrations/api-infrastructure-imports-allowlist.txt"

report_check_header "Check 19: forbid infrastructure imports in API layer"

# Collect all files in internal/api that import internal/infrastructure.
actual=$(rg -l --type go \
    'github\.com/Marcuss-ops/PipelineGen/internal/infrastructure/' \
    internal/api \
    --glob '!**/*_test.go' \
    2>/dev/null | sort || true)

# Build sorted allowlist from the file (strip comments + blank lines).
# Use the lib helper so the parsing convention matches the other
# checks AND any future allowlist-respect gate (zero-baseline, etc.).
allowed=$(allowlist_load "${ALLOWLIST_FILE}")

# Violations: files with infra imports NOT in the allowlist. Pipe
# through grep . to strip spurious blank lines from empty variable
# expansion (echo "" produces a newline that would otherwise hit the
# comm output as a false-positive blank entry).
violations=$(allowlist_comm_diff allowed actual)

# Stale entries: allowlist entries with NO matching infra import.
stale=$(comm -23 \
    <(printf '%s\n' "${allowed}" | grep . || true) \
    <(printf '%s\n' "${actual}" | grep . || true) \
    || true)

if [ -n "${violations}" ]; then
    echo "FAIL: forbidden infrastructure imports detected in API layer:"
    printf '%s\n' "${violations}" | sed 's/^/  /'
    echo ""
    echo "Fix: move the infrastructure dependency to a port in"
    echo "      internal/application/ and inject it at the composition root."
    echo "      If the import is grandfathered, add the file path to"
    echo "      ${ALLOWLIST_FILE} with owner + deadline per AGENTS.md §8."
    exit 1
fi
if [ -n "${stale}" ]; then
    echo "FAIL: stale allowlist entry with no matching infrastructure import:"
    printf '%s\n' "${stale}" | sed 's/^/  /'
    echo ""
    echo "Fix: remove the stale path from ${ALLOWLIST_FILE}. The import was"
    echo "      already removed from the source code; keeping a dead allowlist"
    echo "      entry masks future regressions. Per AGENTS.md §8 zero-baseline"
    echo "      rule, allowlist entries must exactly mirror the codebase."
    exit 1
fi

# Bit-perfect with the legacy OK string for e2e harness continuity.
report_ok "no infrastructure imports in API layer (0 actual, 0 allowed, symmetric clean)"
