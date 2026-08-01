#!/usr/bin/env python3
"""Bounded single poller: wait for the 4 new canary assets to reach INDEXED.

Prints only when the set of states changes (mirrors wait_job.py cadence).
Exit 0 when all INDEXED; exit 1 on timeout.
"""

from __future__ import annotations

import os
import sqlite3
import sys
import time

DB = os.environ.get("VELOX_DB", "data/media/media.db.sqlite")
ASSET_IDS = [
    "planner:5e2d38ea738900c7:0",  # floyd_mayweather.interview
    "planner:5ed6d8be4522c746:0",  # floyd_mayweather.training
    "planner:4e682bc70fb1a841:0",  # sugar_ray_robinson.interview
    "planner:5de851e34742d4bb:0",  # sugar_ray_robinson.training
]
TIMEOUT = float(os.environ.get("CANARY_WAIT_TIMEOUT", "420"))
INTERVAL = 10


def short_name(asset_id: str) -> str:
    return asset_id.split(":")[1][:6]


def main() -> int:
    conn = sqlite3.connect(DB)
    deadline = time.monotonic() + TIMEOUT
    last = ""
    while time.monotonic() < deadline:
        rows = conn.execute(
            "SELECT id, index_state, lifecycle_state FROM media_assets WHERE id IN (?,?,?,?)",
            ASSET_IDS,
        ).fetchall()
        states = {r[0]: (r[1], r[2]) for r in rows}
        summary = ", ".join(
            f"{short_name(a)}:{states.get(a, ('ABSENT', ''))[0][:9]}" for a in ASSET_IDS
        )
        if summary != last:
            print(f"[{time.strftime('%H:%M:%S')}] {summary}", flush=True)
            last = summary
        if all(states.get(a) and states[a][0] == "INDEXED" for a in ASSET_IDS):
            print("ALL_INDEXED", flush=True)
            conn.close()
            return 0
        time.sleep(INTERVAL)
    conn.close()
    print("TIMEOUT waiting for INDEXED", flush=True)
    return 1


if __name__ == "__main__":
    raise SystemExit(main())
