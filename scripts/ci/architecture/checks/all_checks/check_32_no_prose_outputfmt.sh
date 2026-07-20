# scripts/ci/architecture/checks/all_checks/check_32_no_prose_outputfmt.sh
#
# Layer-split (July 2026): extracted from monolithic
# scripts/ci/architecture/checks/all_checks/check_30_database.sh
# (170 LOC, 4 stacked rules).
#
# Rule 32: no prose OutputFmt in canonical production path.
# Source-block: lines ~52-77 of check_30_database.sh (pre-split).

# ── Anti-bleed reset ──────────────────────────────────────────────
literals=""

# Check 32 (no prose OutputFmt in canonical path): post-PR-6,
# the validator rejects OutputFmt=\"prose\" outright. Any
# production-code reference to the value is dead code or a
# regression; documentation comments in tests are excluded via
# the _test.go-with-comment pattern below.
echo "=== Check 32: no prose OutputFmt in canonical path (PR 9 / PR 6) ==="
literals=$(rg -n --type go \
    -e 'OutputFmt[[:space:]]*[:=][[:space:]]*"prose"' \
    -e 'output_fmt[[:space:]]*[:=][[:space:]]*"prose"' \
    -e "OutputFmt[[:space:]]*[:=][[:space:]]*'prose'" \
    -e "output_fmt[[:space:]]*[:=][[:space:]]*'prose'" \
    --glob '!**/*_test.go' \
    internal/application/scripts internal/domain/script 2>/dev/null \
    | awk -F: '{ rest = ""; for (i = 3; i <= NF; i++) rest = rest (i > 3 ? ":" : "") $i; if (rest ~ /^[[:space:]]*\/\//) next; print }' \
    || true)
if [ -n "$literals" ]; then
    echo "FAIL: OutputFmt \"prose\" detected in production path:"
    echo "$literals"
    exit 1
fi
echo "OK: no OutputFmt \"prose\" surface in canonical path"
