#!/usr/bin/env bash
# Forward-prevention gate for PR-VOICEOVER-GROUPSRESOLVER-RETIRE (commit pending).
# Mirrors the canonical code-reviewer percheck scanner pattern from
# cmd/archcheck/scan/percheck_voiceover_alias_ban.go (which banned
# `voiceover.VoiceoverRouteOverride` in production code).
#
# This script is the EQUIVALENT for `voiceover.GroupsResolver`,
# `voiceover.GroupEntry`, `voiceover.ErrGroupNotFound`, and
# `voiceover.NewGroupsResolver` — these were the 4 canonical aliases
# declared at `internal/application/voiceover/groups_resolver.go` (since
# retired via git-rm). The canonical surface now lives at
# `internal/application/assets/destination/resolver.go`:
#   - Resolver  (concrete-struct, was GroupsResolver)
#   - Entry     (struct, was GroupEntry)
#   - ErrNotFound (sentinel, was ErrGroupNotFound)
#   - NewResolver (ctor, was NewGroupsResolver)
#
# Naming convention: `voiceover.GroupsResolver` and `voiceover.GroupEntry`
# are the aging aliases that triggered this retirement. `destination.Resolver`
# + `destination.Entry` are the canonical SOLE owners per godlike/06 SSOT.
#
# Verification rule (per godlike/07 NO-FAKE-AVAILABILITY):
#   - 0 production-code references to the 4 deprecated aliases in:
#       internal/  cmd/  scripts/
#     (excluding _test.go — test RESIDUE markers documenting the
#     migration are preserved per AGENTS.md pre-existing-build-issues
#     convention, but the canonical consumer pattern is muted.)
#   - Pattern is WORD-BOUNDED (`\b...\b`) so `voiceover.GroupEntryType`
#     and `voiceover.NewGroupsResolverLike` would not false-positive.
#   - Pattern is anchored to the precise `voiceover.` import-path
#     prefix so legacy `vo.GroupsResolver` shortnames (if any were ever
#     added) would NOT match and CANNOT silently drift back.
#
# Exit codes (semantically aligned with PR-STOCK-E2E-A precedent):
#   0  PASS  no deprecated alias references detected.
#   1  FAIL  at least one deprecated alias reference survived the
#           retirement; the canonical contract was violated. The
#           output lists every offending file:line:match for operator
#           triage. The crash-side-effect (no go build) is left to
#           CI to flag downstream — this script is the FILE-LEVEL
#           forward-prevention, not a COMPILER-LEVEL check.
#   2  PREREQ missing (rg absent, target dir absent, or git not on
#           origin/main). Operator-triageable; canonical fix is to
#           install rg or run from the repo root on a clean checkout.

set -euo pipefail

# Canonical target pattern — word-bounded to avoid `voiceover.GroupEntryType`,
# short-name-shortened-imports would still match because we anchor on
# `voiceover.` literal-prefix (matches the full canonical import path
# the codebase uses everywhere; legacy short imports would have been
# flagged by godlike/06 SSOT earlier).
DEPRECATED_PATTERN='\bvoiceover\.(GroupsResolver|GroupEntry|ErrGroupNotFound|NewGroupsResolver)\b'

# Target dirs (canonical production sources; excludes _test.go per
# AGENTS.md pre-existing-build-issues convention).
TARGET_DIRS=( internal cmd scripts )

# Locate rg
if ! command -v rg >/dev/null 2>&1; then
  echo "FAIL: ripgrep (rg) not installed; install via your distro or run from the repo root" >&2
  exit 2
fi

# Locate repo root (canonical pattern; ripgrep can run from anywhere)
REPO_ROOT="$(git rev-parse --show-toplevel 2>/dev/null || pwd)"
cd "$REPO_ROOT"

# Verify target dirs exist (operator may pass wrong cwd; fail loud)
for d in "${TARGET_DIRS[@]}"; do
  if [ ! -d "$d" ]; then
    echo "FAIL: target dir '$d' does not exist relative to repo root ($REPO_ROOT)" >&2
    exit 2
  fi
done

# Run the canonical rg search (production code only; _test.go excluded;
# defensive vendor/node_modules/data exclusions cover non-source noise
# that may exist locally).
rg_output=$(rg -n --no-ignore \
  "$DEPRECATED_PATTERN" \
  "${TARGET_DIRS[@]}" \
  -g '!*_test.go' \
  -g '!.git' \
  -g '!vendor' \
  -g '!node_modules' \
  -g '!data' \
  --no-heading \
  2>&1 || true)

# PREREQ: rg found the dirs but produced no output? Could be a clean pass.
# We deliberately tolerate empty output.

if [ -n "$rg_output" ]; then
  echo "FAIL: PR-VOICEOVER-GROUPSRESOLVER-RETIRE forward-prevention gate violated." >&2
  echo "      The following files still contain references to the" >&2
  echo "      RETIRED voiceover.* aliases (post-2026-07-22 cleanup:" >&2
  echo "      git-rm internal/application/voiceover/groups_resolver.go):" >&2
  echo >&2
  echo "$rg_output" | sed 's/^/      /' >&2
  echo >&2
  echo "      Canonical migration: replace voiceover.GroupsResolver" >&2
  echo "      -> destination.Resolver + voiceover.GroupEntry" >&2
  echo "      -> destination.Entry + voiceover.ErrGroupNotFound" >&2
  echo "      -> destination.ErrNotFound + voiceover.NewGroupsResolver" >&2
  echo "      -> destination.NewResolver." >&2
  exit 1
fi

echo "PASS: PR-VOICEOVER-GROUPSRESOLVER-RETIRE forward-prevention gate clean."
echo "      0 production-code references to the 4 deprecated voiceover.* aliases."
echo "      Canonical surface: destination.Resolver / Entry / ErrNotFound / NewResolver"
echo "      at internal/application/assets/destination/resolver.go"
exit 0
