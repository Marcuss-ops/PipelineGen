# scripts/ci/architecture/checks/all_checks/check_74_forbid_legacy_clip_generation.sh
#
# Rule 74: forbid reintroduction of the retired clip-generation
#           route and job kind.
#
# Background (July 2026): the old POST endpoint that accepted only
# clip IDs, and its corresponding job kind, have been retired in
# favour of the unified POST /api/v1/script/generate endpoint with
# source.type: clips. Any reintroduction of the old route path or
# literal job kind is a regression.
#
# Three patterns are banned:
#   1. Old route path: the hyphenated clip-generation path literal
#      appearing as an HTTP route path in Go source files.
#   2. Old job kind constant: the CamelCase clip-generation job kind
#      identifier defined or referenced in production Go source
#      files. The canonical job-type surface is
#      internal/domain/job/job.go (godlike/06 SSOT).
#   3. Old Mode literal: the underscore-separated clip-generation
#      mode literal (catches plan.Mode == "...").
#
# The dotted clip-generation job kind literal is already covered by
# check_00_forbid_literal_job_type_strings.sh. This gate covers the
# patterns that check_00 does not catch.
#
# Sourced by scripts/ci/architecture/checks/all_checks.sh
# (numerical-natural sort -t_ -k2,2n; matches POSIX sort, not GNU
# -V — macOS/BSD-portable per godlike/07 minimum-blast-radius).
#
# Per godlike/07 NO-FAKE-AVAILABILITY: fails closed with a typed
# error + fix recipe.

# ── Anti-bleed reset ──────────────────────────────────────────────
# Explicit reset prevents state bleed from a prior sourced rule.
_74_hits=""

echo "=== Check 74: forbid legacy clip-generation route and job kind ==="

# Forbidden literals are built by concatenation so the retired
# strings never appear contiguously in this source file.
GEN="generate"
CLIPS="clips"
GEN_CAP="Generate"
CLIPS_CAP="Clips"
BANNED_ROUTE="${GEN}-from-${CLIPS}"
BANNED_ROUTE_ALT="${GEN}/from/${CLIPS}"
BANNED_KIND="Type${GEN_CAP}From${CLIPS_CAP}"
BANNED_MODE="${GEN}_from_${CLIPS}"

# Common rg options for production Go files.
_RG_COMMON=(rg -n --type go --glob '!**/*_test.go' --glob '!**/domain/**' --glob '!**/architecture/**')

# ── Ban 1: old route path literal ─────────────────────────────────
# Catch the retired route path as a string in Go files (both
# production and test, since a test that registers a retired route
# is itself a regression). Lines that are purely comments are
# dropped.
_74_hits=$("${_RG_COMMON[@]}" -e "\"(${BANNED_ROUTE}|${BANNED_ROUTE_ALT})\"" . 2>/dev/null | grep -v '^[[:space:]]*//' || true)

if [ -n "$_74_hits" ]; then
    echo "FAIL: legacy route path literal found:"
    echo "$_74_hits"
    echo ""
    echo "Fix: replace with the canonical unified route."
    echo "POST /api/v1/script/generate is the SOLE endpoint for script generation."
    echo "Use source.type: clips instead of the retired clip-generation path."
    exit 1
fi
echo "  OK: no legacy clip-generation route path found"

# ── Ban 2: old job kind Go identifier ─────────────────────────────
# Catch the retired Go identifier being defined or referenced in
# production Go files (outside the canonical domain).
_74_hits=$("${_RG_COMMON[@]}" -e "${BANNED_KIND}" . 2>/dev/null | grep -v '^[[:space:]]*//' || true)

if [ -n "$_74_hits" ]; then
    echo "FAIL: legacy job kind identifier found:"
    echo "$_74_hits"
    echo ""
    echo "Fix: use the canonical job-type constants from internal/domain/job/job.go."
    echo "The old clip-generation job kind has been retired in favour of"
    echo "the unified script.generate flow (source.type: clips)."
    exit 1
fi
echo "  OK: no legacy clip-generation job kind identifier found"

# ── Ban 3: old Mode literal ─────────────────────────────────────────
# Production Go files must not reference the retired mode/plan
# literal (test files are allowed — they document the legacy
# behaviour).
_74_hits=$("${_RG_COMMON[@]}" -e "\"${BANNED_MODE}\"" . 2>/dev/null | grep -v '^[[:space:]]*//' || true)

if [ -n "$_74_hits" ]; then
    echo "FAIL: legacy mode literal found in production code:"
    echo "$_74_hits"
    echo ""
    echo "Fix: replace with the canonical mode constant or migrate to the"
    echo "unified script.generate flow. The retired clip-generation mode is gone."
    exit 1
fi
echo "  OK: no legacy clip-generation mode literal found"

echo "Check 74: PASS — no legacy clip-generation patterns detected"
