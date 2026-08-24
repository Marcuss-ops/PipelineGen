#!/usr/bin/env bash
#
# Check 45 — ban inline map[string]*ClipsRepository{...} map literals
# outside the canonical composition-root + registry paths.
#
# Action P1-3 of cleanup plan (June 2026): the canonical contract for
# ClipRepository access in production paths is the typed
# ClipRepositoryPort / ClipStorePort surface. Inline
# `map[string]*ClipsRepository{...}` literals outside the canonical
# sites are a regression to the pre-port days and block the
# architecture from migrating to alternate clip-store backends
# (Qdrant-only, in-memory mock, etc.).
#
# This check is the UNDERSPECIFIED-WILDCARD sibling of Check 8 (S3e,
# already in ci-architectural-checks.sh). Check 8 covers the fully-
# qualified `*assets.ClipsRepository{` literal (via the canonical
# assets-package alias). Check 45 covers the BARE / shorthand
# `*ClipsRepository{` variant, which is a likely regression shape if a
# future contributor imports the canonical type without the package
# alias (or a future refactor introduces a new unqualified alias).
#
# Canonical-allowed sites (exclusions):
#   - internal/app/**                                               : composition root
#                                                                     directory (the bag is built
#                                                                     in build_bundles_core.go and
#                                                                     injected into the
#                                                                     assetindex.ResolverConfig).
#   - internal/platform/sqlite/assetindex/resolver.go       : canonical registry
#                                                                     field declaration (`ClipsRepos`
#                                                                     field, NOT a literal — the
#                                                                     field type happens to use the
#                                                                     `map[string]*assets.ClipsRepository`
#                                                                     shape but produces no "{...}"
#                                                                     literal at the gate's regex
#                                                                     hit-level; included in the
#                                                                     exclusion so any future
#                                                                     initialisation site stays
#                                                                     adjacent to the existing
#                                                                     adapter surface).
#   - *_test.go                                                     : test fixtures may freely
#                                                                     build the literal as
#                                                                     test scaffolding.
#   - tests/fixtures/zero_legacy/check_*.go                        : canonical negative-example
#                                                                     fixtures (Check 8 precedent).
#
# Pattern anchor: literal — bare form OR any-package-qualified form.
#   map[string]*ClipsRepository{                    (bare; package name empty)
#   map[string]*<pkg>.ClipsRepository{             (qualified; e.g. *sqassets., *assets., *someAlias.)
# Regex: ([A-Za-z0-9_]+\.)? makes the package-name+dot optional, so any
# future contributor-introduced package alias (e.g. `*appsq.ClipsRepository`)
# is caught alongside the canonical site (`*sqassets.ClipsRepository`) and
# the existing bare shorthand (`*ClipsRepository{`). Whitespace-tolerant
# between `*` / `<pkg>` / `ClipsRepository`. The previous narrow-scope
# documentation explicitly anticipated this broadening: a future
# `*someAlias.ClipsRepository` would have slipped past BOTH Check 8
# (package-exact `*assets.ClipsRepository`) AND the bare-only Check 45 —
# this commit closes that gap. ripgrep -E bracket escapes `\[` / `\]` stay
# LITERAL (not flexible), so `map[ string ]*...` stylistic variants still
# need a follow-up if ever introduced; the bare-and-qualified forms are
# the documented regression shapes and the gate is tight on those.
#
# The check uses `set -uo pipefail` and exits non-zero on ANY hit.
# All output is regenerated every CI run; the gate is fail-closed
# per AGENTS.md §8 ARCHITECTURE-CI-GATES zero-baseline rule.

set -uo pipefail

fail_count=0
fail_messages=""

# Step 1: collect ALL hits under internal/ in production .go files
# (excluding the canonical-allowed sites). ripgrep (-E / --type go /
# --glob) is used for consistency with the other ci-architecture checks;
# the failure set is then filtered to drop full-line `//`-comments
# (descriptive prose that mentions the pattern is not a violation).
all_hits=$(rg -nE 'map\[string\][ ]*\*[ ]*([A-Za-z0-9_]+\.)?[ ]*ClipsRepository\{' \
    --type go \
    --glob '!**/internal/app/**' \
    --glob '!**/platform/sqlite/assetindex/resolver.go' \
    --glob '!**/*_test.go' \
    --glob '!tests/fixtures/zero_legacy/**' \
    internal/ 2>/dev/null) || true

# Step 2: drop descriptive full-line `//`-comments (the negative-example
# fixture pattern may reference the literal in prose; the gate is
# strictly about production-code usage).
literal_calls=""
if [ -n "${all_hits}" ]; then
    literal_calls=$(printf '%s\n' "${all_hits}" \
        | awk -F: '{
            rest = ""
            for (i = 3; i <= NF; i++) rest = rest (i > 3 ? ":" : "") $i
            # drop full-line comments (single-line `//` or leading block `/*`)
            if (rest ~ /^[[:space:]]*\/\//) next
            if (rest ~ /^[[:space:]]*\/\*/) next
            print
        }' || true)
fi

# Step 3: emit FAIL on any non-empty literal set.
if [ -n "${literal_calls}" ]; then
    fail_count=$((fail_count + 1))
    fail_messages="${fail_messages}
  Inline map[string]*ClipsRepository literal found outside canonical composition root:
${literal_calls}"
fi

# Step 4: emit verdict + remediation.
if [ "${fail_count}" -gt 0 ]; then
    echo "Check 45 (inline ClipsRepository map ban): FAIL (${fail_count} violations):"
    echo "${fail_messages}"
    echo ""
    echo "Fix: route production ClipRepository access through the typed"
    echo "      ClipRepositoryPort surface in"
    echo "      internal/application/clips/ports.go. The composition root"
    echo "      (internal/app/build_bundles_core.go) is the ONLY site"
    echo "      permitted to build the source=>repo bag; the bag is"
    echo "      injected into assetindex.ResolverConfig, which is the"
    echo "      SSOT for adapter-layer dispatch."
    echo ""
    echo "      If the literal is a transitional backfill, extract the"
    echo "      bag build into the canonical file (or a composition-"
    echo "      root sibling) and inject the per-source repo into the"
    echo "      caller via deps."
    exit 1
fi

echo "Check 45 (inline ClipsRepository map ban): OK (0 violations; 0 inline bare map[string]*ClipsRepository{...} literals outside canonical sites)"
exit 0
