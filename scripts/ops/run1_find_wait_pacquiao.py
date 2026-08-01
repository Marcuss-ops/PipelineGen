#!/usr/bin/env python3
"""RUN1 prepare-stock — find the 3 newly acquired manny_pacquiao assets and
wait for them to reach INDEXED.

The per-role video IDs are derived from the canonical
MANNY_PACQUIAO_MANIFEST (imported from scripts/youtube_boxer_stock_e2e.py,
SSOT) so the finder stays in lockstep with run1_acquire_pacquiao.py. Queries
media_assets by source_video_id + category, newest first, and prints a JSON
mapping slot -> {asset_id, drive_link} once each asset is
ACTIVE+INDEXED+drive, so the registry can be rebound.

Usage:
    python3 scripts/ops/run1_find_wait_pacquiao.py
"""

from __future__ import annotations

import importlib.util
import json
import sqlite3
import sys
import time
from pathlib import Path

DB = "data/media/media.db.sqlite"
RUNNER = Path("scripts/youtube_boxer_stock_e2e.py").resolve()
BOXER = "Manny Pacquiao"
TIMEOUT_SECONDS = 420


def manifest_role_videos() -> dict[str, str]:
    """Return {role: first_video_id} from the canonical manifest (SSOT).

    Mirrors run1_acquire_pacquiao.py so acquirer and finder stay in lockstep
    with MANNY_PACQUIAO_MANIFEST instead of hardcoding video IDs.
    """
    spec = importlib.util.spec_from_file_location("boxer_e2e_runner", RUNNER)
    module = importlib.util.module_from_spec(spec)
    sys.modules["boxer_e2e_runner"] = module
    assert spec.loader is not None
    spec.loader.exec_module(module)
    manifest = module.manifest_for_boxer(BOXER)
    if manifest is None:
        raise SystemExit(f"no manifest for {BOXER}")
    return {role: videos[0] for role, videos in manifest}


def main() -> int:
    roles = manifest_role_videos()
    conn = sqlite3.connect(DB)
    deadline = time.monotonic() + TIMEOUT_SECONDS
    last = ""
    found: dict[str, dict[str, str]] = {}
    while time.monotonic() < deadline:
        found = {}
        summary = []
        for role, video_id in roles.items():
            rows = conn.execute(
                """SELECT id, lifecycle_state, index_state, drive_link
                   FROM media_assets
                   WHERE source_video_id = ? AND category = ?
                   ORDER BY updated_at DESC LIMIT 1""",
                (video_id, role),
            ).fetchall()
            if not rows:
                summary.append(f"{role}:ABSENT")
                continue
            aid, _, index_state, _ = rows[0]
            summary.append(f"{role}:{index_state or '?'}")

            row = conn.execute(
                """SELECT id, drive_link FROM media_assets
                   WHERE id = ? AND lifecycle_state = 'ACTIVE'
                     AND index_state = 'INDEXED' AND drive_link != ''""",
                (aid,),
            ).fetchone()
            if row:
                found[role] = {"asset_id": row[0], "drive_link": row[1]}
        state = ", ".join(summary)
        if state != last:
            print(f"[{time.strftime('%H:%M:%S')}] {state}")
            last = state
        if len(found) == len(roles):
            print(json.dumps({"run1_pacquiao": found}, indent=2))
            conn.close()
            return 0
        time.sleep(10)
    conn.close()
    print("TIMEOUT waiting for manny_pacquiao assets to reach INDEXED", file=sys.stderr)
    return 1


if __name__ == "__main__":
    raise SystemExit(main())
