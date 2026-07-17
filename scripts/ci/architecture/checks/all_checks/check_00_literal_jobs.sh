#!/usr/bin/env bash
# Check 0: forbid literal job-type strings (PR-B, Wave 19 §7)
#
# Extracted atomically from scripts/ci/architecture/checks/all_checks.sh
# (canonical SSOT: internal/domain/job/job.go).
# Sourced by the new dynamic-glob dispatcher loop in all_checks.sh
# (inserted before the load-bearing 30-40-50-60 iteration loop).
#
# Relies on ambient state set in all_checks.sh preamble:
#   REPO_ROOT  : absolute path to repo root
#   SCRIPT_DIR : absolute path to scripts/ci/architecture/checks/
# Resets cross-check state at top to prevent bleed from any previous
# extracted check (godlike/06 SSOT — one canonical owner per fact).

# Reset cross-check state explicitly (anti-bleed).
literals=""

echo "=== Check 0: forbid literal job-type strings (PR-B, Wave 19 §7) ==="
literals=$(rg -n --type go \
    -e '[=:(,]\s*"(script\.generate_batch|media\.curate|script\.generate_from_catalog|script\.generate_from_clips)"' \
    -e '"(script\.generate_batch|media\.curate|script\.generate_from_catalog|script\.generate_from_clips)"\s*[:,)]' \
    --glob '!**/domain/job/job.go' \
    --glob '!**/domain/media/job_types.go' \
    --glob '!**/domain/script/**' \
    --glob '!**/*_test.go' \
    internal/ 2>/dev/null \
    | awk -F: '{
        rest = ""
        for (i = 3; i <= NF; i++) rest = rest (i > 3 ? ":" : "") $i
        if (rest ~ /^[[:space:]]*\/\//) next
        print
    }' \
    | grep -vE '\/\/.*"(script\.generate_batch|media\.curate|script\.generate_from_catalog|script\.generate_from_clips)"' \
    || true)
if [ -n "$literals" ]; then
    echo "FAIL: literal job-type string found outside canonical SSOT:"
    echo "$literals"
    echo ""
    echo "Fix: replace the literal with the canonical constant from"
    echo "internal/domain/job/job.go (e.g. job.TypeBatchScriptGenerate)."
    echo "If the literal is required for documentation, wrap it in a"
    echo "backtick code span in prose, not in a string literal."
    exit 1
fi
echo "OK: no literal job-type strings outside canonical domain/job/job.go"
