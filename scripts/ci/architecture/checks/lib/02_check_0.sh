#!/usr/bin/env bash
# 02_check_0.sh — generated mirror of Check 0.

# ── Check 0: forbid literal job-type strings outside canonical SSOT ─────
echo "=== Check 0: forbid literal job-type strings (PR-B, Wave 19 §7) ==="
literals=$(rg -n --type go \
    -e '[=:(,]\s*"(script\.generate_batch|media\.curate|script\.generate_from_catalog)"' \
    -e '"(script\.generate_batch|media\.curate|script\.generate_from_catalog)"\s*[:,)]' \
    --glob '!**/domain/job/job.go' \
    --glob '!**/domain/media/job_types.go' \
    --glob '!**/kernel/script/**' \
    --glob '!**/*_test.go' \
    internal/ 2>/dev/null \
    | awk -F: '{
        rest = ""
        for (i = 3; i <= NF; i++) rest = rest (i > 3 ? ":" : "") $i
        if (rest ~ /^[[:space:]]*\/\//) next
        print
    }' \
    | grep -vE '\/\/.*"(script\.generate_batch|media\.curate|script\.generate_from_catalog)"' \
    || true)
if [ -n "$literals" ]; then
    echo "FAIL: literal job-type string found outside canonical SSOT:"
    echo "$literals"
    echo ""
    echo "Fix: replace the literal with the canonical domain constant."
    exit 1
fi
echo "OK: no guarded literal job-type strings outside canonical owners"
