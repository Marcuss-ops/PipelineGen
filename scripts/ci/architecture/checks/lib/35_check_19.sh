#!/usr/bin/env bash
# Check 19: prevent reintroduction of retired internal roots.
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
