#!/usr/bin/env python3
"""Shim: tools/regen_hotspots.py → scripts/regen_hotspots.py

The CI gate historically invoked `python3 tools/regen_hotspots.py --check`.
Canonical implementation lives in `scripts/regen_hotspots.py`. This shim
forwards all arguments so both paths remain valid.
"""
import runpy
import sys
from pathlib import Path

shim_dir = Path(__file__).resolve().parent
canonical = shim_dir.parent / "scripts" / "regen_hotspots.py"
if not canonical.exists():
    print(f"FAIL: canonical script missing: {canonical}", file=sys.stderr)
    sys.exit(2)
# Forward to canonical implementation
sys.argv[0] = str(canonical)
runpy.run_path(str(canonical), run_name="__main__")
