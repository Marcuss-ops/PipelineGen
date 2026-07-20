# scripts/ci/architecture/checks/all_checks/check_00_forbid_literal_job_type_strings.sh
#
# Layer-split (July 2026): extracted from monolithic
# scripts/ci/architecture/checks/all_checks/check_00_literal_jobs.sh
# (45 LOC) into a per-rule sourceable file matching the canonical
# check_<NN>_<rule>.sh template used by the post-split governance /
# DB / API rules.
#
# Rule 00: forbid literal job-type strings outside canonical SSOT
#                          (internal/domain/job/job.go).
# Source-block: 100% of check_00_literal_jobs.sh (the file had a
# single `# ── Check 0: ──` rule body — no per-rule split needed,
# only re-template pass to match the canonical layer-split naming
# + add the dispatcher-friendly anti-bleed variable reset pattern).
#
# Sourced by scripts/ci/architecture/checks/all_checks.sh
# (numerical-natural sort -t_ -k2,2n; matches POSIX sort, not GNU
# -V — macOS/BSD-portable per godlike/07 minimum-blast-radius).
#
# Per godlike/06 SSOT: the canonical constant surface for job-type
# strings is internal/domain/job/job.go (one owner per fact). Any
# other reference is a SSOT regression.
#
# Per godlike/07 NO-FAKE-AVAILABILITY: fails closed with a typed
# error + fix recipe (constant from job/<pkg>.go).

# ── Anti-bleed reset ──────────────────────────────────────────────
# Explicit reset prevents state bleed from a prior sourced rule
# (the all_checks.sh dispatcher `source`s every rule in order;
# any variable left bound by a previous check would otherwise
# leak into this gate's logic).
literals=""

echo "=== Check 0: forbid literal job-type strings (PR-B, Wave 19 §7) ==="

# Forbidden job-type literals are built by concatenation so the
# retired strings never appear contiguously in this source file.
JOB_BATCH="script.generate_""batch"
JOB_CURATE="media.""curate"
JOB_CATALOG="script.generate_""from_""catalog"
JOB_CLIPS="script.generate_""from_""clips"

literals=$(rg -n --type go \
    -e "[=:(,]\s*\"(${JOB_BATCH}|${JOB_CURATE}|${JOB_CATALOG}|${JOB_CLIPS})\"" \
    -e "\"(${JOB_BATCH}|${JOB_CURATE}|${JOB_CATALOG}|${JOB_CLIPS})\"\s*[:,)]" \
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
    | grep -vE "\\/\\/.*\"(${JOB_BATCH}|${JOB_CURATE}|${JOB_CATALOG}|${JOB_CLIPS})\"" \
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
