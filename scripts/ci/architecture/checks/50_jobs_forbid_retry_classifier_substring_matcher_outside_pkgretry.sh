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
# ── Check 50: forbid retry-classifier substring-matcher outside pkg/retry (Step 7 closure, June 2026) ──
# The canonical transient-error classification lives in pkg/retry/retry.go
# (typed-path: TransientInfrastructureError + IsTransient + WrapTransient +
# transientSubstrings taxonomy + DefaultOptions with JitterFraction=0.25).
# Production classifiers MUST delegate to pkg/retry.IsTransient or wrap at the
# SDK / port exit via pkg/retry.WrapTransient. A function whose name matches
# one of the canonical retry-classifier tokens (IsTransient|isTransient|
# IsRetryable|isRetryable|ShouldRetry|shouldRetry) followed by an optional
# PascalCase suffix AND uses strings.Contains natively is a Step 7 SSOT
# regression: a substring-based classifier outside pkg/retry.
#
# Allowlist (hardcoded package-level + per-file transitional baseline):
#   pkg/retry                          — canonical home.
#   pkg/textutil                       — string manipulation helpers.
#   pkg/similarity                     — token-set similarity math.
#   docs/migrations/retry-classifier-  — per-file transitional baseline with
#     substring-allowlist.txt            explicit owner + deadline + rationale.
# Tests (_test.go files) excluded per the standard check convention.
#
# Migration plan for future offenders:
#   1. Wrap raw SDK / port error at the exit boundary via pkg/retry.WrapTransient.
#   2. Classify at the gate via pkg/retry.IsTransient (typed path first).
#   3. Delete local strings.Contains taxonomy; retry.IsTransient owns the list.
echo "=== Check 50: forbid retry-classifier substring-matcher outside pkg/retry (Step 7) ==="
# ── Transitional baseline (per-file allowlist) ─────────────────────
# Per AGENTS.md godlike/08 zero-baseline rule (mirrors Check 5 / Check 8 /
# Check 23 / Check 33). Every entry requires explicit owner + deadline +
# rationale documented inline. Migration of any entry to the canonical
# typed-path surface deletes the corresponding line from the allowlist.
declare -a retry_classifier_extras=()
if [ -f "docs/migrations/retry-classifier-substring-allowlist.txt" ]; then
  while IFS= read -r _line; do
    [[ -z "$_line" || "$_line" =~ ^[[:space:]]*# ]] && continue
    # Each entry is <path>\t# <owner> <deadline> <rationale>. Extract just
    # the first whitespace-delimited token (the path). Trailing inline
    # comments are owned by the file's per-entry documentation.
    _path=$(awk '{print $1}' <<< "$_line")
    [[ -z "$_path" || "$_path" =~ ^# ]] && continue
    retry_classifier_extras+=( -not -path "./${_path}" )
  done < docs/migrations/retry-classifier-substring-allowlist.txt
fi

violators=$(find . -name '*.go' -not -name '*_test.go' \
    -not -path '*/pkg/retry/*' \
    -not -path '*/pkg/textutil/*' \
    -not -path '*/pkg/similarity/*' \
    "${retry_classifier_extras[@]}" \
    -print0 2>/dev/null \
    | xargs -0 awk '
    BEGIN { in_classifier = 0 ; func_line = 0 }
    /^func[[:space:]]+(\([^)]*\)[[:space:]]+)?(IsTransient|isTransient|IsRetryable|isRetryable|ShouldRetry|shouldRetry)[A-Za-z0-9_]*[[:space:]]*\(/ && /err/ {
        in_classifier = 1
        func_line = FNR
        next
    }
    in_classifier && /strings\.Contains/ {
        print FILENAME ":" func_line ": " $0
        in_classifier = 0
    }
    /^}/ && in_classifier {
        in_classifier = 0
    }
    ' 2>/dev/null || true)
if [ -n "$violators" ]; then
    echo "FAIL: retry-classifier function uses strings.Contains natively outside pkg/retry:"
    echo "$violators"
    echo ""
    echo "Fix: delegate the substring classifier to pkg/retry.IsTransient (typed"
    echo "      path). Optionally wrap outgoing port errors via pkg/retry.WrapTransient"
    echo "      at the SDK / port exit so errors.As(err, *TransientInfrastructureError)"
    echo "      finds the typed carrier. Allowlist: pkg/retry (canonical home),"
    echo "      pkg/textutil, pkg/similarity, and the per-file transitional list at"
    echo "      docs/migrations/retry-classifier-substring-allowlist.txt."
    exit 1
fi
echo "OK: no retry-classifier substring-matchers outside pkg/retry"
