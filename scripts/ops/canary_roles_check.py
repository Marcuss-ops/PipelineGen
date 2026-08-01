#!/usr/bin/env python3
"""Check for existing interview/training role assets for Floyd/SugarRay."""

from __future__ import annotations

import json
import os
import sqlite3

DB = os.environ.get("VELOX_DB", "data/media/media.db.sqlite")


def main() -> None:
    conn = sqlite3.connect(DB)
    print("=== ACTIVE assets with category interview/training (any boxer) ===")
    rows = conn.execute(
        "SELECT id, category, source_video_id, source_provider, index_state, folder_path, metadata_json "
        "FROM media_assets "
        "WHERE lifecycle_state='ACTIVE' AND category IN ('interview','training') "
        "ORDER BY category, id LIMIT 30"
    ).fetchall()
    for r in rows:
        print(f"  {str(r[0])[:46]:46s} {r[1]:10s} vid={str(r[2])[:14]:14s} prov={str(r[3])[:8]:8s} idx={r[4]:8s} folder={str(r[5])[:34]}")
    if not rows:
        print("  (none)")
    print()
    print("=== ALL planner: assets in DB ===")
    rows = conn.execute(
        "SELECT id, category, source_video_id, index_state, drive_file_id, folder_path "
        "FROM media_assets WHERE id LIKE 'planner:%' ORDER BY id LIMIT 40"
    ).fetchall()
    for r in rows:
        print(f"  {str(r[0])[:46]:46s} cat={str(r[1])[:10]:10s} vid={str(r[2])[:14]:14s} idx={r[3]:8s} drive={'yes' if r[4] else 'no'} folder={str(r[5])[:34]}")
    conn.close()


if __name__ == "__main__":
    main()
