#!/usr/bin/env python3
"""HipHop 36 — spec -> process.json (deterministic, no LLM).

A payload is 5-8 main windows plus the same number of <=15s hook openers
carved inside them. Writing that by hand duplicates ~40 lines of
boilerplate per clip, which is how hand-written payloads drift. The
CONTENT is still hand-picked from the timed transcript (tx.py); this
script only expands a compact spec into the wire shape.

specs/<slug>.json:
  {
    "video": "a6port1qeh4",
    "source_title": "...", "source_channel": "...", "source_year": "1986",
    "speakers": ["Diana Ross", "Terry Wogan"],       # default speakers
    "about": ["Diana Ross"],                        # default mentioned_people
    "article": "il",                                # summary language
    "mains": [
      {"start": "00:03:51", "end": "00:04:45",
       "name": "…", "summary": "…", "topics": "…" (one dense string),
       "hook": "…" (the on-air sentence, also the search_text hook),
       "hook_window": ["00:03:55", "00:04:09"],   # optional, inside start..end
       "people": ["…"], "speakers": ["…"]}        # per-segment overrides
    ]
  }

`topics` is kept as one long keyword-dense string because that is what
composeYouTubeClipSearchText feeds into search_text -> 768d embedding.
`texts[0]` (language_code/title/summary/description) becomes an
asset_text_tracks row in the same atomic commit (MaterializePayloadTexts).

Usage:
  mkpayload.py diana_ross cher …      # writes <slug>/process.json
  mkpayload.py --all
"""

from __future__ import annotations

import argparse
import json
import sys
from pathlib import Path

HERE = Path(__file__).resolve().parent
SPECS = HERE / "specs"
CATEGORY = "celebrity_interview"


def ts_to_sec(value: str) -> int:
    parts = [int(p) for p in str(value).split(":")]
    while len(parts) < 3:
        parts.insert(0, 0)
    return parts[0] * 3600 + parts[1] * 60 + parts[2]


def segment(spec: dict, main: dict, kind: str) -> dict:
    start, end = main["start"], main["end"]
    name = main["name"]
    summary, hook = main["summary"], main["hook"]
    topics = main.get("topics", "")
    if kind == "hook":
        hw = main["hook_window"]
        start, end = hw[0], hw[1]
        name = f"Hook: {main.get('hook_name', name.lower())}"
        summary = (
            f"Apertura secca ({ts_to_sec(end) - ts_to_sec(start)}s): "
            f"{main.get('hook_summary', name.lower())}"
        )
        # The hook sentence itself plus the topics string: together the
        # search_text dense enough for a useful embedding on a short clip.
        topics = f"{topics} hook apertura short opening-short"
    seg = {
        "start": start,
        "end": end,
        "name": name,
        "category": CATEGORY,
        "source_title": spec["source_title"],
        "source_channel": spec.get("source_channel", ""),
        "summary": summary,
        "topics": [topics],
        "tags": [
            spec["artist"],
            *(spec.get("source_tag", [])),
            CATEGORY,
            *(spec.get("tags", [])),
            *(["hook"] if kind == "hook" else []),
        ],
        "speakers": main.get("speakers") or spec.get("speakers") or [spec["artist"]],
        "mentioned_people": main.get("people") or spec.get("about") or [spec["artist"]],
        "hook": hook,
        "texts": [
            {
                "language_code": spec.get("article", "it"),
                "title": main.get("text_title") or name,
                "summary": main.get("text_summary") or summary[:180],
                "description": main.get("text_description")
                or f"{summary} Contesto: {topics}",
            }
        ],
    }
    return seg


def build(slug: str) -> dict:
    spec = json.loads((SPECS / f"{slug}.json").read_text())
    spec.setdefault("artist", slug.replace("_", " ").title())
    slides: list[dict] = []
    for main in spec["mains"]:
        slides.append(segment(spec, main, "main"))
        if main.get("hook_window"):
            slides.append(segment(spec, main, "hook"))
    return {
        "_comment": spec.get("_comment")
        or (
            f"POST /api/clips/process payload (ExtractRequest, drift source: "
            f"internal/capabilities/youtube/dto/types.go). Source: {spec['source_title']} "
            f"(https://www.youtube.com/watch?v={spec['video']}). Destination: the "
            f"Clips/HipHop child folder for {spec['artist']} (see check.py FOLDERS), "
            f"create_subfolder=false. Windows picked BY HAND from the timed transcript "
            f"printed by ops/jobs/hiphop_36/tx.py; no LLM selection. Clip ids are "
            f"deterministic (yt_<videoID>_<startSec>_<endSec>_v1) so a re-submit is an "
            f"UPSERT. topics/summary/hook are keyword-dense on purpose: "
            f"composeYouTubeClipSearchText feeds search_text -> 768d pgvector embedding, "
            f"texts[] lands in asset_text_tracks in the same atomic commit."
        ),
        "url": f"https://www.youtube.com/watch?v={spec['video']}",
        "strategy": "verify",
        "concurrency": 3,
        "force_keyframes": True,
        "keep_audio": True,
        "write_summary": True,
        "destination": {
            "folder_id": spec["folder_id"],
            "create_subfolder": False,
        },
        "segments": slides,
    }


def main() -> int:
    ap = argparse.ArgumentParser()
    ap.add_argument("slugs", nargs="*")
    args = ap.parse_args()
    slugs = args.slugs or sorted(p.stem for p in SPECS.glob("*.json"))
    for slug in slugs:
        path = SPECS / f"{slug}.json"
        if not path.exists():
            print(f"no spec: {path}")
            continue
        doc = build(slug)
        out = HERE / slug / "process.json"
        out.parent.mkdir(parents=True, exist_ok=True)
        out.write_text(json.dumps(doc, ensure_ascii=False, indent=2) + "\n")
        print(f"wrote {out} ({len(doc['segments'])} segments)")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
