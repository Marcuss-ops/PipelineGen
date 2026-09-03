#!/usr/bin/env python3
"""Regenerate architecture/package_hotspots.json current counts.

Counts production Go files (excluding *_test.go) per hotspot path and
recalculates max_loc (max lines per file) for local verification.
Mirrors cmd/archcheck/scan/structure packages scan logic (skip .git,
vendor, node_modules, etc.) but in Python for CI freshness gate.

Usage:
  python3 scripts/regen_hotspots.py          # regenerate in place
  python3 scripts/regen_hotspots.py --check  # fail if stale
"""
from __future__ import annotations

import argparse
import json
import sys
from pathlib import Path

REPO_ROOT = Path(__file__).resolve().parent.parent
HOTSPOT_PATH = REPO_ROOT / "architecture" / "package_hotspots.json"

SKIP_DIRS = {".git", "vendor", "node_modules", "node-scraper", "examples", "scripts", ".venv-argos", ".venv-whisper", ".cache", "out", "output"}

def count_production_files(repo_root: Path, hotspot_path: str) -> tuple[int, int]:
    """Return (file_count, max_loc) for production .go files directly in hotspot_path.

    Mirrors the hotspot definition in architecture/package_hotspots.json where
    the count is for the exact package directory (non-recursive), excluding
    *_test.go. This matches the legacyHotspotGrowth scanner which counts per
    directory, not recursively into subpackages (subpackages have their own
    hotspot or are target packages).
    """
    target = repo_root / hotspot_path
    if not target.exists() or not target.is_dir():
        return 0, 0
    count = 0
    max_loc = 0
    for p in target.glob("*.go"):
        if p.name.endswith("_test.go"):
            continue
        count += 1
        try:
            loc = sum(1 for _ in p.open(encoding="utf-8", errors="ignore"))
        except OSError:
            continue
        if loc > max_loc:
            max_loc = loc
    return count, max_loc

def load_hotspots() -> dict:
    with HOTSPOT_PATH.open(encoding="utf-8") as f:
        return json.load(f)

def regenerate(check: bool = False) -> int:
    data = load_hotspots()
    changed = False
    details = []
    for hs in data.get("hotspots", []):
        path = hs.get("path", "")
        old_files = hs.get("current", {}).get("files", None)
        old_loc = hs.get("current", {}).get("max_loc", None)
        actual_files, actual_loc = count_production_files(REPO_ROOT, path)
        # max_loc: if baseline max_loc is 0, keep 0 (hotspot tracks only file count)
        # Otherwise update to actual max, but preserve 0-sentinel semantics.
        baseline_max = hs.get("baseline", {}).get("max_loc", 0)
        # For hotspots where baseline max_loc is 0, keep current max_loc as 0
        if baseline_max == 0:
            actual_loc = 0
        if old_files != actual_files or old_loc != actual_loc:
            details.append(f"  {path}: {old_files}→{actual_files} files, {old_loc}→{actual_loc} max_loc")
            if not check:
                hs.setdefault("current", {})["files"] = actual_files
                hs["current"]["max_loc"] = actual_loc
            changed = True
    if check:
        if changed:
            print("FAIL: package_hotspots.json stale — current counts differ:", file=sys.stderr)
            for d in details:
                print(d, file=sys.stderr)
            print("Run: python3 scripts/regen_hotspots.py", file=sys.stderr)
            return 1
        print("OK: hotspots fresh")
        return 0
    if changed:
        # preserve formatting: json.dump with indent 2, ensure trailing newline
        with HOTSPOT_PATH.open("w", encoding="utf-8") as f:
            json.dump(data, f, indent=2, ensure_ascii=False)
            f.write("\n")
        print("Regenerated architecture/package_hotspots.json:")
        for d in details:
            print(d)
    else:
        print("OK: hotspots already fresh")
    return 0

def main() -> None:
    ap = argparse.ArgumentParser(description="Regenerate package_hotspots current counts")
    ap.add_argument("--check", action="store_true", help="fail if file is stale instead of rewriting")
    args = ap.parse_args()
    sys.exit(regenerate(check=args.check))

if __name__ == "__main__":
    main()
