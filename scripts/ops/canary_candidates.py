#!/usr/bin/env python3
"""Find concrete planner: assets for the 4 missing canary role slots.

Floyd/SugarRay interview+training: pick one ACTIVE+INDEXED+drive planner:
asset whose source_video_id matches the boxer's manifest video IDs.
"""

from __future__ import annotations

import os
import sqlite3

DB = os.environ.get("VELOX_DB", "data/media/media.db.sqlite")

SLOTS = {
    "floyd_mayweather": {
        "interview": ("J6Zert5VaWk", "1gjZjirv740", "RVc37DH7Sns", "Kxb9AUmSrIA", "1UA6zGsvkrw", "RXw8fJDTb5I"),
        "training": ("XVU8e6YGTY4", "sNumtUs8d6M"),
    },
    "sugar_ray_robinson": {
        "interview": ("naPht4IBx4w", "ohQYcpFSKs0", "Xi-2E5QcXtQ", "4LNQKtq5SEw", "CrAMsMCb2bg", "iuRLVCEdUUo"),
        "training": ("FQivVOx8SnM", "7D3_UMN97gI"),
    },
}


def main() -> None:
    conn = sqlite3.connect(DB)
    for boxer, roles in SLOTS.items():
        for role, video_ids in roles.items():
            print(f"--- {boxer} {role} (manifest videos: {', '.join(video_ids)}) ---")
            placeholders = ",".join("?" * len(video_ids))
            rows = conn.execute(
                f"SELECT id, source_video_id, lifecycle_state, index_state, drive_file_id, drive_link, duration_ms "
                f"FROM media_assets WHERE source_video_id IN ({placeholders}) "
                f"AND category=? ORDER BY id LIMIT 12",
                (*video_ids, role),
            ).fetchall()
            if not rows:
                print("  (no matching assets)")
            for r in rows:
                good = r[2] == "ACTIVE" and r[3] == "INDEXED" and r[4] and r[5] and (r[6] or 0) > 0
                print(f"  {'OK ' if good else 'BAD'} {str(r[0])[:46]:46s} vid={str(r[1])[:14]:14s} {r[2]}/{r[3]} drive={'yes' if r[4] else 'no'} link={'yes' if r[5] else 'no'} dur_ms={r[6]}")
    conn.close()


if __name__ == "__main__":
    main()
