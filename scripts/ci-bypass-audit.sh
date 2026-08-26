#!/usr/bin/env bash
# scripts/ci-bypass-audit.sh — Asset-Mutation Bypass Audit gate.
#
# Wave 22 PR-4 (June 2026): re-runs the four rg queries against
# `internal cmd`, subtracts the per-file allowlist at
# `docs/migrations/admin-sql-allowlist.txt`, and exits non-zero if a
# non-allowlisted production hit appears. Every direct mutation call that
# is NOT on the allowlist is a regression against the canonical
# `mutations.AssetMutationDispatcher` SSOT.
#
# Why a per-file allowlist (rather than regex globs / line-pattern
# exceptions): the queries `\.Upsert\( / UpsertClip\( / \.Restore\(
# / \.HardDelete\(` are intentionally generic — `comm -13` against a
# sorted path list is the audit-style ratchet used by
# the retired API/infrastructure import policy and it
# produces stable, easy-to-grep failure output.
#
# Wired into `scripts/ci-architectural-checks.sh` as Check 7 (last entry)
# so the same workflow that runs the existing Wave 19/22 gates also runs
# the bypass audit. Allowlist edits are commit-locked: adding/removing
# an entry is a 1-line PR which is always reviewable.
set -euo pipefail

# Resolve REPO_ROOT once so the gate works from any cwd (CI runners,
# IDE hook invocations, manual bash). Same robustness pattern as
# scripts/ci-architectural-checks.sh.
if [ -n "${BASH_SOURCE[0]:-}" ]; then
  SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
else
  echo "CI: cannot resolve script directory from BASH_SOURCE[0]=" >&2
  echo "    Run the script as: bash scripts/ci-bypass-audit.sh" >&2
  exit 1
fi
REPO_ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"

ALLOWLIST="${REPO_ROOT}/docs/migrations/admin-sql-allowlist.txt"

# ── Load allowlist ─────────────────────────────────────────────────────
# Skip lines starting with # (comments) and blank lines. Sort and
# dedupe so the comm key works. Missing file = NO exemptions (the
# file is expected to be on disk; absent file is a clear migration
# regression caught by the reviewer).
allowed_paths=""
if [ ! -f "${ALLOWLIST}" ]; then
    echo "CI: allowlist not found at ${ALLOWLIST}" >&2
    echo "    This file is the contract for what bypass-survives; its" >&2
    echo "    absence means the gate cannot prove safety. Restoring the" >&2
    echo "    file from git history is the only recovery path." >&2
    exit 1
fi
allowed_paths=$(grep -vE '^\s*(#|$)' "${ALLOWLIST}" | sort -u)

# Helper: rg query that returns `path:line:` for every hit, then we
# pull the path component to feed to comm. The rg globs below mirror
# the once-pass auditor's flows — tests, primitive declarations, the
# admin purge package, and the entire infrastructure subtree are
# pruned before path-extraction so the comm key sizes stay small.
run_query() {
    local label="$1"; shift
    local pattern="$1"; shift
    local glob_args=( "$@" )

    local rg_hits
    rg_hits=$(rg -n --type go "${pattern}" "${glob_args[@]}" \
        "${REPO_ROOT}/internal" "${REPO_ROOT}/cmd" 2>/dev/null || true)

    if [ -z "${rg_hits}" ]; then
        return 0
    fi

    # Extract just the repo-root-relative path (cut on first colon) and
    # sort/uniq so the comm key is stable. Lines whose path is shorter
    # than 1 are noise.
    local hit_paths
    hit_paths=$(printf '%s\n' "${rg_hits}" \
        | awk -F: '{ if (NF >= 2) print $1 }' \
        | sed "s|^${REPO_ROOT}/||" \
        | sort -u)

    # comm -13: lines in hit_paths that are NOT in allowed_paths.
    local violators
    violators=$(comm -13 <(printf '%s\n' "${allowed_paths}") <(printf '%s\n' "${hit_paths}") || true)

    if [ -n "${violators}" ]; then
        echo "FAIL: ${label}" >&2
        echo "  non-allowlisted files with hits:" >&2
        printf '    %s\n' ${violators} >&2
        echo "  offending lines (file:line + context):" >&2
        # Build a regex of RELATIVE violator paths and grep the already-
        # relativised rg_hits stream (stripped of REPO_ROOT below). This
        # avoids the ${REPO_ROOT} single-quote escape problem in the
        # earlier sed-based pre-pend approach.
        violator_regex=$(printf '%s' "${violators}" | paste -sd'|' -)
        printf '%s\n' "${rg_hits}" \
            | sed "s|^${REPO_ROOT}/||" \
            | grep -E "^(${violator_regex}):" >&2 || true
        echo "" >&2
        echo "  Fix: rewrite the mutation to flow through" >&2
        echo "  mutations.AssetMutationDispatcher.{EnqueueAndIndex,EnqueueAndRestore,EnqueueAndDelete}" >&2
        echo "  or add an allowlist entry to ${ALLOWLIST} with a clear" >&2
        echo "  bucket header comment." >&2
        return 1
    fi
    echo "OK: ${label} (0 non-allowlisted hits)" >&2
    return 0
}

failures=0

# ── Check A: rg1 `\.Upsert\(` — generic `.Upsert(` callers ─────────────
# Excluded: tests, primitives decl site, admin package, infrastructure.
# What remains: production callers (api/, application/, app/, cmd/ non-admin)
# and composition-root adapters.
run_query "Check A: \`.Upsert(\` non-allowlisted hits" '\.Upsert\(' \
    --glob '!**/*_test.go' \
    --glob '!**/mutations/primitives.go' \
    --glob '!**/admin/purge*.go' \
    --glob '!**/platform/sqlite/**' \
    || failures=$((failures + 1))

# ── Check B: rg2 `UpsertClip\(` — direct calls to dispatcher-only primitive ──
# Looking for stray callers in production code. The interface-decl site
# (mutations/primitives.go) and the canonical implementation site
# (ClipsRepository.UpsertClip) are the only legitimate locations.
run_query "Check B: \`UpsertClip(\` non-allowlisted hits" 'UpsertClip\(' \
    --glob '!**/*_test.go' \
    --glob '!**/mutations/primitives.go' \
    --glob '!**/admin/purge*.go' \
    --glob '!**/platform/sqlite/**' \
    || failures=$((failures + 1))

# ── Check C: rg3 `\.Restore\(` — restore-path callers ───────────────────
# Admin tooling (cmd/admin/, internal/admin/) is the ONLY legitimate
# surface. No production caller should reach `.Restore(` directly.
run_query "Check C: \`.Restore(\` non-allowlisted hits" '\.Restore\(' \
    --glob '!**/*_test.go' \
    --glob '!**/admin/purge*.go' \
    --glob '!**/platform/sqlite/**' \
    || failures=$((failures + 1))

# ── Check D: rg4 `\.HardDelete\(` — physical-purge callers ─────────────
# Same shape as Check C: admin tooling only.
run_query "Check D: \`.HardDelete(\` non-allowlisted hits" '\.HardDelete\(' \
    --glob '!**/*_test.go' \
    --glob '!**/admin/purge*.go' \
    --glob '!**/platform/sqlite/**' \
    || failures=$((failures + 1))

# ── Check E: rg5 `ExecContext.*media_assets` — raw-SQL anti-pattern ───
# This query is intentionally strict: any hit is a regression because
# the canonical write path for media_assets is mutations.AssetMutationDispatcher,
# NOT db.ExecContext. The check serves as a regression guard against
# silent re-introduction of the antipattern (QDRANT-001 closure).
echo "=== Check E: raw-SQL antipattern regression guard (QDRANT-001) ==="
# Tests are excluded via `--glob '!**/*_test.go'` (mirrors Checks A-D): the
# asset_committer_ssot_test.go and similar persistence contract tests do
# legitimately need raw-SQL fixtures to assert the dispatcher's commit
# envelope, and the QDRANT-001 closure explicitly carved them out as the
# canonical allowlisted test-fixture surface. Without this glob the gate
# becomes a false-positive trip that confuses the lifecycle of the test
# suite with regressions against the production mutation SSOT.
raw_sql=$(rg -n --type go 'ExecContext.*media_assets' \
    --glob '!**/*_test.go' \
    "${REPO_ROOT}/internal/application" "${REPO_ROOT}/internal/api" 2>/dev/null || true)
if [ -n "${raw_sql}" ]; then
    echo "FAIL: raw-SQL antipattern re-introduced:"
    echo "${raw_sql}"
    echo ""
    echo "Fix: rewrite the call to flow through"
    echo "mutations.AssetMutationDispatcher.EnqueueAndIndex; the atomic"
    echo "UPSERT + outbox envelope is the only sanctioned write path for"
    echo "media_assets (per QDRANT-001)."
    failures=$((failures + 1))
else
    echo "OK: no raw-SQL antipattern regression (0 hits)"
fi

if [ "${failures}" -gt 0 ]; then
    echo ""
    echo "Bypass audit: ${failures} gate(s) FAILED"
    exit 1
fi

echo ""
echo "Bypass audit: 5 gates pass; ${REPO_ROOT}/docs/migrations/admin-sql-allowlist.txt is the SSOT"
