#!/usr/bin/env bash
# scripts/ci/architecture/checks/50_jobs.sh — slim orchestrator post-refactor.
#
# Replaces the original monolithic 2144-line 50_jobs.sh with a
# source-list iteration over 18 dedicated sub-check files
# (scripts/ci/architecture/checks/50_jobs_*.sh), each one a verbatim
# extraction of one of the original script's `Check N:` sections per
# the byte-precise line ranges in
# scripts/ci/architecture/checks/lib/50_jobs_section_map.json.
#
# Mirrors the precedent established by commit
# "refactor(ci): split ci-architectural-checks.sh (4618 => 77 + 59 lib)".
# Invariant: message-for-message, exit-code-for-exit-code byte parity
# with the original monolithic script (the user's
# "identici i messaggi di errore e i codici di uscita" contract).
#
# Invocation modes:
#   - `bash scripts/ci/architecture/checks/50_jobs.sh` direct invocation
#     (REPO_ROOT cwd is the user's responsibility — historically
#     invoked from CI where cwd=REPO_ROOT).
#   - `source` from scripts/ci/architecture/checks/all_checks.sh; the
#     `for sub in "${SUBCHECKS[@]}"; do bash "${SCRIPT_DIR}/../${sub}"; done`
#     loop runs each sub-check in its own subshell, exit codes
#     propagate via `|| exit 1`, and all_checks.sh's `set -e` aborts
#     the dispatcher on first failure (mirrors original behaviour).
#
# This file replaces the original monolithic; the original body lives
# in git history (commit prior to the split commit).

# ── Self-resolution ─────────────────────────────────────────────────
# Resolve SCRIPT_DIR from BASH_SOURCE[0]; BASH_SOURCE[0] equals the
# path of THIS file under both `bash` (direct) and `source` (sourced
# frames push a new BASH_SOURCE[0]) invocation modes. Required so
# that trampolined sub-checks (Check 44, Check 46) can resolve their
# own external script invocations via the per-section `${SCRIPT_DIR}`.
if [ -n "${BASH_SOURCE[0]:-}" ]; then
    SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
else
    echo "CI: cannot resolve 50_jobs sub-check directory from BASH_SOURCE[0]=" >&2
    exit 1
fi

# ── REPO_ROOT ───────────────────────────────────────────────────────
# Exported so sub-checks that need to invoke repo-root-scoped commands
# (e.g. `go vet ./internal/...` in Check 49, the ground-truth file
# scans in Check 54, the size-cap YAML SSOT read in the trampolined
# Check 44) resolve correctly from any invocation cwd. The original
# monolithic 50_jobs.sh inherited this same value from its caller
# (all_checks.sh) — the export restores the same fixity without
# depending on the source-call framing.
REPO_ROOT="$(cd "${SCRIPT_DIR}/../../../../.." && pwd)"
export REPO_ROOT

# ── Locale pinning for deterministic ripgrep output ordering ───────
# `rg --files` (and any rg invocation that depends on readdir order)
# can return hits in a different order across subprocess invocations
# when the parent shell inherits a non-C locale with a non-trivial
# collation table (LC_COLLATE=en_US.UTF-8 sorts locale-aware while
# LC_COLLATE=C sorts byte-exact). Pinning the C locale forces rg's
# readdir iteration to be byte-deterministic across runs, which is
# required for the user's "identici i messaggi di errore" contract
# (the verifier diffs baseline.out vs new.out at byte level).
# Safe for the canonical pipeline: the rg queries here operate on
# Go source code and ASCII-glob patterns only; no unicode collation
# is consumed downstream. The original monolithic 50_jobs.sh
# inherited whatever locale the user / CI runner injected; this
# pin removes that variability.
export LC_ALL="C" LANG="C" LC_COLLATE="C"

# ── Source the lib: SUBCHECKS registry + extracted helpers ─────────
# shellcheck source=/dev/null
source "${SCRIPT_DIR}/../lib/50_jobs_lib.sh"

# ── Iterate the registry: run each sub-check in canonical order ────
# Fail-fast on the first non-zero exit. Sub-shells isolate per-check
# env so a single failing check does not leave shared state behind
# (e.g. SHELLOPTS, command-line accumulator) that could mask a
# subsequent failure as a false-pass.
for sub in "${SUBCHECKS[@]}"; do
		bash "${SCRIPT_DIR}/../${sub}" || exit 1
done
