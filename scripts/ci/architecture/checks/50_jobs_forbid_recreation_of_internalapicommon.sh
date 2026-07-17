#!/usr/bin/env bash
# 50_jobs sub-check (verbatim-extracted section of the original monolithic
# scripts/ci/architecture/checks/50_jobs.sh — see
# scripts/ci/architecture/checks/lib/50_jobs_section_map.json for the
# byte-precise line range, and the lib/50_jobs_profile.sh for the
# analysis that produced this split). Do NOT hand-edit body to fix
# checks; edit the original 50_jobs.sh and re-run the splitter (or
# move body content out-of-line manually here with a corresponding
# orchestrator update).

if [ -n "${BASH_SOURCE[0]:-}" ]; then
    SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
else
    echo "CI: cannot resolve sub-check directory from BASH_SOURCE[0]=" >&2
    exit 1
fi
# shellcheck source=/dev/null
source "${SCRIPT_DIR}/lib/50_jobs_lib.sh"

# ── Verbatim section body extracted from the original monolithic ────────
# ── Check 41: forbid recreation of internal/api/common/ (Issue 10, June 2026) ──
# internal/api/common/ was a compatibility stub with a duplicated OK helper.
# Removed in Issue 10 (June 2026). Any new import of the package or
# existence of the directory is a regression — the canonical helpers
# live in pkg/apiutil.
#
# This check fails if:
#   (a) internal/api/common/ directory exists, OR
#   (b) any production .go file imports ".../internal/api/common"
echo "=== Check 41: forbid recreation of internal/api/common/ (Issue 10) ==="
if [ -d "${REPO_ROOT}/internal/api/common" ]; then
    echo "FAIL: internal/api/common/ directory exists — delete it (removed in Issue 10, June 2026)"
    echo "      The canonical HTTP helpers live in pkg/apiutil."
    exit 1
fi
commonImports=$(rg -n --type go \
    -e 'github\.com/Marcuss-ops/PipelineGen/internal/api/common"' \
    --glob '!**/internal/api/common/**' \
    --glob '!**/*_test.go' \
    "${REPO_ROOT}" 2>/dev/null \
    || true)
if [ -n "$commonImports" ]; then
    echo "FAIL: import of internal/api/common detected (package was removed in Issue 10):"
    echo "$commonImports"
    echo ""
    echo "Fix: use pkg/apiutil instead. internal/api/common was a compatibility stub"
    echo "      with a duplicated OK helper — removed June 2026."
    exit 1
fi
echo "OK: internal/api/common/ is not present and no imports reference it"
