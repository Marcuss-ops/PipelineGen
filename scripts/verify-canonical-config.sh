#!/usr/bin/env bash
# Verifies that runtime configuration surfaces use the canonical port 8000.

set -uo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT_DIR" || { echo "cannot cd to $ROOT_DIR"; exit 2; }

fail=0
FILES_TO_CHECK=(
  ".env.example"
  "config.example.yaml"
  "Makefile"
  "Dockerfile"
  "docker-compose.yml"
  "scripts/start.sh"
  "README.md"
)

echo "Checking canonical port 8000 in runtime surfaces..."
for f in "${FILES_TO_CHECK[@]}"; do
  if [ ! -f "$f" ]; then
    echo "  ! $f: missing (skipping)"
    continue
  fi
  hits=$(grep -nE ':8080\b|\b8080\b' "$f" || true)
  if [ -n "$hits" ]; then
    echo "  ERROR $f contains obsolete 8080 references:"
    echo "$hits" | sed 's/^/     /'
    fail=1
  else
    echo "  OK $f"
  fi
done

echo
echo "Checking canonical anchors..."
for f in .env.example config.example.yaml scripts/start.sh; do
  [ -f "$f" ] || continue
  if ! grep -qF '8000' "$f"; then
    echo "  ERROR $f does not declare the canonical 8000 anchor"
    fail=1
  fi
done

if grep -qE 'VELOX_PORT:-8080|8080.*pipefail' Makefile; then
  echo "  ERROR Makefile still references 8080 as a default"
  fail=1
else
  echo "  OK Makefile"
fi

if grep -qE 'pipelinegen-server:8080\b' docker-compose.yml; then
  echo "  ERROR docker-compose.yml still points at 8080"
  fail=1
else
  echo "  OK docker-compose.yml"
fi

if [ "$fail" -eq 0 ]; then
  echo
  echo "All runtime surfaces are aligned to canonical port 8000."
  exit 0
fi

echo
echo "Canonical runtime defaults diverged from port 8000."
exit 1
