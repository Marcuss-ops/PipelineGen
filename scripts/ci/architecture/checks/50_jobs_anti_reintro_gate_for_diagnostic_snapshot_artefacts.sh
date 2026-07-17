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
# ── Check 36: anti-reintro gate for diagnostic / snapshot artefacts (PR-A, June 2026) ──
# Forward-prevention after the Wave 21 PR-G mega-batch that re-landed
# .tmp-diag/ directory + CURRENT_<X>.go + TODO<N>_<X>.go fixtures in the
# working tree (see paste audit). This gate ensures the .gitignore
# patterns appended by PR-A remain effective: any re-introduction of
# the four diagnostic patterns under internal/ cmd/ pkg/ scripts/ tests/
# fails CI with a remediation `git rm -rf` instruction.
#
# Pattern anchors (case-sensitive, basename-only):
#   directory names:  .tmp-diag,  tmp-diag
#   file basenames:   CURRENT_*.go  (literal CURRENT_ prefix)
#                     TODO[0-9]*.go (literal TODO prefix + 1 digit, no underscore required)
#
# Scope: the four top-level source roots only. .git/ hidden by default
# via `find` not descending into .git; tests/fixtures/zero_legacy/ is
# OUT of scope (`tests/` only matches the directory, fixtures of the
# canonical negative-example shape are not flagged).
#
# Implementation: `find` is canonical here (consistent with Check 23
# field-count extraction). rg --glob filters the search space, not the
# file-name; for basenameonly matching, find -name is the precise tool.
#
# Failure mode: emit the offending paths AND a copy-pasteable `git rm`
# one-liner so the operator can clean up in one step. Standard
# fail-fast + literal remediation. Index/PR-bodies stay consistent
# across the diagnostic-artefact family.
echo "=== Check 36: diagnostic-artefact anti-reintro gate (PR-A, June 2026) ==="
diag_files=$(find internal cmd pkg scripts tests -type f \
    \( -name 'CURRENT_*.go' -o -name 'TODO[0-9]*.go' \) \
    -not -path 'tests/fixtures/zero_legacy/*' 2>/dev/null || true)
diag_dirs=$(find internal cmd pkg scripts tests -type d \
    \( -name '.tmp-diag' -o -name 'tmp-diag' \) 2>/dev/null || true)
diag_hits=$(printf '%s\n%s\n' "$diag_files" "$diag_dirs" \
    | grep -v '^$' | sort -u || true)
if [ -n "$diag_hits" ]; then
    echo "FAIL: diagnostic / snapshot artefacts detected in source roots:"
    printf '%s\n' "$diag_hits" | sed 's/^/  /'
    echo ""
    echo "Resolution:"
    echo "  1. If these are intended diagnostic snapshots, MOVE them under"
    echo "     tests/fixtures/zero_legacy/ (the canonical negative-example"
    echo "     surface exempted by this gate)."
    echo "  2. Otherwise the canonical cleanup is to remove them via:"
    printf '%s\n' "$diag_hits" | sed 's/^/     git rm -rf /'
    echo ""
    echo "Per AGENTS.md §8 ARCHITECTURE-CI-GATES zero-baseline rule,"
    echo "re-introduction of these patterns is now blocked; this gate"
    echo "is the forward-prevention half of PR-A."
    exit 1
fi
echo "Check 36: 0 diagnostic-artefact paths in internal/ cmd/ pkg/ scripts/ tests/"
# ── Check 39: HC-7 anti-reintro gate (ChunkDuration: 25 literal + parent_id:"") ────
# HC-7 (June 2026) consolidates the script-video SSOT into
# pkg/defaults/video.go::{VideoConfig, DefaultVideoConfig}. Two patterns
# historically leaked past the SSOT and the leak-prone variants are
# gated here:
#
#   (a) ChunkDuration: 25 literal in platform/config/video.go::WithDefaults
#       (was hard-coded at line 64 pre-HC-7). The handler-side video
#       pipeline must read defaults.DefaultVideoConfig().ChunkDuration.
#       Pattern: `ChunkDuration <= 0 { ... = 25 `  (the cheap-to-grep
#       textual re-occurrence of the literal in the *conditioned* default
#       path — the unconditional canonical is in defaults package).
#
#   (b) `"parent_id": ""` literal in /api/scripts/* HTTP responses. The
#       canonical reader uses `s.ParentScriptID` (line 121 of
#       internal/api/script/helpers.go::ListScripts post-HC-7); the empty
#       string was DRIFT-23-4.
#
# Pattern anchors:
#   ChunkDuration.{0,40}= 25   — the conditioned-default shape; tolerates
#                                 any arithmetic (e.g. `+=25` `=((25))`)
#                                 but REMAINS strict on the literal value.
#   "parent_id":[[:space:]]*""  — the exact JSON-empty pattern.
#
# Scope: the same four top-level source roots used by Check 36 to keep
# the diagnostic-artefact family aligned. tests/fixtures/zero_legacy/
# is OUT of scope (negative-example fixtures exempt, mirrors Check 36).
#
# Negative examples live in fixtures/zero_legacy/ — if a future
# negative-EXAMPLE fixture needs to exist, place it there (the gate
# excludes that path) and update Check 39's allowlist rationale.
echo "=== Check 39: HC-7 anti-reintro gate (ChunkDuration: 25 literal + parent_id:\"\") ==="
hc7_hits=$(rg -n --type go \
    -e 'ChunkDuration.{0,40}=[[:space:]]*25\b' \
    -e '"parent_id":[[:space:]]*""' \
    --glob '!**/*_test.go' \
    --glob '!tests/fixtures/zero_legacy/*' \
    internal cmd pkg scripts 2>/dev/null \
    | awk -F: '{
        rest = ""
        for (i = 3; i <= NF; i++) rest = rest (i > 3 ? ":" : "") $i
        if (rest ~ /^[[:space:]]*\/\//) next   # drop full-line comments
        print
    }' \
    || true)
# Filter out the SSOT itself: pkg/defaults/video.go is where the canonical
# 25 + "parent_id" literal legitimately lives; excluding it keeps the gate
# focused on consumer re-introduction.
hc7_literal=$(printf '%s\n' "$hc7_hits" \
    | awk -F: '$1 != "pkg/defaults/video.go"' \
    || true)
if [ -n "$hc7_literal" ]; then
    echo "FAIL: HC-7 re-introduction detected (ChunkDuration: 25 literal OR parent_id:\"\"):"
    printf '%s\n' "$hc7_literal" | sed 's/^/  /'
    echo ""
    echo "Fix: route the value through pkg/defaults/video.go::{VideoConfig,"
    echo "      DefaultVideoConfig}. The canonical CSV lives in:"
    echo "    - ChunkDuration: 25          → defaults.DefaultVideoConfig().ChunkDuration"
    echo "    - parent_id JSON field name → defaults.DefaultVideoConfig().ParentFieldName"
    echo "    - EffectsDir: 'effects/'     → defaults.DefaultVideoConfig().EffectsDir"
    echo ""
    echo "For ListScripts-style parent_id emission, iterate scriptRecords and"
    echo "emit `s.ParentScriptID` (the canonical int64) rather than the literal"
    echo 'empty string `"parent_id": ""` (the DRIFT-23-4 anti-pattern).'
    exit 1
fi
echo "Check 39: 0 HC-7 re-introduction patterns (ChunkDuration: 25 \/ parent_id:\"\")"
