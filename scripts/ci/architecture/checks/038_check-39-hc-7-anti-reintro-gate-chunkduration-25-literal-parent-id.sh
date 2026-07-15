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

