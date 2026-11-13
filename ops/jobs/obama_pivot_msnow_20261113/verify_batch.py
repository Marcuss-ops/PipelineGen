#!/usr/bin/env python3
"""Verify the Pivot / MS NOW batch after the service restart."""
from __future__ import annotations

import json
import os
import urllib.request
from pathlib import Path

HERE = Path(__file__).resolve().parent
REFACTORED = HERE.parents[2]
PREVIOUS = REFACTORED / "ops/jobs/obama_20261113/state.json"
OUT = HERE / "asset-verification.json"
BASE = "http://127.0.0.1:8000"


def token() -> str:
    tok = os.environ.get("VELOX_ADMIN_TOKEN", "").strip()
    if tok:
        return tok
    for line in (REFACTORED / ".env").read_text(errors="ignore").splitlines():
        if line.strip().startswith("VELOX_ADMIN_TOKEN="):
            return line.split("=", 1)[1].strip().strip('"').strip("'")
    raise RuntimeError("VELOX_ADMIN_TOKEN not available")


TOKEN = token()


def get_asset(asset_id: str) -> dict:
    req = urllib.request.Request(
        f"{BASE}/api/assets/operator/assets/{asset_id}",
        headers={"X-Velox-Admin-Token": TOKEN},
    )
    return json.load(urllib.request.urlopen(req, timeout=30))


def inspect(asset_id: str) -> dict:
    d = get_asset(asset_id)
    return {
        "id": asset_id,
        "index_state": d.get("index_state"),
        "has_embedding": d.get("has_embedding"),
        "pending_outbox_events": d.get("pending_outbox_events"),
        "languages": sorted({t.get("language_code") for t in (d.get("text_tracks") or [])}),
        "index_health": (d.get("index_health") or {}).get("code"),
    }


def main() -> None:
    state = json.loads((HERE / "state.json").read_text())
    manifest = json.loads((HERE / "manifest.json").read_text())
    expected = {str(v["n"]): len(v["clips"]) for v in manifest["videos"]}
    previous = json.loads(PREVIOUS.read_text()) if PREVIOUS.is_file() else {"videos": {}}

    assets = []
    problems = []
    drive_refs = 0
    for n, video in state.get("videos", {}).items():
        ids = video.get("asset_ids") or []
        items = video.get("items") or []
        if len(ids) != expected.get(n):
            problems.append(f"video {n}: {len(ids)} assets, expected {expected.get(n)}")
        drive_refs += sum(1 for it in items if it.get("drive_file_id"))
        for aid in ids:
            row = inspect(aid)
            row.update(
                {
                    "video": n,
                    "video_id": video.get("video_id"),
                    "drive_file_id": next(
                        (it.get("drive_file_id") for it in items if it.get("id") == aid),
                        None,
                    ),
                }
            )
            assets.append(row)
            if row["index_state"] != "INDEXED":
                problems.append(f"{aid}: index_state={row['index_state']}")
            if not row["has_embedding"]:
                problems.append(f"{aid}: no embedding")
            if row["pending_outbox_events"] != 0:
                problems.append(f"{aid}: pending={row['pending_outbox_events']}")
            if row["languages"] != ["en"]:
                problems.append(f"{aid}: languages={row['languages']}")
            if row["index_health"] in (None, "PENDING"):
                problems.append(f"{aid}: health={row['index_health']}")
            if not row["drive_file_id"]:
                problems.append(f"{aid}: no drive_file_id")
        print(f"video {n} {video.get('video_id')}: {len(ids)} assets, {len(items)} items")

    previous_rows = []
    for n, video in previous.get("videos", {}).items():
        if video.get("video_id") not in ("xOl7HTXuLvk", "yvYCzh0BcmM"):
            continue
        for aid in video.get("asset_ids") or []:
            row = inspect(aid)
            row.update({"video": n, "video_id": video.get("video_id")})
            previous_rows.append(row)
            if row["index_state"] != "INDEXED" or not row["has_embedding"] or row["pending_outbox_events"] != 0:
                problems.append(f"pre-existing {aid}: not healthy: {row}")

    report = {
        "batch": "obama_pivot_msnow_20261113",
        "folder_id": "1n4YRewXB7F7nC5QozS8RmTF2tBLFDfFr",
        "total_new_assets": len(assets),
        "drive_refs": drive_refs,
        "languages": sorted({lang for a in assets for lang in a["languages"]}),
        "existing_assets_checked": len(previous_rows),
        "problems": problems,
        "assets": assets,
        "existing_assets": previous_rows,
    }
    OUT.write_text(json.dumps(report, indent=2, ensure_ascii=False))
    print("drive_refs", drive_refs, "/", len(assets))
    print("languages", report["languages"])
    print("existing_assets_checked", len(previous_rows))
    print("problems", problems)
    if problems:
        raise SystemExit(1)


if __name__ == "__main__":
    main()
