#!/usr/bin/env python3
"""Canary role acquisition — fill the 4 missing 15-asset slots live.

Submits one bounded stock job per missing role slot:
  floyd_mayweather    interview / training
  sugar_ray_robinson  interview / training

Imports the canonical boxer manifests from scripts/youtube_boxer_stock_e2e.py
so the manifest video IDs never drift from the source of truth. Resolves the
Boxe Drive root through the canonical alias resolver (canary-upload), then
POSTs /api/stock-pipeline/run with a 1-clip payload per role (canary-minimal).

Output: a JSON mapping slot -> job_id on stdout (machine-consumable for the
single wait_job.py poller). Token: canonical file SSOT (file over env).
"""

from __future__ import annotations

import importlib.util
import json
import os
import re
import sys
import time
import urllib.error
import urllib.request
from datetime import datetime, timezone
from pathlib import Path
from typing import Any

BASE = os.environ.get("VELOX_BASE_URL") or "http://127.0.0.1:8000"
TOKEN_FILE = "/etc/pipelinegen/pipelinegen.env"
RUNNER = Path("scripts/youtube_boxer_stock_e2e.py").resolve()

# (boxer_key, canonical boxer name, drive folder name, role). The drive
# folder name deliberately matches the existing folders already published for
# these boxers ("Floyd Mayweather" without "Jr.") so acquisition lands in the
# same Drive subtree instead of creating a parallel folder.
SLOTS = (
    ("floyd_mayweather", "Floyd Mayweather Jr.", "Floyd Mayweather", "interview"),
    ("floyd_mayweather", "Floyd Mayweather Jr.", "Floyd Mayweather", "training"),
    ("sugar_ray_robinson", "Sugar Ray Robinson", "Sugar Ray Robinson", "interview"),
    ("sugar_ray_robinson", "Sugar Ray Robinson", "Sugar Ray Robinson", "training"),
)


def load_runner() -> Any:
    spec = importlib.util.spec_from_file_location("boxer_e2e_runner", RUNNER)
    module = importlib.util.module_from_spec(spec)
    sys.modules["boxer_e2e_runner"] = module
    assert spec.loader is not None
    spec.loader.exec_module(module)
    return module


def load_token() -> str:
    tok = ""
    try:
        with open(TOKEN_FILE, encoding="utf-8") as fh:
            for line in fh:
                if line.startswith("VELOX_ADMIN_TOKEN="):
                    tok = line.split("=", 1)[1].strip()
                    break
    except OSError as exc:
        print(f"token file unreadable: {exc}", file=sys.stderr)
    if not tok:
        tok = os.environ.get("VELOX_ADMIN_TOKEN", "")
    if not re.fullmatch(r"[a-fA-F0-9]{64}", tok or ""):
        raise SystemExit("VELOX_ADMIN_TOKEN missing or invalid (not 64-hex)")
    return tok


def main() -> int:
    runner = load_runner()
    token = load_token()

    # Resolve the canonical Boxe Drive root through the alias resolver.
    boxe = runner.resolve_boxe_folder(BASE, token)
    print(f"Boxe root folder: {boxe}", file=sys.stderr)

    run_id = datetime.now(timezone.utc).strftime("%Y%m%dT%H%M%S%fZ")
    os.environ["VELOX_STOCK_RUN_ID"] = run_id
    results: dict[str, str] = {}

    for boxer_key, boxer_name, folder_name, role in SLOTS:
        manifest = runner.manifest_for_boxer(boxer_name)
        if manifest is None:
            raise SystemExit(f"no manifest for {boxer_name}")
        videos = dict(manifest).get(role)
        if not videos:
            raise SystemExit(f"{boxer_name} manifest has no {role} videos")
        video_id = videos[0]
        url = f"https://www.youtube.com/watch?v={video_id}"
        # Fail fast before submitting: the manifest video must be reachable and
        # long enough for a 4s clip. Mirrors select()/describe() in the runner.
        try:
            described = runner.describe(video_id, role)
        except RuntimeError as exc:
            raise SystemExit(f"{boxer_name} {role} video {video_id} unusable: {exc}") from exc
        print(f"probe {boxer_key}.{role} video={video_id} dur={described.get('duration'):.0f}s", file=sys.stderr)
        slug = runner.boxer_slug(boxer_name)
        index = {"interview": 1, "training": 2}[role]
        payload: dict[str, Any] = {
            "direct_urls": [url],
            "clips": [{
                "title": boxer_name,
                "description": f"{role} scene featuring {boxer_name}.",
                "url": url,
                "start_sec": 8,
                "end_sec": 12,
                "category": role,
                "tags": [boxer_name, role],
                "slug": f"{run_id}-{index:02d}-{video_id}-01",
            }],
            "total_minutes": 1,
            "target_total_duration_seconds": 4,
            "target_duration_per_source_seconds": 4,
            "clips_per_source": 1,
            "clip_duration_seconds": 4,
            "download_mode": "sections_only",
            "clip_duration": 4,
            "folder_name": folder_name,
            "drive_folder_id": boxe,
            "subfolder": f"{run_id}/{index:02d}/{role}/{video_id}",
            "metadata": {
                "title": boxer_name,
                "description": f"{role} stock for {boxer_name}.",
                "category": "Boxe",
                "tags": [boxer_name, role],
            },
            "async": True,
        }
        request_id = f"youtube-stock-canary-{slug}-{role}-{video_id}-{run_id}"
        accepted = runner.http(BASE, token, "POST", "/api/stock-pipeline/run", payload, request_id)
        job_id = str(accepted.get("job_id") or accepted.get("run_id") or "")
        if not job_id:
            raise SystemExit(f"{boxer_key}.{role}: stock pipeline returned no job_id: {accepted}")
        results[f"{boxer_key}.{role}"] = job_id
        print(f"submitted {boxer_key}.{role} video={video_id} job={job_id}", file=sys.stderr)

    print(json.dumps({"run_id": run_id, "jobs": results}, indent=2))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
