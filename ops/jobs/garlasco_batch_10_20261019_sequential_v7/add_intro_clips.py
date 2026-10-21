#!/usr/bin/env python3
"""Add the canonical protected intro section to the four Garlasco payloads.

The existing `media_plan.intro` block is a visual-slot plan; the renderer only
turns `items[].intro` into a protected fixed-media scene. This script lifts the
three already-selected clip IDs into the canonical intro contract and writes a
new payload so the original evidence files stay untouched.
"""

import json
from pathlib import Path

BASE = Path(__file__).resolve().parent
JOBS = [
    (
        "01_cronologia.generate.v8.json",
        "01_cronologia.generate.v8.introfix.json",
        "Garlasco 01 — Cronologia: introduzione",
    ),
    (
        "02_procedimento.generate.v15.json",
        "02_procedimento.generate.v15.introfix.json",
        "Garlasco 02 — Indagine, processo e condanna: introduzione",
    ),
    (
        "03_stasi_sentenza.generate.v9.json",
        "03_stasi_sentenza.generate.v9.introfix.json",
        "Garlasco 03 — La vicenda Stasi: introduzione",
    ),
    (
        "04_nuova_indagine.generate.v11.json",
        "04_nuova_indagine.generate.v11.introfix.json",
        "Garlasco 04 — La nuova indagine: introduzione",
    ),
]

for src_name, dst_name, title in JOBS:
    src = BASE / src_name
    payload = json.loads(src.read_text(encoding="utf-8"))
    items = payload.get("items") or []
    if len(items) != 1:
        raise SystemExit(f"{src_name}: expected exactly one item, got {len(items)}")
    item = items[0]

    media_intro = (item.get("media_plan") or {}).get("intro") or {}
    clips = media_intro.get("clips") or []
    clip_ids = []
    for clip in clips:
        asset_id = str(clip.get("asset_id") or "").strip()
        if asset_id:
            clip_ids.append(asset_id)
    if len(clip_ids) != 3:
        raise SystemExit(f"{src_name}: expected 3 intro clips, got {clip_ids}")
    if len(set(clip_ids)) != 3:
        raise SystemExit(f"{src_name}: duplicate intro clip ids: {clip_ids}")

    suffix = "-introfix-20261020"
    item["id"] = f"{item.get('id', '')}{suffix}"
    payload["correlation_id"] = f"{payload.get('correlation_id', '')}{suffix}"

    item["intro"] = {
        "clip_ids": clip_ids,
        "title": title,
        "playback": {
            "audio_mode": "original_clip",
            "source_in_ms": 0,
            "source_out_ms": 5000,
        },
    }

    output = item.setdefault("output", {})
    for key in ("drive_subfolder_name",):
        if output.get(key):
            output[key] = f"{output[key]}-introfix"
    render = output.get("render") or {}
    if render.get("drive_subfolder_name"):
        render["drive_subfolder_name"] = f"{render['drive_subfolder_name']}-introfix"

    dst = BASE / dst_name
    dst.write_text(json.dumps(payload, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")
    print(f"{dst.name}: {', '.join(clip_ids)}")
