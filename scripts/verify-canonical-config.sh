#!/usr/bin/env bash
# scripts/verify-canonical-config.sh
#
# Fails when any of the runtime configuration surfaces drifts away from the
# canonical port 8000. Mirrors the Go test in
#   internal/platform/config/canonical_defaults_test.go
# so CI shells and developers without a Go toolchain still get a fast
# read-only check.
#
# Run from repo root:
#   bash scripts/verify-canonical-config.sh
#
# Exit codes:
#   0 = every checked file already reflects port 8000
#   1 = at least one surface still references 8080 (or is missing)

set -uo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT_DIR" || { echo "❌ cannot cd to $ROOT_DIR"; exit 2; }

fail=0

# Files where `:8080` literals are forbidden (canonical default = 8000).
# Whitelist: ServerConfig.go uses 8000, SearXNG uses 18080, healthcheck
# does ':8000/health'. Anything else with `:8080` is drift.
FILES_TO_CHECK=(
  ".env.example"
  "config.example.yaml"
  "Makefile"
  "Dockerfile"
  "docker-compose.yml"
  "scripts/start.sh"
  "README.md"
  "PROJECT_GUIDE.md"
)

echo "Checking canonical port 8000 in runtime surfaces..."

for f in "${FILES_TO_CHECK[@]}"; do
  if [ ! -f "$f" ]; then
    echo "  ! $f: missing (skipping)"
    continue
  fi
  hits=$(grep -nE ':8080\b|\b8080\b' "$f" || true)
  if [ -n "$hits" ]; then
    echo "  ❌ $f contains obsolete 8080 references:"
    echo "$hits" | sed 's/^/     /'
    fail=1
  else
    echo "  ✅ $f"
  fi
done

# Also confirm defaults are consistent: .env.example + config.example.yaml
# must both DECLARE 8000 as the canonical default (read positive anchors).
echo
echo "Checking canonical anchors..."
for f in .env.example config.example.yaml scripts/start.sh; do
  if [ ! -f "$f" ]; then continue; fi
  if ! grep -qF '8000' "$f"; then
    echo "  ❌ $f does not declare the canonical 8000 anchor"
    fail=1
  fi
done

# Makefile doctor/artlist must default to 8000 (not 8080)
if grep -qE 'VELOX_PORT:-8080|8080.*pipefail' Makefile; then
  echo "  ❌ Makefile still references 8080 as a default"
  fail=1
else
  echo "  ✅ Makefile"
fi

# docker-compose traffic must speak to port 8000
if grep -qE 'pipelinegen-server:8080\b' docker-compose.yml; then
  echo "  ❌ docker-compose.yml still points VELOX_MASTER_URL at 8080"
  fail=1
else
  echo "  ✅ docker-compose.yml"
fi

if [ "$fail" -eq 0 ]; then
  echo
  echo "✅ All runtime surfaces are aligned to canonical port 8000."
  exit 0
fi

echo
echo "❌ canonical runtime defaults diverged from port 8000 — fix and re-run."
exit 1
