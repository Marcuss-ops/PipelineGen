#!/usr/bin/env bash
# check_00_job_type_strings.sh — forbid literal job-type strings outside
# the canonical domain/job/job.go decl.
#
# The 4 canonical constants (TypeBatchScriptGenerate,
# TypeClipScriptGenerate, TypeCatalogScriptGenerate, TypeMediaCurate)
# carry string values that MUST NOT be re-introduced as quoted STRING
# LITERALS in production code. Any new rg hit on those strings as
# quoted STRING LITERALS outside internal/domain/job/job.go is a SSOT
# regression and fails this gate.
#
# Mirrors scripts/ci-architectural-checks.sh::Check 0 (PR-B, June 2026,
# Wave 19 §7) bit-perfect for the FAIL signature so scripts/ci-archcheck-e2e.sh
# regressions are caught identically.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
. "${SCRIPT_DIR}/../lib/allowlist.sh"
. "${SCRIPT_DIR}/../lib/ripgrep.sh"
. "${SCRIPT_DIR}/../lib/report.sh"

REPO_ROOT="${REPO_ROOT:-$(cd "$(dirname "${BASH_SOURCE[0]}")/../../../.." && pwd)}"

report_check_header "Check 0: forbid literal job-type strings (PR-B, Wave 19 §7)"

# Two rg patterns cover assignment / arg / map-key forms. The awk
# pre-pass in lib/ripgrep.sh::rg_strip_full_line_comments drops hits
# whose content line is a full-line // comment; the post-pass grep
# additionally drops hits where the literal appears in an INLINE
# comment of a real code line.
literals=$(rg_strip_full_line_comments \
    -e '[=:(,]\s*"(script\.generate_batch|media\.curate|script\.generate_from_catalog|script\.generate_from_clips)"' \
    -e '"(script\.generate_batch|media\.curate|script\.generate_from_catalog|script\.generate_from_clips)"\s*[:,)]' \
    --glob '!**/domain/job/job.go' \
    --glob '!**/*_test.go' \
    -t go \
    internal/ 2>/dev/null) || true
literals=$(printf '%s\n' "${literals}" \
    | grep -vE '//.*"(script\.generate_batch|media\.curate|script\.generate_from_catalog|script\.generate_from_clips)"' \
    || true)

if [ -n "${literals}" ]; then
    echo "FAIL: literal job-type string found outside canonical SSOT:"
    echo "${literals}"
    echo ""
    echo "Fix: replace the literal with the canonical constant from"
    echo "internal/domain/job/job.go (e.g. job.TypeBatchScriptGenerate)."
    echo "If the literal is required for documentation, wrap it in a"
    echo "backtick code span in prose, not in a string literal."
    exit 1
fi
report_ok "no literal job-type strings outside canonical domain/job/job.go"
