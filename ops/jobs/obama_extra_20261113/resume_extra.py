#!/usr/bin/env python3
"""Resume the extra batch after the video-1 backfill failed on a missing env.

The clips are already committed; only search_text/index repair + verification
remain for video 1. Run this with `.env` exported so bin/admin sees the
PostgreSQL media DSN. Then rr.main() processes video 2 normally.
"""
from __future__ import annotations

import json
import sys
import time
from pathlib import Path

HERE = Path(__file__).resolve().parent
sys.path.insert(0, str(HERE))

import run_extra as re  # noqa: E402

rr = re.rr


def main() -> None:
    state = json.loads(rr.STATE.read_text())
    video = state["videos"]["1"]
    if video.get("status") != "done":
        asset_ids = video["asset_ids"]
        rr.log(f"resuming video 1: backfill {len(asset_ids)} committed assets")
        report = rr.run_backfill(asset_ids)
        video["backfill"] = {
            k: report.get(k)
            for k in (
                "processed",
                "source_ready_count",
                "source_acquired_count",
                "created_total",
                "index_repaired_total",
                "reindex_requested_total",
                "subtitles_delivered_total",
                "failed_lang_total",
                "failed_asset_ids",
                "duration_ms",
            )
        }
        rr.save_state(state)
        rows = rr.wait_assets(asset_ids)
        video["assets"] = rows
        video["status"] = "done"
        video["finished_at"] = time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime())
        video.pop("error", None)
        rr.save_state(state)
        rr.log(f"video 1 DONE: {len(asset_ids)} clips indexed en")

    rr.main()


if __name__ == "__main__":
    main()
