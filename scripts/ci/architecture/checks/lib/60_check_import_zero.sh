#!/usr/bin/env bash
# 60_check_import_zero — ensures deleted application roots have zero imports
echo "=== Check 60: ImportZero for deleted application roots ==="
deleted=("internal/application/jobs" "internal/application/images" "internal/application/qdrant" "internal/application/mediamemory" "internal/application/search")
fail=0
for pat in "${deleted[@]}"; do
  hits=$(git grep -l "$pat" -- "*.go" 2>/dev/null || true)
  if [ -n "$hits" ]; then
    echo "FAIL: stale import $pat found in:"
    echo "$hits" | sed 's/^/  /'
    fail=1
  fi
done
if [ $fail -ne 0 ]; then exit 1; fi
echo "OK: no stale imports to deleted application roots"
# Hotspots freshness
echo "=== Check 61: Hotspots freshness ==="
if [ -f scripts/regen_hotspots.py ]; then
  python3 scripts/regen_hotspots.py --check 2>&1 || {
    echo "FAIL: hotspots stale — run python3 scripts/regen_hotspots.py"
    exit 1
  }
elif [ -f tools/regen_hotspots.py ]; then
  python3 tools/regen_hotspots.py --check 2>&1 || {
    echo "FAIL: hotspots stale — run python3 tools/regen_hotspots.py"
    exit 1
  }
else
  echo "FAIL: regen_hotspots.py missing (expected scripts/regen_hotspots.py)"
  exit 1
fi
echo "OK: hotspots fresh"
