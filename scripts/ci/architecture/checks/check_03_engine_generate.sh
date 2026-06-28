#!/usr/bin/env bash
# check_03_engine_generate.sh — forbid engine.Generate() outside
# the canonical GenerateOneUseCase (PR-6, June 2026).
#
# Engine access must flow through the typed pipeline orchestrator
# GenerateOneUseCase; any direct engine.Generate( call in production
# code is a SSOT regression.
#
# Allowlist (mirrors legacy):
#   - generate_one_usecase.go : canonical caller (typed orchestrator)
#   - engine.go                : definition site
#   - *_test.go                : tests may call Generate for verification
#
# Mirrors scripts/ci-architectural-checks.sh::Check 3 verbatim.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
. "${SCRIPT_DIR}/../lib/allowlist.sh"
. "${SCRIPT_DIR}/../lib/ripgrep.sh"
. "${SCRIPT_DIR}/../lib/report.sh"

REPO_ROOT="${REPO_ROOT:-$(cd "$(dirname "${BASH_SOURCE[0]}")/../../../.." && pwd)}"

report_check_header "Check 3: forbid engine.Generate() outside GenerateOneUseCase (PR-6)"

literals=$(rg_strip_full_line_comments \
    -e '\bengine\.Generate\(' \
    --glob '!**/generate_one_usecase.go' \
    --glob '!**/engine.go' \
    --glob '!**/*_test.go' \
    -t go \
    internal/ 2>/dev/null) || true

if [ -n "${literals}" ]; then
    echo "FAIL: direct engine.Generate() call outside GenerateOneUseCase:"
    echo "${literals}"
    echo ""
    echo "Fix: route engine access through GenerateOneUseCase.Execute()."
    echo "The engine is the canonical script-generator; the sole production"
    echo "caller is generate_one_usecase.go. Handler code, resolvers, and"
    echo "postprocessors must NOT call engine.Generate() directly."
    exit 1
fi
report_ok "no direct engine.Generate() calls outside GenerateOneUseCase"
