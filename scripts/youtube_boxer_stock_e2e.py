#!/usr/bin/env python3
"""Run and audit the real YouTube stock chain for one boxer.

The runner deliberately submits one source job at a time.  A green HTTP
enqueue is never treated as success: every job is polled, then SQLite and
Drive are checked for the final counts and canonical provenance.
"""

from __future__ import annotations

import argparse
from concurrent.futures import ThreadPoolExecutor, as_completed
import json
import os
import sqlite3
import subprocess
import time
import urllib.error
import urllib.parse
import urllib.request
from collections import Counter
from datetime import datetime, timezone
from pathlib import Path
from typing import Any

TERMINAL = {"SUCCEEDED", "COMPLETED", "FAILED", "CANCELLED", "DEAD_LETTERED"}

# ── Contract constants (July 2026) ──────────────────────────────────────
# Every boxer MUST produce exactly 20 minutes of stock, never more, never
# less.  PER-SOURCE gates are as important as the global totals, because a
# single misconfigured source that downloads the full video would silently
# inflate the aggregate while the per-source tolerance catches it.
TARGET_VIDEOS = 20
TARGET_CLIPS_PER_VIDEO = 15
CLIP_DURATION_SECONDS = 4
TARGET_SECONDS_PER_SOURCE = 60          # TARGET_CLIPS_PER_VIDEO × CLIP_DURATION_SECONDS
TARGET_TOTAL_SECONDS = 1_200            # TARGET_VIDEOS × TARGET_SECONDS_PER_SOURCE
TARGET_TOTAL_MS = TARGET_VIDEOS * TARGET_SECONDS_PER_SOURCE * 1000
PER_SOURCE_MIN_MS = 57_000              # ─2×CLIP_DURATION_SECONDS tolerance
PER_SOURCE_MAX_MS = 63_000
CLIP_DURATION_MIN_SEC = 3.8             # ffprobe tolerance
CLIP_DURATION_MAX_SEC = 4.2

QUERIES = {
    "fight": ("{name} fights", "{name} knockout", "{name} highlights", "{name} boxing fight"),
    "interview": ("{name} interview", "{name} press conference", "{name} documentary interview"),
    "training": ("{name} training", "{name} workout", "{name} backstage"),
}

MIKE_TYSON_MANIFEST = (
    ("fight", ("0vnOfawuQF4", "pHXurRbFTss", "jfpUia2gJjg", "CfV8oYVYa_k", "O47EW2WWe28", "2Q-5DCL99JY", "lNPpojcSMgI", "3yYRQIQN8jQ", "isaR1SyVzoE", "tG2B90evafc", "aJ-AUIkBX_Y", "1ee-NU7Lp5Y")),
    ("interview", ("iHaK0M-207o", "LkWU9lB2zEQ", "OOhdx1TLutw", "7MNv4_rTkfU", "NrPWFWd8cVM", "pTrAokYX3CY")),
    ("training", ("ItX74qZf2o0", "K6i9tkWOXhs")),
)

# Keep the pilot's source set deterministic.  Dynamic search is useful for
# discovery, but rerunning it can replace a source with a new candidate and
# silently turn a 20-source run into 21 sources.  This manifest is the set
# already validated for the Ali pilot (13 fight, 5 interview, 2 training).
MUHAMMAD_ALI_MANIFEST = (
    ("fight", ("wjWFnnC7edI", "v9VkFC3SRXI", "pK0CF_CfrD0", "oLA8HIAQEzU", "oJUzl0aFHZw", "eIm2eK5uuVA", "bI-O40Hcnj8", "VFFDe9FQL3s", "RtINcMrdKY0", "I7P0oXNcL_o", "EhGWj-lvg-w", "6kEmuFoEy54", "-3BzkEwUNY8")),
    ("interview", ("lwfMZbkDttg", "R0iAWPDwYvo", "J8ZWZzt0bkQ", "HqiWFLsgVi4", "8CQTVRwi9Fk")),
    ("training", ("V7tfxuocZoM", "75fPUKEP7Ws")),
)

MANNY_PACQUIAO_MANIFEST = (
    ("fight", ("kUruG4y9mak", "VJAk5sy1xoI", "2Esu9upLw88", "Y6FXD5IbLDI", "Sl4V0e2Odqw", "w1BzF2CRC6o", "wgVMjmL2mJk", "ZogCWQST3us", "QddTpJ4aYo8", "CGknwOjzPTM", "bwsv5NrZn6Y", "BVpAM5leGv0")),
    ("interview", ("BKHQS8sj7iw", "yyIzSnJPOXY", "LKfGGQnSuJg", "4jm7XyE3Aa4", "DdtkvPHw6yI", "HzNyry9DNhk")),
    ("training", ("GGsJ9SHlA0o", "wMTdHR2c5ic")),
)

FLOYD_MAYWEATHER_MANIFEST = (
    ("fight", ("Yj3GM2L6nag", "39zhhfMGNRk", "D8y53m388nM", "KYvOC7MBuUw", "Z0qvcHTEPqg", "66Dg_n0H8rQ", "fKiGQfpupRA", "dXq8P_37lMg", "3DpkVOvuA0Y", "tA14uRHqqWs", "Sw51Rjd1BWY", "PnbJWE2wvpg")),
    ("interview", ("1gjZjirv740", "RVc37DH7Sns", "Kxb9AUmSrIA", "1UA6zGsvkrw", "RXw8fJDTb5I", "J6Zert5VaWk")),
    ("training", ("XVU8e6YGTY4", "sNumtUs8d6M")),
)

SUGAR_RAY_ROBINSON_MANIFEST = (
    ("fight", ("-dixu5le9NI", "H4eP1TTedYc", "eobxArm4tDA", "3BBtxCqNFBA", "HmVSxcShBqg", "cOCDmL4F3nM", "4FzA5frXpzI", "QvDCTmK0Naw", "80RUvhi5uaI", "gOdNYYY99GU", "Ey37kbCfozQ", "n_M4SFK8NCc")),
    ("interview", ("naPht4IBx4w", "ohQYcpFSKs0", "Xi-2E5QcXtQ", "4LNQKtq5SEw", "CrAMsMCb2bg", "iuRLVCEdUUo")),
    ("training", ("FQivVOx8SnM", "7D3_UMN97gI")),
)


def http(base: str, token: str, method: str, path: str, body: Any = None, request_id: str = "") -> dict[str, Any]:
    raw = None if body is None else json.dumps(body).encode()
    req = urllib.request.Request(base.rstrip("/") + path, data=raw, method=method)
    req.add_header("Authorization", f"Bearer {token}")
    req.add_header("Content-Type", "application/json")
    if request_id:
        req.add_header("Idempotency-Key", request_id)
    try:
        with urllib.request.urlopen(req, timeout=90) as resp:
            return json.loads(resp.read())
    except urllib.error.HTTPError as exc:
        raise RuntimeError(f"HTTP {exc.code} {path}: {exc.read().decode(errors='replace')}") from exc


def search(_base: str, _token: str, boxer: str, category: str) -> list[dict[str, Any]]:
    out: list[dict[str, Any]] = []
    seen: set[str] = set()
    for template in QUERIES[category]:
        query = template.format(name=boxer)
        command = ["yt-dlp", "--js-runtime", "node", "--remote-components", "ejs:github",
                   "--no-warnings", "--extractor-args", "youtube:player_client=android_creator",
                   "--flat-playlist", "--dump-single-json", f"ytsearch10:{query}"]
        try:
            raw = subprocess.check_output(command, text=True, timeout=90, stderr=subprocess.DEVNULL)
            entries = json.loads(raw).get("entries", [])
        except (OSError, subprocess.SubprocessError, json.JSONDecodeError) as exc:
            raise RuntimeError(f"YouTube search failed for {query!r}: {exc}") from exc
        for item in entries:
            video_id = str(item.get("id") or "")
            duration = float(item.get("duration", 0) or 0)
            if not video_id or video_id in seen or duration < 64:
                continue
            seen.add(video_id)
            url = f"https://www.youtube.com/watch?v={video_id}"
            out.append({"video_id": video_id, "url": url, "title": item.get("title", ""),
                        "channel": item.get("channel", item.get("uploader", "")),
                        "duration": duration, "category": category})
    return out


def describe(video_id: str, category: str) -> dict[str, Any]:
    command = ["yt-dlp", "--js-runtime", "node", "--remote-components", "ejs:github",
               "--no-warnings", "--extractor-args", "youtube:player_client=android_creator",
               "--dump-single-json", "--skip-download", f"https://www.youtube.com/watch?v={video_id}"]
    try:
        item = json.loads(subprocess.check_output(command, text=True, timeout=90, stderr=subprocess.DEVNULL))
    except (OSError, subprocess.SubprocessError, json.JSONDecodeError) as exc:
        raise RuntimeError(f"YouTube manifest video {video_id} unavailable: {exc}") from exc
    duration = float(item.get("duration", 0) or 0)
    if duration < 64:
        raise RuntimeError(f"YouTube manifest video {video_id} is too short: {duration}s")
    return {"video_id": video_id, "url": f"https://www.youtube.com/watch?v={video_id}",
            "title": item.get("title", ""), "channel": item.get("channel", item.get("uploader", "")),
            "duration": duration, "category": category}


def select(base: str, token: str, boxer: str) -> list[dict[str, Any]]:
    if boxer.casefold() == "mike tyson":
        selected = [describe(video_id, category) for category, ids in MIKE_TYSON_MANIFEST for video_id in ids]
        if len(selected) != 20 or len({item["video_id"] for item in selected}) != 20:
            raise RuntimeError("Mike Tyson persisted manifest is not exactly 20 unique videos")
        return selected
    if boxer.casefold() == "muhammad ali":
        selected = [describe(video_id, category) for category, ids in MUHAMMAD_ALI_MANIFEST for video_id in ids]
        if len(selected) != 20 or len({item["video_id"] for item in selected}) != 20:
            raise RuntimeError("Muhammad Ali persisted manifest is not exactly 20 unique videos")
        return selected
    if boxer.casefold() == "manny pacquiao":
        selected = [describe(video_id, category) for category, ids in MANNY_PACQUIAO_MANIFEST for video_id in ids]
        if len(selected) != 20 or len({item["video_id"] for item in selected}) != 20:
            raise RuntimeError("Manny Pacquiao persisted manifest is not exactly 20 unique videos")
        return selected
    if boxer.casefold() == "floyd mayweather jr.":
        selected = [describe(video_id, category) for category, ids in FLOYD_MAYWEATHER_MANIFEST for video_id in ids]
        if len(selected) != 20 or len({item["video_id"] for item in selected}) != 20:
            raise RuntimeError("Floyd Mayweather Jr. manifest is not exactly 20 unique videos")
        return selected
    if boxer.casefold() == "sugar ray robinson":
        selected = [describe(video_id, category) for category, ids in SUGAR_RAY_ROBINSON_MANIFEST for video_id in ids]
        if len(selected) != 20 or len({item["video_id"] for item in selected}) != 20:
            raise RuntimeError("Sugar Ray Robinson manifest is not exactly 20 unique videos")
        return selected
    selected: list[dict[str, Any]] = []
    for category, wanted in (("fight", 12), ("interview", 6), ("training", 2)):
        candidates = search(base, token, boxer, category)
        if len(candidates) < wanted:
            raise RuntimeError(f"{category}: only {len(candidates)} usable candidates, need {wanted}")
        selected.extend(candidates[:wanted])
    if len({item["video_id"] for item in selected}) != 20:
        raise RuntimeError("selection contains duplicate video IDs")
    return selected


def folder(base: str, token: str, root_id: str, boxer: str) -> str:
    listing = http(base, token, "GET", f"/api/drive/files?folder_id={urllib.parse.quote(root_id)}")
    matches = [f for f in listing.get("files", []) if f.get("mime_type") == "application/vnd.google-apps.folder" and f.get("name", "").casefold() == boxer.casefold()]
    if len(matches) > 1:
        raise RuntimeError(f"multiple Drive folders named {boxer!r} under root")
    if matches:
        return str(matches[0]["id"])
    created = http(base, token, "POST", "/api/drive/folders", {"parent_id": root_id, "folders": [boxer]})
    return str(created.get("created", {}).get(boxer, ""))


def segments(video: dict[str, Any]) -> list[dict[str, Any]]:
    duration = int(video["duration"])
    # Fifteen non-overlapping windows distributed over the source, avoiding
    # the first/last few seconds where intros/outros are commonly black.
    usable = max(60, duration - 8)
    step = max(4, usable // 16)
    return [{"start": f"{start // 60:02d}:{start % 60:02d}",
             "end": f"{end // 60:02d}:{end % 60:02d}",
             "name": video.get("title", ""),
             "source_title": video.get("title", ""),
             "source_channel": video.get("channel", ""),
             "category": video["category"],
             "description": f"{video['category']} scene featuring {video.get('title') or video['video_id']}"}
            for i in range(TARGET_CLIPS_PER_VIDEO)
            for start, end in [(8 + i * step, 12 + i * step)]]


def latest_job(db: Path, video: dict[str, Any], segment: dict[str, Any], submitted_at: str) -> str:
    with sqlite3.connect(db) as conn:
        row = conn.execute(
            "SELECT id FROM jobs WHERE type='youtube_clip.extract' AND created_at>=? AND payload_json LIKE ? AND payload_json LIKE ? ORDER BY created_at DESC LIMIT 1",
            (submitted_at, f"%{video['video_id']}%", f"%{segment['start']}%"),
        ).fetchone()
    if not row:
        raise RuntimeError(f"enqueue returned no correlated YouTube job for {video['video_id']} {segment['start']}")
    return str(row[0])


def clip_id(video_id: str, segment: dict[str, Any]) -> str:
    def seconds(value: str) -> int:
        parts = [int(part) for part in value.split(":")]
        return parts[-1] + (parts[-2] * 60 if len(parts) > 1 else 0)
    return f"yt_{video_id}_{seconds(segment['start'])}_{seconds(segment['end'])}_v1"


def wait_asset(db: Path, asset_id: str, timeout: int = 180) -> None:
    deadline = time.monotonic() + timeout
    while time.monotonic() < deadline:
        with sqlite3.connect(db) as conn:
            row = conn.execute("SELECT drive_file_id, file_hash, duration_ms FROM media_assets WHERE id=?", (asset_id,)).fetchone()
        if row and row[0] and row[1] and row[2] and row[2] > 0:
            return
        time.sleep(3)
    raise RuntimeError(f"job reported success but asset {asset_id} was not durably persisted")


def asset_complete(db: Path, asset_id: str) -> bool:
    with sqlite3.connect(db) as conn:
        row = conn.execute("""
          SELECT drive_file_id, file_hash, duration_ms,
                 json_extract(metadata_json, '$.source_provider'),
                 json_extract(metadata_json, '$.video_id'),
                 json_extract(metadata_json, '$.source_title'),
                 json_extract(metadata_json, '$.source_channel')
          FROM media_assets WHERE id=? AND lifecycle_state='ACTIVE'
        """, (asset_id,)).fetchone()
    return bool(row and row[0] and row[1] and row[2] and row[2] > 0 and all(row[3:]))


def run_source(base: str, token: str, db: Path, boxer: str, folder_id: str, video: dict[str, Any], index: int) -> None:
    for clip_index, segment in enumerate(segments(video), 1):
        asset_id = clip_id(video["video_id"], segment)
        if asset_complete(db, asset_id):
            print(f"[{index:02d}/20] {video['category']:<9} {video['video_id']} clip {clip_index:02d}/15 CACHED")
            continue
        payload = {"url": video["url"], "segments": [segment], "strategy": "replace",
                   "destination": {"folder_id": folder_id, "folder_path": boxer, "create_subfolder": False}}
        # One segment per job is intentional.  The multi-segment fan-out
        # currently has a reproducible RUNNING-at-5% deadlock; atomising the
        # work preserves the 15 clips/video contract and makes retries exact.
        last_error = ""
        for attempt in range(1, 4):
            request_id = f"youtube-stock-{boxer.casefold().replace(' ', '-')}-{video['video_id']}-{clip_index:02d}-v17-a{attempt}"
            submitted_at = datetime.now(timezone.utc).isoformat(timespec="seconds").replace("+00:00", "Z")
            accepted = http(base, token, "POST", "/api/clips/process", payload, request_id)
            job_id = str(accepted.get("job_id") or accepted.get("id") or accepted.get("job", {}).get("id") or "")
            if not job_id:
                job_id = latest_job(db, video, segment, submitted_at)
            deadline = time.monotonic() + 600
            while time.monotonic() < deadline:
                result = http(base, token, "GET", f"/api/jobs/{job_id}/full")
                state = str(result.get("status", result.get("state", ""))).upper()
                if state in TERMINAL:
                    if state in {"SUCCEEDED", "COMPLETED"}:
                        wait_asset(db, asset_id)
                        print(f"[{index:02d}/20] {video['category']:<9} {video['video_id']} clip {clip_index:02d}/15 SUCCEEDED")
                        break
                    last_error = f"job {job_id} ended {state}: {result.get('error', '')}"
                    break
                time.sleep(3)
            else:
                last_error = f"timeout waiting for {job_id}"
            if asset_complete(db, asset_id):
                break
            if attempt < 3:
                print(f"[{index:02d}/20] {video['category']:<9} {video['video_id']} clip {clip_index:02d}/15 retry {attempt + 1}/3: {last_error}")
        else:
            raise RuntimeError(f"{video['video_id']} clip {clip_index}: {last_error}")


def verify(db: Path, folder_id: str, boxer: str) -> None:
    with sqlite3.connect(db) as conn:
        row = conn.execute("""
          SELECT COUNT(*), COUNT(DISTINCT source_video_id), COALESCE(SUM(duration_ms),0),
                 COUNT(DISTINCT file_hash), SUM(CASE WHEN drive_file_id='' THEN 1 ELSE 0 END),
                 SUM(CASE WHEN source_video_id='' OR source_url='' OR category='' OR duration_ms<=0 THEN 1 ELSE 0 END)
          FROM media_assets WHERE source='youtube' AND lifecycle_state='ACTIVE' AND folder_id=? AND LOWER(folder_path)=LOWER(?)
        """, (folder_id, boxer)).fetchone()
        per_source = conn.execute("SELECT source_video_id, COUNT(*) FROM media_assets WHERE source='youtube' AND lifecycle_state='ACTIVE' AND folder_id=? AND LOWER(folder_path)=LOWER(?) GROUP BY source_video_id", (folder_id, boxer)).fetchall()
        paths = [r[0] for r in conn.execute("SELECT local_path FROM media_assets WHERE source='youtube' AND lifecycle_state='ACTIVE' AND folder_id=? AND LOWER(folder_path)=LOWER(?)", (folder_id, boxer))]
    count, videos, duration, hashes, missing_drive, incomplete = row
    if (count, videos, duration, hashes, missing_drive, incomplete) != (TARGET_VIDEOS * TARGET_CLIPS_PER_VIDEO, TARGET_VIDEOS, TARGET_TOTAL_MS, TARGET_VIDEOS * TARGET_CLIPS_PER_VIDEO, 0, 0):
        raise RuntimeError(f"SQLite gate failed: clips={count}, videos={videos}, duration_ms={duration}, hashes={hashes}, missing_drive={missing_drive}, incomplete={incomplete}")
    if any(n > TARGET_CLIPS_PER_VIDEO for _, n in per_source):
        raise RuntimeError("more than 15 clips found for a source video")
    # ── Per-source duration gate ────────────────────────────────────────
    per_dur = conn.execute("""
      SELECT source_video_id, COUNT(*) AS clip_count, SUM(duration_ms) AS total_duration_ms
      FROM media_assets
      WHERE source='youtube' AND lifecycle_state='ACTIVE' AND folder_id=? AND LOWER(folder_path)=LOWER(?)
      GROUP BY source_video_id
      HAVING COUNT(*) != ? OR SUM(duration_ms) < ? OR SUM(duration_ms) > ?
    """, (folder_id, boxer, TARGET_CLIPS_PER_VIDEO, PER_SOURCE_MIN_MS, PER_SOURCE_MAX_MS)).fetchall()
    if per_dur:
        offenders = [f"{r[0]} clips={r[1]} dur_ms={r[2]}" for r in per_dur]
        raise RuntimeError(f"per-source duration gate failed ({len(offenders)} sources): {'; '.join(offenders[:5])}")
    bad_files = []
    for path in paths:
        clip = Path(path)
        if not path or not clip.is_file() or clip.stat().st_size <= 0:
            bad_files.append(f"missing:{path}")
            continue
        probe = subprocess.run(["ffprobe", "-v", "error", "-show_entries", "format=duration", "-of", "csv=p=0", path], capture_output=True, text=True, check=False)
        try:
            duration_sec = float(probe.stdout.strip())
        except ValueError:
            duration_sec = 0
        if probe.returncode != 0 or not CLIP_DURATION_MIN_SEC <= duration_sec <= CLIP_DURATION_MAX_SEC:
            bad_files.append(f"duration:{path}:{duration_sec:.3f}")
    if bad_files:
        raise RuntimeError(f"physical clip gate failed ({len(bad_files)} files): {bad_files[:3]}")
    print("SQLite gate: 300 clips, 20 videos, 20 real minutes, complete provenance")


def main() -> int:
    ap = argparse.ArgumentParser()
    ap.add_argument("boxer")
    ap.add_argument("--base", default=os.environ.get("VELOX_BASE_URL", "http://127.0.0.1:8000"))
    ap.add_argument("--db", default="data/media/media.db.sqlite")
    ap.add_argument("--root-id", default=os.environ.get("YOUTUBE_STOCK_ROOT_ID", ""))
    ap.add_argument("--folder-id", default="")
    ap.add_argument("--verify-only", action="store_true")
    ap.add_argument("--concurrency", type=int, default=2)
    args = ap.parse_args()
    # Normalise to lowercase so folder_path in SQLite is always consistent.
    # verify() already uses LOWER() for case-insensitive matching, but new
    # clips must also land with the same casing to avoid mixed-path bugs.
    args.boxer = args.boxer.strip().lower()
    token = os.environ.get("VELOX_ADMIN_TOKEN", "")
    if not token:
        raise SystemExit("VELOX_ADMIN_TOKEN is required")
    if args.verify_only:
        target = args.folder_id
        if not target:
            raise SystemExit("--folder-id is required with --verify-only")
        verify(Path(args.db), target, args.boxer)
        return 0
    if not args.root_id:
        raise SystemExit("YOUTUBE_STOCK_ROOT_ID is required")
    selected = select(args.base, token, args.boxer)
    target = folder(args.base, token, args.root_id, args.boxer)
    if not target:
        raise SystemExit("could not resolve/create boxer Drive folder")
    print(f"selected=20 folder_id={target}")
    # The server-side worker pool is independently bounded. Keep this
    # client fan-out controlled, but allow enough parallelism for the
    # per-clip metadata/index path to make a five-boxer run practical.
    workers = max(1, min(args.concurrency, 16))
    with ThreadPoolExecutor(max_workers=workers, thread_name_prefix="youtube-stock") as pool:
        futures = [pool.submit(run_source, args.base, token, Path(args.db), args.boxer, target, video, index)
                   for index, video in enumerate(selected, 1)]
        for future in as_completed(futures):
            future.result()
    verify(Path(args.db), target, args.boxer)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
