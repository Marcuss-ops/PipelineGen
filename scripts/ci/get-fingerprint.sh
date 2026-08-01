#!/usr/bin/env bash
# scripts/ci/get-fingerprint.sh
#
# Generates a fingerprint of the current source tree state (including commits,
# working directory diff, untracked contents, and tooling versions) to cache
# green verification runs safely.

set -euo pipefail

# 1. Commit SHA
COMMIT_SHA=$(git rev-parse HEAD 2>/dev/null || echo "no-commit")

# 2. Diff of working tree (tracked and untracked)
#
# `git status --porcelain` records untracked paths but not their contents.
# Include a deterministic content hash so changing an untracked source file
# invalidates a cached green verification result instead of reusing stale
# output for the same path. Git emits tracked paths in a stable order, and the
# NUL delimiter keeps filenames containing whitespace/newlines unambiguous.
TRACKED_DIFF_HASH=$(git diff HEAD 2>/dev/null | sha256sum | awk '{print $1}')
UNTRACKED_CONTENT_HASH=$(
    git ls-files --others --exclude-standard -z 2>/dev/null \
        | while IFS= read -r -d '' path; do
            printf '%s\0' "$path"
            sha256sum -- "$path"
        done \
        | sha256sum \
        | awk '{print $1}'
)
WORKTREE_DIFF="${TRACKED_DIFF_HASH}_${UNTRACKED_CONTENT_HASH}"

# 3. Go version
GO_VER=$(go version 2>/dev/null || echo "no-go")

# 4. Node version
NODE_VER=$(node --version 2>/dev/null || echo "no-node")

# 5. Hash of Make verification inputs
VERIFY_MK_HASH=$(sha256sum make/verify.mk make/test.mk 2>/dev/null | sha256sum | awk '{print $1}' || echo "no-verify-mk")

# 6. Hash of CI scripts and hooks
CI_SCRIPTS_HASH=$(find scripts/ci scripts/hooks -type f 2>/dev/null | sort | xargs sha256sum 2>/dev/null | sha256sum | awk '{print $1}' || echo "no-ci-scripts")

# Combine all into a single fingerprint
echo "${COMMIT_SHA}_${WORKTREE_DIFF}_${GO_VER}_${NODE_VER}_${VERIFY_MK_HASH}_${CI_SCRIPTS_HASH}" | sha256sum | awk '{print $1}'
