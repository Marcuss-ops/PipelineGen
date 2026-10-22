#!/usr/bin/env python3
"""Build the r13 payloads: intro clips referenced from the Drive stock folder.

The protected intro points at three clips that live in the Garlasco Drive stock
folder (Garlasco_TG_Interviste_Stock_20x60s_5s), registered in the media catalog
as drive-only assets: no local file, the canonical materializer downloads them
from Drive when the run needs them.

The source window is capped at the certified clip duration (4979 ms): the first
r10 attempt asked for 5000 ms and the fixed-playback validation rejected the
window as longer than the clip.

Only identity fields change, so each attempt is a new run:
  correlation_id / items[0].id                -> -r13 suffix
  items[0].output.render.drive_subfolder_name -> -r13 suffix
  items[0].intro.clip_ids                     -> Drive file ids
  items[0].media_plan.intro.clips             -> same ids, 4979 ms each
"""

import json
from pathlib import Path

HERE = Path(__file__).resolve().parent
SRC = HERE.parent / "resubmit_r9"

SUFFIX = "r13"
OLD_SUFFIXES = ("-r9", "-r10", "-r11", "-r12")

DRIVE_CLIPS = [
    ("1Rc8lxEQjP8-CifHA6Mk9XeEVwgEzF7BX", "clip_001.mp4"),
    ("1cXux36S6uE_nm_noJ4xt0-mtzRGYE0fW", "clip_011.mp4"),
    ("1IQwJ7aaMb0QGpVKqg6Z5jgwpSHuQ1QzU", "clip_021.mp4"),
]
CLIP_IDS = [c[0] for c in DRIVE_CLIPS]
# Certified duration of every clip in the folder (videoMediaMetadata).
CLIP_MS = 4979

JOBS = [
    "01_cronologia",
    "02_procedimento",
    "03_stasi_sentenza",
    "04_nuova_indagine",
]


def bump(text: str) -> str:
    for old in OLD_SUFFIXES:
        if text.endswith(old):
            return text[: -len(old)] + f"-{SUFFIX}"
    return text


for name in JOBS:
    src_path = SRC / f"{name}.r9.json"
    payload = json.loads(src_path.read_text(encoding="utf-8"))
    payload["correlation_id"] = bump(payload.get("correlation_id", ""))
    for item in payload.get("items") or []:
        item["id"] = bump(item.get("id", ""))
        intro = item.get("intro") or {}
        intro["clip_ids"] = list(CLIP_IDS)
        intro["playback"] = {
            "audio_mode": "original_clip",
            "source_in_ms": 0,
            "source_out_ms": CLIP_MS,
        }
        item["intro"] = intro
        plan = (item.get("media_plan") or {}).get("intro")
        if plan is not None:
            plan["mode"] = "manual"
            plan["clips"] = [{"asset_id": cid, "duration_ms": CLIP_MS} for cid in CLIP_IDS]
            plan["target_duration_ms"] = CLIP_MS * len(CLIP_IDS)
        render = (item.get("output") or {}).get("render") or {}
        if "drive_subfolder_name" in render:
            render["drive_subfolder_name"] = bump(render["drive_subfolder_name"])
    out = HERE / f"{name}.{SUFFIX}.json"
    out.write_text(json.dumps(payload, ensure_ascii=False, indent=1) + "\n", encoding="utf-8")
    print(out.name, payload["correlation_id"])
