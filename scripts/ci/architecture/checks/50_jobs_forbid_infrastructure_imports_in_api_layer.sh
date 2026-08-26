#!/usr/bin/env bash
# Check 19: reject reintroduction of the retired API/infrastructure surface.
#
# The internal/api and internal/infrastructure roots are migration-only and
# no longer exist in the current tree. The old per-file allowlist described
# deleted files and therefore could only create stale, dead exceptions.
# Current ownership is enforced by cmd/archcheck and scripts/archcheck.
set -euo pipefail

echo "=== Check 19: forbid retired internal/api and internal/infrastructure roots ==="

for root in internal/api internal/infrastructure; do
  if [ -d "${REPO_ROOT}/${root}" ]; then
    echo "FAIL: retired architecture root has been reintroduced: ${root}"
    echo "      Use only internal/app, internal/kernel, internal/capabilities, and internal/platform."
    exit 1
  fi
done

echo "OK: retired API/infrastructure roots are absent; no legacy allowlist required"
