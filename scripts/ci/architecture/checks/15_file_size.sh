#!/usr/bin/env bash
# Check 15: 500-LoC per productive .go file (TRANSITIONAL allowlist until 2026-07-15)
#
# Reads allowlist paths verbatim from:
#   docs/migrations/archcheck-2026-06-28-hard-gate-promotion.md
# (paths listed as `internal/.../*.go`).

set -u

TODAY=$(date -u +%Y-%m-%d)
EXPIRY=2026-07-15
IS_PRE_EXPIRY=$([ "$TODAY" \< "$EXPIRY" ] && echo 1 || echo 0)

CURR_DIR=$(cd "$(dirname "$0")" && pwd)
ROOT=$(cd "$CURR_DIR/../../../.." && pwd)
DOC="$ROOT/docs/migrations/archcheck-2026-06-28-hard-gate-promotion.md"

if [ ! -f "$DOC" ]; then
  echo "[FAIL] Check 15: allowlist doc not found at $DOC"
  exit 1
fi

ALLOWLIST=$(grep -oE 'internal/[A-Za-z0-9_./-]+\.go' "$DOC" | sort -u)

INTERNAL_DIR="$ROOT/internal"
[ -d "$INTERNAL_DIR" ] || { echo "[FAIL] Check 15: $INTERNAL_DIR not found"; exit 1; }

ALL_OVER=$(find "$INTERNAL_DIR" -name '*.go' ! -name '*_test.go' -exec wc -l {} + 2>/dev/null | awk '$1 > 500 {printf "%6d %s\n", $1, $2}')

HARD_FAIL=""
WARN=""
while IFS= read -r line; do
  [ -z "$line" ] && continue
  file=$(echo "$line" | awk '{$1=""; sub(/^[ ]*/, ""); print}')
  matched=0
  for allow in $ALLOWLIST; do
    if grep -qF "$allow" <<< "$file"; then matched=1; break; fi
  done
  if [ "$matched" = "1" ]; then
    if [ "$IS_PRE_EXPIRY" = "1" ]; then
      WARN="${WARN}${line}\n"
    else
      HARD_FAIL="${HARD_FAIL}[EXPIRED] ${line}\n"
    fi
  else
    HARD_FAIL="${HARD_FAIL}${line}\n"
  fi
done <<< "$ALL_OVER"

if [ -n "$HARD_FAIL" ]; then
  echo "Check 15 FAILED: >500 LoC violations (hard-fail):"
  echo -e "$HARD_FAIL"
  exit 1
fi
if [ -n "$WARN" ]; then
  echo "Check 15 WARNING: >500 LoC files in transitional allowlist (expiry $EXPIRY):"
  echo -e "$WARN"
fi
echo "Check 15 OK"
exit 0
