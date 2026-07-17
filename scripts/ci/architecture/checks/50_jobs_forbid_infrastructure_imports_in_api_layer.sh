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
# ── Check 19: forbid infrastructure imports in API layer ──
# Scans internal/api/ for production Go files that import
# github.com/Marcuss-ops/PipelineGen/internal/infrastructure/
# and fails on any file NOT listed in the per-file allowlist at
# docs/migrations/api-infrastructure-imports-allowlist.txt.
# Symmetric comparison: both non-allowlisted imports AND stale
# allowlist entries with no matching import fail the gate.
#
# This gate enforces AGENTS.md Pattern 8 (API package: thin transport
# only). The API layer MUST NOT import database/sql, Google Drive SDK,
# FFmpeg/process execution, or any other infrastructure concrete.
# Infrastructure dependencies must flow through typed ports in
# internal/application/ and be injected at the composition root.
#
# Zero-baseline: as of P0.6 (June 2026), the API layer has ZERO
# infrastructure imports. Any new import fails this gate.
echo "=== Check 19: forbid infrastructure imports in API layer ==="
allowlist_file="docs/migrations/api-infrastructure-imports-allowlist.txt"

# Collect all files in internal/api that import internal/infrastructure
actual=$(rg -l --type go \
    'github\.com/Marcuss-ops/PipelineGen/internal/infrastructure/' \
    internal/api \
    --glob '!**/*_test.go' \
    2>/dev/null | sort || true)

# Build sorted allowlist from the file (strip comments + blank lines)
allowed=$(grep -vE '^\s*(#|$)' "$allowlist_file" 2>/dev/null | sort || true)

# Violations: files with infra imports NOT in the allowlist.
# Pipe through grep . to strip spurious blank lines from empty
# variable expansion (echo "" produces a newline that would
# otherwise hit the comm output as a false-positive blank entry).
violations=$(comm -13 <(echo "$allowed" | grep .) <(echo "$actual" | grep .) 2>/dev/null || true)

# Stale entries: allowlist entries with NO matching infra import
stale=$(comm -23 <(echo "$allowed" | grep .) <(echo "$actual" | grep .) 2>/dev/null || true)

if [ -n "$violations" ]; then
    echo "FAIL: forbidden infrastructure imports detected in API layer:"
    echo "$violations"
    echo ""
    echo "Fix: move the infrastructure dependency to a port in"
    echo "      internal/application/ and inject it at the composition root."
    echo "      If the import is grandfathered, add the file path to"
    echo "      $allowlist_file with owner + deadline per AGENTS.md §8."
    exit 1
fi
if [ -n "$stale" ]; then
    echo "FAIL: stale allowlist entry with no matching infrastructure import:"
    echo "$stale"
    echo ""
    echo "Fix: remove the stale path from $allowlist_file. The import was"
    echo "      already removed from the source code; keeping a dead allowlist"
    echo "      entry masks future regressions. Per AGENTS.md §8 zero-baseline"
    echo "      rule, allowlist entries must exactly mirror the codebase."
    exit 1
fi
echo "OK: no infrastructure imports in API layer (0 actual, 0 allowed, symmetric clean)"
