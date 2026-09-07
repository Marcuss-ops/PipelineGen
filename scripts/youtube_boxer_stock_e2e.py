#!/usr/bin/env python3
"""Run and audit the real YouTube stock chain for one boxer.

The runner submits one bounded multi-clip stock job per source, with a
bounded number of source jobs in flight. A green HTTP enqueue is never treated as success:
every job is polled, then SQLite and Drive are checked for the selected
profile's counts and canonical provenance.
"""

from __future__ import annotations

import argparse
from concurrent.futures import ThreadPoolExecutor, as_completed
import json
import os
import sqlite3
import statistics
import subprocess
import time
import urllib.error
import urllib.parse
import urllib.request
from collections import Counter
from datetime import datetime, timezone
from pathlib import Path
from typing import Any, NamedTuple

TERMINAL = {"SUCCEEDED", "COMPLETED", "FAILED", "CANCELLED", "DEAD_LETTERED"}

# ── Contract constants (July 2026) ──────────────────────────────────────
# The full profile produces 20 minutes per boxer. The canary profile is a
# deliberately smaller preflight: one fight source and three clips.
TARGET_VIDEOS = 20
TARGET_CLIPS_PER_VIDEO = 15
CLIP_DURATION_SECONDS = 4
CLIP_DURATION_MIN_SEC = 3.8             # ffprobe tolerance
CLIP_DURATION_MAX_SEC = 4.2
MAX_CONCURRENCY = 4                      # matches the media.stock worker budget



# The helper functions below were split out of this single-file e2e runner
# (scripts/youtube_boxer_stock_e2e_partN.py) so every physical file stays
# under 400 lines. They are exec'd in order into this module namespace --
# identical to the former single top-to-bottom script, with no import
# rewiring and no behavior change.
import os

_here = os.path.dirname(os.path.abspath(__file__))
for _name in (
    "youtube_boxer_stock_e2e_part1.py",
    "youtube_boxer_stock_e2e_part2.py",
    "youtube_boxer_stock_e2e_part3.py",
):
    with open(os.path.join(_here, _name), encoding="utf-8") as _f:
        exec(compile(_f.read(), _name, "exec"))


def main() -> int:
    ap = argparse.ArgumentParser()
    ap.add_argument("boxer", nargs="?")
    ap.add_argument("--preflight-auth", action="store_true")
    ap.add_argument("--preflight-report", default="out/01_youtube_auth_preflight.json")
    ap.add_argument("--base", default=os.environ.get("VELOX_BASE_URL", "http://127.0.0.1:8000"))
    ap.add_argument("--db", default="data/media/media.db.sqlite")
    ap.add_argument("--root-id", default=os.environ.get("YOUTUBE_STOCK_ROOT_ID", ""))
    ap.add_argument("--folder-id", default="")
    ap.add_argument("--verify-only", action="store_true")
    ap.add_argument("--profile", choices=sorted(PROFILES), default="full")
    ap.add_argument("--concurrency", type=int, default=MAX_CONCURRENCY)
    ap.add_argument("--manifest", default="", help="persisted source manifest; reused unless --refresh-manifest is set")
    ap.add_argument("--refresh-manifest", action="store_true")
    ap.add_argument("--staging-ttl-seconds", type=int, default=1800)
    ap.add_argument("--staging-max-bytes", type=int, default=int(os.environ.get("VELOX_STOCK_STAGING_MAX_BYTES", "0")))
    ap.add_argument("--timings-report", default="", help="atomic JSON report with per-source timings and p50/p95")
    ap.add_argument("--destination-name", default="", help="Drive subfolder name; defaults to the canonical boxer name")
    args = ap.parse_args()
    if args.preflight_auth:
        report = run_auth_preflight()
        write_auth_preflight_report(report, args.preflight_report)
        return 0 if report["youtube_auth"] == "PASS" else 1
    if not args.boxer:
        ap.error("boxer is required unless --preflight-auth is used")
    profile = profile_for(args.profile)
    # Keep the display name used for Drive/folder_path separate from the
    # deterministic slug used in idempotency keys and reports.
    args.boxer = canonical_boxer_name(args.boxer)
    run_id = datetime.now(timezone.utc).strftime("%Y%m%dT%H%M%S%fZ")
    os.environ["VELOX_STOCK_RUN_ID"] = run_id
    manifest_path = Path(args.manifest) if args.manifest else Path("out") / boxer_slug(args.boxer) / f"{profile.name}.manifest.json"
    cleanup_stale_staging(
        Path("data/tmp"),
        max(60, args.staging_ttl_seconds),
        max(0, args.staging_max_bytes),
    )
    token = os.environ.get("VELOX_ADMIN_TOKEN", "")
    if not token:
        raise SystemExit("VELOX_ADMIN_TOKEN is required")
    if args.verify_only:
        target = args.folder_id
        if not target:
            raise SystemExit("--folder-id is required with --verify-only")
        expected_sources = expected_source_video_ids(args.boxer, profile)
        if manifest_path.exists():
            persisted = json.loads(manifest_path.read_text(encoding="utf-8"))
            if persisted.get("schema") != "velox.youtube-stock-manifest.v1":
                raise SystemExit(f"invalid persisted manifest schema: {manifest_path}")
            if persisted.get("boxer") != args.boxer or persisted.get("profile") != profile.name:
                raise SystemExit(f"persisted manifest does not match boxer/profile: {manifest_path}")
            expected_sources = {str(video.get("video_id")) for video in persisted.get("videos", []) if video.get("video_id")}
            if len(expected_sources) != profile.videos:
                raise SystemExit(f"persisted manifest must contain exactly {profile.videos} sources")
        if expected_sources is None:
            raise SystemExit(
                "--verify-only requires a static manifest for the selected boxer; "
                "run acquisition first or provide a persisted run manifest"
            )
        verify(
            Path(args.db),
            target,
            args.boxer,
            profile,
            expected_source_video_ids=expected_sources,
        )
        return 0
    selected = select(args.base, token, args.boxer, profile, manifest_path, args.refresh_manifest)
    write_manifest(manifest_path, args.boxer, profile, selected)
    if args.folder_id:
        target = args.folder_id
        upload_parent = target
    elif args.root_id:
        target = folder(args.base, token, args.root_id, args.boxer)
        # The stock endpoint owns creation/reuse of the boxer folder. Pass the
        # explicit stock parent so it does not create Mike Tyson/Mike Tyson.
        upload_parent = args.root_id
    else:
        target = resolve_boxe_folder(args.base, token)
        upload_parent = target
    if not target:
        raise SystemExit("could not resolve/create boxer Drive folder")
    print(
        f"selected={len(selected)} profile={profile.name} "
        f"clips={profile.total_clips} boxer={args.boxer} slug={boxer_slug(args.boxer)}"
    )
    # The server-side worker pool is independently bounded. Keep this
    # client fan-out at or below the safe limit for YouTube, Drive, FFmpeg,
    # and SQLite, even when a caller requests a larger value.
    workers = bounded_concurrency(args.concurrency)
    timing_records: list[dict[str, Any]] = []
    with ThreadPoolExecutor(max_workers=workers, thread_name_prefix="youtube-stock") as pool:
        futures = [pool.submit(run_source, args.base, token, Path(args.db), args.boxer, upload_parent, video, index, profile, args.destination_name or None)
                   for index, video in enumerate(selected, 1)]
        for future in as_completed(futures):
            timing_records.append(future.result())
    verify(
        Path(args.db),
        target,
        args.boxer,
        profile,
        expected_source_video_ids={video["video_id"] for video in selected},
        expected_run_id=run_id,
    )
    timing_path = Path(args.timings_report) if args.timings_report else Path("out") / boxer_slug(args.boxer) / f"{profile.name}.timings.json"
    write_timing_report(timing_path, args.boxer, profile, sorted(timing_records, key=lambda item: item["video_id"]))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
