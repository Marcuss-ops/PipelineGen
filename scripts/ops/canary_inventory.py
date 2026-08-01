#!/usr/bin/env python3
"""Canary inventory — ACTIVE assets grouped by folder_path + Mayweather/Robinson scan."""

from __future__ import annotations

import os
import sqlite3

DB = os.environ.get("VELOX_DB", "data/media/media.db.sqlite")


def main() -> None:
    conn = sqlite3.connect(DB)
    print("=== ALL ACTIVE assets grouped by folder_path/category (top 40) ===")
    rows = conn.execute(
        "SELECT folder_path, category, COUNT(*), "
        "SUM(CASE WHEN index_state='INDEXED' THEN 1 ELSE 0 END) "
        "FROM media_assets "
        "WHERE lifecycle_state='ACTIVE' AND folder_path IS NOT NULL AND folder_path != '' "
        "GROUP BY folder_path, category ORDER BY folder_path LIMIT 40"
    ).fetchall()
    for fp, cat, n, idx in rows:
        print(f"  {str(fp)[:70]:70s} {str(cat):12s} total={n} indexed={idx}")
    print()
    print("=== Mayweather/Robinson ACTIVE assets ===")
    rows = conn.execute(
        "SELECT id, source_provider, source_video_id, category, index_state, folder_path "
        "FROM media_assets "
        "WHERE (folder_path LIKE '%ayweather%' OR folder_path LIKE '%obinson%' "
        "   OR name LIKE '%ayweather%' OR name LIKE '%obinson%') "
        "AND lifecycle_state='ACTIVE' ORDER BY folder_path LIMIT 40"
    ).fetchall()
    for r in rows:
        print(f"  {str(r[0])[:55]:55s} | {r[1]} | {str(r[2])[:14]:14s} | {r[3]} | {r[4]} | {str(r[5])[:40]}")
    conn.close()


if __name__ == "__main__":
    main()
