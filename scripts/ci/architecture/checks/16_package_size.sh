#!/usr/bin/env bash
# Check 16: <=39 productive files per package (TRANSITIONAL allowlist for qdrant until 2026-07-15)
#
# Reads allowlist package dirs verbatim from:
#   docs/migrations/archcheck-2026-06-28-hard-gate-promotion.md

set -u

TODAY=$(date -u +%Y-%m-%d)
EXPIRY=2026-07-15
IS_PRE_EXPIRY=$([ "$TODAY" \< "$EXPIRY" ] && echo 1 || echo 0)

CURR_DIR=$(cd "$(dirname "$0")" && pwd)
ROOT=$(cd "$CURR_DIR/../../../.." && pwd)
DOC="$ROOT/docs/migrations/archcheck-2026-06-28-hard-gate-promotion.md"

if [ ! -f "$DOC" ]; then
  echo "[FAIL] Check 16: allowlist doc not found at $DOC"
  exit 1
fi

ALLOWLIST_DIRS=$(grep -oE 'internal/platform/qdrant(/[A-Za-z0-9_]+)?' "$DOC" | sort -u)

INTERNAL_DIR="$ROOT/internal"
[ -d "$INTERNAL_DIR" ] || { echo "[FAIL] Check 16: $INTERNAL_DIR not found"; exit 1; }

HARD_FAIL=""
WARN=""
for d in $(find "$INTERNAL_DIR" -mindepth 2 -maxdepth 2 -type d); do
  c=$(ls "$d"/*.go 2>/dev/null | grep -v _test.go | wc -l)
  if [ "$c" -ge 40 ]; then
    rel="${d#${ROOT}/}"
    matched=0
    for allow in $ALLOWLIST_DIRS; do
      if grep -qF "$allow" <<< "$rel"; then matched=1; break; fi
    done
    if [ "$matched" = "1" ]; then
      if [ "$IS_PRE_EXPIRY" = "1" ]; then
        WARN="${WARN}${rel}: ${c} files\n"
      else
        HARD_FAIL="${HARD_FAIL}[EXPIRED] ${rel}: ${c} files\n"
      fi
    else
      HARD_FAIL="${HARD_FAIL}${rel}: ${c} files\n"
    fi
  fi
done

if [ -n "$HARD_FAIL" ]; then
  echo "Check 16 FAILED: >=40 file packages (hard-fail):"
  echo -e "$HARD_FAIL"
  exit 1
fi
if [ -n "$WARN" ]; then
  echo "Check 16 WARNING: >=40 file packages in transitional allowlist (expiry $EXPIRY):"
  echo -e "$WARN"
fi
echo "Check 16 OK"
exit 0
