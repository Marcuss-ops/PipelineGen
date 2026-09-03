#!/usr/bin/env bash
# Check 60 (ImportZero for deleted application roots) is retired: the
# forward-prevention surface is the Go archcheck retired-root gates.
# Hotspots freshness
echo "=== Check 61: Hotspots freshness ==="
if [ ! -f scripts/regen_hotspots.py ]; then
  echo "FAIL: regen_hotspots.py missing (expected scripts/regen_hotspots.py)"
  exit 1
fi
python3 scripts/regen_hotspots.py --check 2>&1 || {
  echo "FAIL: hotspots stale — run python3 scripts/regen_hotspots.py"
  exit 1
}
echo "OK: hotspots fresh"
