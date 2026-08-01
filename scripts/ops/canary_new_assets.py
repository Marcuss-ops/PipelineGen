#!/usr/bin/env python3
"""Find the new assets created by the canary role acquisition run.

Lists ACTIVE+INDEXED assets whose source_video_id matches the four missing
role manifests (floyd/sugar_ray interview/training), most recent first, so
the caller can pick concrete asset_ids + drive_links for the registry.
"""

from __future__ import annotations

import os
import sqlite3

DB = os.environ.get("VELOX_DB", "data/media/media.db.sqlite")

ROLES = {
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
    for boxer, roles in ROLES.items():
        for role, video_ids in roles.items():
            placeholders = ",".join("?" * len(video_ids))
            rows = conn.execute(
                f"SELECT id, source_video_id, lifecycle_state, index_state, drive_file_id, drive_link, "
                f"duration_ms, created_at, updated_at, folder_path "
                f"FROM media_assets WHERE source_video_id IN ({placeholders}) AND category=? "
                f"ORDER BY COALESCE(updated_at, '') DESC, id LIMIT 5",
                (*video_ids, role),
            ).fetchall()
            print(f"--- {boxer} {role} ---")
            if not rows:
                print("  (none)")
            for r in rows:
                print(
                    f"  id={str(r[0])[:48]:48s} vid={str(r[1])[:14]:14s} {r[2]}/{r[3]} "
                    f"drive={'yes' if r[4] else 'no'} dur={r[6]} updated={str(r[8])[:19]}\n"
                    f"      link={r[5]}\n"
                    f"      folder={str(r[9])[:60]}"
                )
    conn.close()


if __name__ == "__main__":
    main()
