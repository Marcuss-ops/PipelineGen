#!/usr/bin/env bash
# Hermetic test for scripts/ci/check-clean-tree.sh.
set -Eeuo pipefail

SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
GATE="$SCRIPT_DIR/check-clean-tree.sh"
TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT

failures=0
pass() { printf '  PASS %s\n' "$1"; }
fail() { printf '  FAIL %s\n' "$1"; failures=$((failures + 1)); }

repo="$TMP/repo"
mkdir -p "$repo"
git -C "$repo" init -q
git -C "$repo" config user.email test@example.invalid
git -C "$repo" config user.name test
printf 'clean\n' > "$repo/tracked.txt"
git -C "$repo" add tracked.txt
git -C "$repo" commit -qm init

if "$GATE" "$repo" >/dev/null 2>&1; then
  pass "clean tree passes"
else
  fail "clean tree passes"
fi

printf 'dirty\n' > "$repo/untracked.txt"
if "$GATE" "$repo" >/dev/null 2>&1; then
  fail "untracked file fails"
else
  pass "untracked file fails"
fi
rm -f "$repo/untracked.txt"

printf 'modified\n' >> "$repo/tracked.txt"
if "$GATE" "$repo" >/dev/null 2>&1; then
  fail "modified tracked file fails"
else
  pass "modified tracked file fails"
fi
git -C "$repo" checkout -q -- tracked.txt

if "$GATE" "$TMP/not-a-repo" >/dev/null 2>&1; then
  fail "non-git directory fails"
else
  pass "non-git directory fails"
fi

printf '== clean-tree selftest: %d failure(s)\n' "$failures"
[ "$failures" -eq 0 ]
