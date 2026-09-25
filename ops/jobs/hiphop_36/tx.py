#!/usr/bin/env python3
"""HipHop 36 — transcript helper (NO LLM).

Thin deterministic wrapper around three live endpoints:

  GET /api/clips/search      -> candidate videos (ranked by the server)
  GET /api/clips/info        -> duration / channel / title for one video
  GET /api/clips/transcript  -> per-cue timings for one video

The transcript endpoint returns YouTube auto-caption *rolling* cues
(cue N+1 repeats cue N's text and appends more words). `dedupe` folds
those runs into one non-overlapping line per sentence so the printed
`[MM:SS] text` list is readable and directly usable to pick clip
boundaries by hand.

Usage:
  tx.py search "<query>" [--limit 12]
  tx.py info <video_id>
  tx.py tx <video_id> [--from 00:00] [--to 12:00]
  tx.py tx-url "<youtube url>" ...

Disk cache: cache/transcripts/<video_id>.json (so a re-read is free).
"""

from __future__ import annotations

import argparse
import json
import os
import re
import sys
import urllib.error
import urllib.parse
import urllib.request
from pathlib import Path

BASE = os.environ.get("VELOX_BASE", "http://127.0.0.1:8000")
REPO = Path(__file__).resolve().parents[3]
ENV = REPO / ".env"
CACHE = Path(__file__).resolve().parent / "cache" / "transcripts"


def token() -> str:
    tok = os.environ.get("VELOX_ADMIN_TOKEN")
    if tok:
        return tok.strip()
    if ENV.exists():
        for line in ENV.read_text().splitlines():
            if line.startswith("VELOX_ADMIN_TOKEN="):
                return line.split("=", 1)[1].strip()
    sys.exit("VELOX_ADMIN_TOKEN not found (env or refactored/.env)")


def get(path: str, params: dict, timeout: int = 180) -> dict:
    url = f"{BASE}{path}?{urllib.parse.urlencode(params)}"
    req = urllib.request.Request(url, headers={"Authorization": f"Bearer {token()}"})
    with urllib.request.urlopen(req, timeout=timeout) as resp:
        return json.loads(resp.read().decode())


def video_id_of(value: str) -> str:
    m = re.search(r"(?:v=|youtu\.be/|/shorts/|/embed/)([A-Za-z0-9_-]{6,})", value)
    return m.group(1) if m else value


def mmss(sec: float) -> str:
    sec = int(sec)
    return f"{sec // 3600:02d}:{(sec % 3600) // 60:02d}:{sec % 60:02d}"


def parse_ts(text: str) -> int:
    parts = [int(p) for p in re.split(r"[:.]", str(text).strip()) if p.strip() != ""]
    while len(parts) < 3:
        parts.insert(0, 0)
    return parts[0] * 3600 + parts[1] * 60 + parts[2]


def info(video_id: str) -> dict:
    data = get("/api/clips/info", {"url": f"https://www.youtube.com/watch?v={video_id}"})
    return data


def duration_of(video_id: str) -> int:
    data = info(video_id)
    for key in ("duration", "duration_seconds", "length_seconds", "length"):
        v = data.get(key)
        if isinstance(v, (int, float)) and v:
            return int(v)
    for key in ("video", "metadata", "data"):
        sub = data.get(key)
        if isinstance(sub, dict):
            for k2 in ("duration", "duration_seconds", "length_seconds"):
                v = sub.get(k2)
                if isinstance(v, (int, float)) and v:
                    return int(v)
    raise SystemExit(f"cannot find duration in /api/clips/info reply: {list(data)[:20]}")


def raw_cues(video_id: str, start: int = 0, end: int = 0, refresh: bool = False) -> dict:
    CACHE.mkdir(parents=True, exist_ok=True)
    path = CACHE / f"{video_id}_{start}_{end}.json"
    if path.exists() and not refresh:
        return json.loads(path.read_text())
    data = get(
        "/api/clips/transcript",
        {"url": f"https://www.youtube.com/watch?v={video_id}", "start": start, "end": end},
        timeout=300,
    )
    path.write_text(json.dumps(data, ensure_ascii=False))
    return data


def dedupe(cues: list[dict]) -> list[dict]:
    """Fold YouTube rolling captions into one line per sentence.

    A rolling caption run looks like: (t0,t1,"a b") (t1,t2,"a b c d") ...
    Every cue after the first starts with the previous cue's full text.
    """
    out: list[dict] = []
    for cue in cues:
        text = re.sub(r"\s+", " ", str(cue.get("text", ""))).strip()
        if not text or text in {"[Music]", "[Applause]"}:
            continue
        start = int(cue.get("start_ms", 0))
        end = int(cue.get("end_ms", start))
        if out:
            prev = out[-1]
            if text == prev["text"]:
                prev["end"] = max(prev["end"], end)
                continue
            if text.startswith(prev["text"]):
                prev["text"] = text
                prev["end"] = end
                continue
            if prev["text"].startswith(text):
                continue
        out.append({"start": start, "end": end, "text": text})
    return out


def cmd_search(args: argparse.Namespace) -> None:
    data = get("/api/clips/search", {"q": args.query, "limit": args.limit}, timeout=180)
    print(f"# query={args.query!r} count={data.get('count')}")
    for r in data.get("results", []):
        langs = r.get("caption_languages") or []
        print(
            f"{r.get('video_id')}  dur={r.get('duration'):>5}s  "
            f"caps={str(bool(r.get('has_captions'))):>5}  "
            f"langs={','.join(langs[:4]) or '-':<12}  "
            f"views={r.get('view_count', 0):>10}  "
            f"sim={r.get('similarity_score')}\n"
            f"    {r.get('title')}\n"
            f"    ch={r.get('channel_name')}"
        )


def cmd_info(args: argparse.Namespace) -> None:
    data = info(video_id_of(args.video))
    keys = ("ok", "video_id", "title", "channel_name", "channel", "duration",
            "duration_seconds", "upload_date", "view_count", "has_captions",
            "caption_languages", "language")
    shown = {k: data[k] for k in keys if k in data}
    if "caption_languages" in shown:
        shown["caption_languages"] = (shown["caption_languages"] or [])[:6]
    print(json.dumps(shown or data, indent=2, ensure_ascii=False))


def cmd_tx(args: argparse.Namespace) -> None:
    vid = video_id_of(args.video)
    dur = args.duration or duration_of(vid)
    start = parse_ts(args.start) if args.start else 0
    end = parse_ts(args.end) if args.end else dur
    data = raw_cues(vid, start, end, refresh=args.refresh)
    cues = dedupe(data.get("cues", []))
    inf = {}
    try:
        inf = info(vid)
    except Exception:
        pass
    title = inf.get("title") or inf.get("video_title") or "?"
    channel = inf.get("channel_name") or inf.get("channel") or "?"
    print(f"# {vid} | {title} | ch={channel} | dur={dur}s | lang={data.get('language')} "
          f"| raw_cues={len(data.get('cues', []))} | lines={len(cues)}")
    print("# window {} -> {}".format(mmss(start), mmss(end)))
    for cue in cues:
        print(f"[{mmss(cue['start'] / 1000)}] {cue['text']}")
    if args.json_out:
        Path(args.json_out).write_text(json.dumps(
            {"video_id": vid, "title": title, "channel": channel, "duration": dur,
             "language": data.get("language"), "lines": cues}, ensure_ascii=False, indent=2))


def main() -> None:
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    sub = ap.add_subparsers(dest="cmd", required=True)

    p = sub.add_parser("search")
    p.add_argument("query")
    p.add_argument("--limit", type=int, default=12)
    p.set_defaults(func=cmd_search)

    p = sub.add_parser("info")
    p.add_argument("video")
    p.set_defaults(func=cmd_info)

    p = sub.add_parser("tx")
    p.add_argument("video")
    p.add_argument("--from", dest="start", default="")
    p.add_argument("--to", dest="end", default="")
    p.add_argument("--duration", type=int, default=0)
    p.add_argument("--refresh", action="store_true")
    p.add_argument("--json-out", default="")
    p.set_defaults(func=cmd_tx)

    args = ap.parse_args()
    args.func(args)


if __name__ == "__main__":
    main()
