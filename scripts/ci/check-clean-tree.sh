#!/usr/bin/env bash
# check-clean-tree.sh — fail closed when a release build starts from a dirty tree.
#
# Why this exists: on 2026-11-23 a concurrent session rebuilt the live
# pipelinegen binary from a modified worktree (git_dirty=true), reintroducing the
# code/service drift that OPS-10 must prevent. A build that cannot be attributed
# to a clean revision is not a deployable artifact.
#
# Usage:
#   scripts/ci/check-clean-tree.sh [repo]
#
# Exit codes:
#   0 clean worktree
#   1 dirty worktree or not a git checkout
set -Eeuo pipefail

repo="${1:-$(git rev-parse --show-toplevel 2>/dev/null || true)}"

if [ -z "$repo" ] || ! git -C "$repo" rev-parse --is-inside-work-tree >/dev/null 2>&1; then
  printf 'ERROR: clean-tree gate: %s is not a git worktree\n' "${repo:-<unknown>}" >&2
  exit 1
fi

status="$(git -C "$repo" status --porcelain --untracked-files=normal)"
if [ -n "$status" ]; then
  printf 'ERROR: clean-tree gate: refusing a release build from a dirty tree (%s)\n' "$repo" >&2
  printf '%s\n' "$status" | sed -n '1,20p' >&2
  printf 'Commit or stash the changes, then rebuild. This gate has no bypass.\n' >&2
  exit 1
fi

printf 'clean-tree: %s is clean at %s\n' "$repo" "$(git -C "$repo" rev-parse --short HEAD)"
