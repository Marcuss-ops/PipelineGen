#!/usr/bin/env bash
# scripts/ci/get-fingerprint.sh
#
# Generates a fingerprint of the current source tree state (including commits,
# working directory diff, and tooling versions) to cache green verification runs.

set -euo pipefail

# 1. Commit SHA
COMMIT_SHA=$(git rev-parse HEAD 2>/dev/null || echo "no-commit")

# 2. Diff of working tree (tracked and untracked)
WORKTREE_DIFF=$( (git diff HEAD; git status --porcelain) 2>/dev/null | sha256sum | awk '{print $1}' )

# 3. Go version
GO_VER=$(go version 2>/dev/null || echo "no-go")

# 4. Node version
NODE_VER=$(node --version 2>/dev/null || echo "no-node")

# 5. Hash of make/verify.mk and make/test.mk
VERIFY_MK_HASH=$(sha256sum make/verify.mk make/test.mk 2>/dev/null | sha256sum | awk '{print $1}' || echo "no-verify-mk")

# 6. Hash of CI scripts (under scripts/ci and scripts/hooks)
CI_SCRIPTS_HASH=$(find scripts/ci scripts/hooks -type f 2>/dev/null | sort | xargs sha256sum 2>/dev/null | sha256sum | awk '{print $1}' || echo "no-ci-scripts")

# Combine all into a single fingerprint
echo "${COMMIT_SHA}_${WORKTREE_DIFF}_${GO_VER}_${NODE_VER}_${VERIFY_MK_HASH}_${CI_SCRIPTS_HASH}" | sha256sum | awk '{print $1}'
