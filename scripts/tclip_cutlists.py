#!/usr/bin/env python3
"""Build the retrieval-ready cutlist JSON for every interview.

Reads the operator segment files (data/tclip/persons/<slug>/NN-<vid>.json) and
emits, per video:

    {
      "video_url": "https://www.youtube.com/watch?v=<vid>",
      "title": "<source title>",
      "clips":  [ <=60s editorial moments  ],
      "hooks":  [ <=15s opening bumpers     ]
    }

every entry carrying clip_id / start / end (MM:SS, HH:MM:SS past the hour) /
topic / vector_description / summary. Nothing is invented: topic, summary and
vector_description come from the operator-authored name/summary/description.
"""
from __future__ import annotations

import argparse
import json
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent


def _sec(ts: str) -> int:
    parts = [int(p) for p in ts.strip().split(":")]
    while len(parts) < 3:
        parts.insert(0, 0)
    return parts[0] * 3600 + parts[1] * 60 + parts[2]


def _fmt(ts: str) -> str:
    parts = [int(p) for p in ts.strip().split(":")]
    while len(parts) < 3:
        parts.insert(0, 0)
    h, m, s = parts
    return f"{h:02d}:{m:02d}:{s:02d}" if h else f"{m:02d}:{s:02d}"


def _entry(seg: dict, cid: int) -> dict:
    topics = seg.get("topics") or []
    topic = topics[0] if topics else seg.get("name", "")
    if topic.lower().startswith("hook: "):
        topic = topic[6:]
    vecs = [seg.get("description") or ""]
    vecs += [t for t in seg.get("tags") or []]
    vecs += [p for p in seg.get("mentioned_people") or []]
    vector = " ".join(dict.fromkeys(v.strip() for v in vecs if v and v.strip()))
    return {
        "clip_id": cid,
        "start": _fmt(seg["start"]),
        "end": _fmt(seg["end"]),
        "topic": topic,
        "vector_description": vector,
        "summary": seg.get("summary", ""),
    }


def build(video_id: str, segments_file: Path, meta: dict) -> dict:
    segs = json.loads(segments_file.read_text(encoding="utf-8"))
    moments = sorted((s for s in segs if (s.get("kind") or "moment") != "hook"),
                     key=lambda s: _sec(s["start"]))
    hooks = sorted((s for s in segs if (s.get("kind") or "") == "hook"),
                   key=lambda s: _sec(s["start"]))
    info = meta.get(video_id, {})
    doc = {
        "video_url": f"https://www.youtube.com/watch?v={video_id}",
        "title": info.get("title") or video_id,
        "clips": [_entry(s, i + 1) for i, s in enumerate(moments)],
        "hooks": [_entry(s, i + 1) for i, s in enumerate(hooks)],
    }
    # Third tier: the 10-minute (600s) blocks, when the operator wrote them.
    long_file = segments_file.with_name(segments_file.stem + ".long.json")
    if long_file.is_file():
        longs = sorted(json.loads(long_file.read_text(encoding="utf-8")),
                       key=lambda s: _sec(s["start"]))
        doc["long_clips"] = [_entry(s, i + 1) for i, s in enumerate(longs)]
    return doc


def main() -> int:
    ap = argparse.ArgumentParser()
    ap.add_argument("--data-dir", default="data/tclip/persons/vannacci")
    ap.add_argument("--out", default="data/tclip/persons/vannacci/cutlists")
    ap.add_argument("--combined", default="data/tclip/persons/vannacci/cutlists_vannacci.json")
    a = ap.parse_args()

    data = ROOT / a.data_dir
    out_dir = ROOT / a.out
    out_dir.mkdir(parents=True, exist_ok=True)
    meta = json.loads((data / "_meta.json").read_text(encoding="utf-8"))

    combined = []
    for seg_file in sorted(data.glob("[0-9][0-9]-*.json")):
        if seg_file.stem.endswith(".long"):
            continue  # third tier: folded into its video's long_clips
        vid = seg_file.stem.split("-", 1)[1]
        doc = build(vid, seg_file, meta)
        (out_dir / f"{seg_file.stem}.json").write_text(
            json.dumps(doc, ensure_ascii=False, indent=2), encoding="utf-8")
        combined.append(doc)
        print(f"{vid}: clips={len(doc['clips'])} hooks={len(doc['hooks'])}")
    (ROOT / a.combined).write_text(
        json.dumps(combined, ensure_ascii=False, indent=2), encoding="utf-8")
    print(f"combined -> {a.combined} ({len(combined)} videos)")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
