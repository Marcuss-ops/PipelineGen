#!/usr/bin/env bash
# scripts/ci-strict-gate-block.sh
#
# Mechanical strict-gate blocker. To be invoked on every push to main
# (or on PR open/update targeting main) by GitHub Actions / GitLab CI.
# Exits 0 only when EVERY check below passes.
#
# Reference: codex/dir-strict-gates (July 2026) — the user mandate
# "Bloccante: nessun merge futuro verso main finché --strict non è verde."
# is enforced by THIS script. AGENTS.md Active Concern #11 cross-references.
set -euo pipefail

echo "=== strict-gate blocker ==="
FAILED=0

# 1. archcheck --strict (no ratchet baseline anymore — direct check on
#    current tree's aliases/wrappers/violations/directories all zero).
echo "-- 1) go run ./scripts/archcheck --strict"
if go run ./scripts/archcheck --strict; then
  echo "   OK"
else
  echo "   FAIL: archcheck --strict non-zero"
  FAILED=1
fi

# 2. ci-architectural-checks.sh (includes Check 15: strict-gate perimeter).
echo "-- 2) bash scripts/ci-architectural-checks.sh"
if bash scripts/ci-architectural-checks.sh > /tmp/strict_block_ci.log 2>&1; then
  echo "   OK"
else
  echo "   FAIL: ci-architectural-checks.sh non-zero"
  tail -40 /tmp/strict_block_ci.log
  FAILED=1
fi

# 3. The 4 final rg searches (verbatim of the user's mandate).
echo "-- 3) 4 final rg searches"
for entry in \
  "rg 'internal/(media|sources)\\b' --type go ." \
  "rg 'internal/application/(association|realtime|ingest|monitor|artlist)\\b' --type go ." \
  "rg 'database/sql' --type go internal/api internal/application internal/domain" \
  "rg 'sql\\\\.Open\\\\(' --type go internal"; do
  HITS=$(eval "$entry" 2>/dev/null | grep -vE ':[0-9]+:\\s*//' | wc -l || true)
  if [ "${HITS:-0}" -eq 0 ]; then
    echo "   OK: $entry → 0"
  else
    echo "   FAIL: $entry → $HITS"
    FAILED=1
  fi
done

if [ "$FAILED" -ne 0 ]; then
  echo ""
  echo "strict-gate blocker FAILED"
  exit 1
fi

echo ""
echo "strict-gate blocker PASSED"
