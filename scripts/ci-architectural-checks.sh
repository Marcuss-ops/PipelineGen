#!/usr/bin/env bash
# scripts/ci-architectural-checks.sh — CI gate for the architectural checker.
#
# PR-A (June 2026): adds the `--future-ratchet` flag so the 5 Phase 0 rules
# (interface{}/any growth, setter detector, cross-package type alias,
# fake route, handler-to-DB) run in baseline-on-baseline ratchet mode.
# During the minor cycle (this script's current state) the gate fails ONLY
# on regressions vs scripts/archcheck/phase0_baseline.json; existing
# entries in the baseline are accepted.
#
# PR-B (June 2026): adds Check 0 — forbid literal job-type strings outside
# the canonical domain/job/job.go decl. Four canonical constants live there
# (TypeBatchScriptGenerate, TypeClipScriptGenerate, TypeCatalogScriptGenerate,
# TypeMediaCurate); every consumer should reference them by name. New
# quoted-string occurrences of those values outside the canonical decl are
# a SSOT regression and fail this gate.
#
# Promote-to-required checklist (separate follow-up PR):
#   1. Drop `--future-ratchet` from the command line below.
#   2. Fold runPhase0Checks() into runRatchetChecks() in
#      scripts/archcheck/main.go.
#   3. Update docs/architecture/godlike/14_INITIAL_BACKLOG.md — mark
#      Block 1 + the 5 Phase 0 rules as verified_zero: true.
set -euo pipefail

# ── Check 0: forbid literal job-type strings outside canonical SSOT ─────
# The 4 canonical constants carry string values:
#   "script.generate_batch"          (job.TypeBatchScriptGenerate)
#   "script.generate_from_clips"     (job.TypeClipScriptGenerate)
#   "script.generate_from_catalog"   (job.TypeCatalogScriptGenerate)
#   "media.curate"                   (job.TypeMediaCurate)
# Each canonical declaration lives in internal/domain/job/job.go. Any new
# rg hit on those strings as quoted STRING LITERALS (not comments) in
# production code indicates a regression — the canonical reference should
# always be the typed constant.
#
# PR-B (June 2026) closes the 4 script-related constants only. The
# remaining literal constants in internal/application/jobs/registry.go
# (TypeBulUploadYouTubeClips, TypeDriveFolderSync) and the other keys in
# internal/application/jobs/worker.go's timeout registry are intentionally
# out of PR-B scope and will be folded in a separate wave.
#
# Pattern anchors:
#   [=:(,]\s*"..."  — matches TypeBatchScriptGenerate = "...", Type: "...", func args
#   "...\"\s*[:,)]  — matches map keys ("...": NUMBER), trailing ,) cases
# Comment-only lines are excluded via awk so descriptive log strings
# ("handling foo job") don't trigger false positives. A second grep-vE
# belt-and-suspenders rejects inline comments where "// \"...\" ..."
# appears on a code line.
echo "=== Check 0: forbid literal job-type strings (PR-B, Wave 19 §7) ==="
literals=$(rg -n --type go \
    -e '[=:(,]\s*"(script\.generate_batch|media\.curate|script\.generate_from_catalog|script\.generate_from_clips)"' \
    -e '"(script\.generate_batch|media\.curate|script\.generate_from_catalog|script\.generate_from_clips)"\s*[:,)]' \
    --glob '!**/domain/job/job.go' \
    --glob '!**/*_test.go' \
    internal/ 2>/dev/null \
    | awk -F: '{
        rest = ""
        for (i = 3; i <= NF; i++) rest = rest (i > 3 ? ":" : "") $i
        if (rest ~ /^[[:space:]]*\/\//) next   # drop full-line comments
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

# ── Main gate ────────────────────────────────────────────────────
# Run the focused+ratchet archcheck; PR-A's `--future-ratchet` keeps the
# 5 Phase 0 rules in grace-cycle regression-detection mode.
go run ./scripts/archcheck --ratchet --future-ratchet
