#!/usr/bin/env python3
"""tclip_verify — acceptance check for a person's harvested clips.

For every planned segment (short moments <=60s, hooks <=15s, long blocks ==600s)
it asks two independent sources:

  1. Drive — `yt_<videoID>_<start>_<end>_v1*` must exist in the video's
     destination subfolder (and the subfolder must be reachable from the root).
  2. the media SSOT — the clip row must be registered with a Drive file id and
     a non-empty index_revision (i.e. the vector/text index actually ran).

Exit code 0 only when both sides agree for every planned segment; otherwise it
prints exactly what is missing, unindexed or duplicated.
"""
from __future__ import annotations

import argparse
import json
import time
import urllib.parse
from pathlib import Path

import tclip_long_driver as drv  # sharing the API + Drive helpers (same package dir)

ROOT = Path(__file__).resolve().parent.parent


def _sec(ts: str) -> int:
    parts = [int(p) for p in ts.strip().split(":")]
    while len(parts) < 3:
        parts.insert(0, 0)
    return parts[0] * 3600 + parts[1] * 60 + parts[2]


def _prefix(vid: str, seg: dict) -> str:
    return f"yt_{vid}_{_sec(seg['start'])}_{_sec(seg['end'])}_v1"


def _resolve_sub(root_id: str, n: str, name: str, is_long: bool, video_id: str = "") -> tuple[str, str]:
    """Find the video's folder: by exact name, else by `Vannacci NN` + tier.

    Returns (folder_id, folder_name). The name carries the channel as resolved
    at submission time, which was not always the channel we now know
    (`Vannacci 01 - canale` vs `... - Falsissimo`), so fall back to the number +
    tier that the folder names always carry. That fallback has to be careful:
    the root also holds a stale empty folder literally named
    `... (cartella vuota da eliminare)`, and picking it made 15 delivered clips
    of video 01 read as missing — so an explicitly-marked empty folder is
    skipped, and between several number+tier matches the one that actually
    holds this video's clips wins.
    """
    import re
    folders = [f for f in drv._folder_children(root_id)
               if f.get("mime_type") == "application/vnd.google-apps.folder"]
    for f in folders:
        if f.get("name") == name:
            return f["id"], f.get("name") or ""
    pat = re.compile(rf"^Vannacci {int(n):02d}\b")
    candidates = []
    for f in folders:
        fname = f.get("name") or ""
        if not pat.match(fname):
            continue
        if "cartella vuota" in fname.lower():
            continue
        # Same tier only: the long tier lives in its own ` - lungo` folder, so a
        # number match on the short folder must not answer for a long segment.
        if is_long != fname.endswith(" - lungo"):
            continue
        candidates.append(f)
    if not candidates:
        return "", ""
    if len(candidates) == 1:
        return candidates[0]["id"], candidates[0].get("name") or ""

    def holds_clips(f: dict) -> int:
        prefix = f"yt_{video_id}_"
        return sum(1 for c in drv._folder_children(f["id"])
                   if (c.get("name") or "").startswith(prefix))

    best = max(candidates, key=holds_clips)
    return best["id"], best.get("name") or ""


def _indexed_clip_ids(vid: str) -> dict[str, dict]:
    """clip_id -> row, straight from the media SSOT search endpoint."""
    try:
        r = drv._request("/api/media/clips/youtube/clips?q=" +
                         urllib.parse.quote(vid) + "&limit=200", timeout=60)
    except Exception as exc:
        print(f"  ! index query failed for {vid}: {exc}")
        return {}
    out = {}
    for row in r.get("clips") or []:
        cid = row.get("id") or ""
        if cid.startswith(f"yt_{vid}_"):
            out[cid] = row
    return out


def _plan(data: Path, meta: dict):
    """Every planned artifact: (video_id, number, channel, title, tier, subfolder, segment).

    Tier is the destination subfolder the pipeline was asked to create, so it is
    derivable from the submission inputs alone — the same rule the driver uses.
    """
    for seg_file in sorted(data.glob("[0-9][0-9]-*.json")):
        stem = seg_file.stem
        if stem.endswith(".long"):
            continue
        n, vid = stem.split("-", 1)
        info = meta.get(vid, {})
        channel = (info.get("channel") or "canale").strip()
        tiers = [("short", f"Vannacci {n} - {channel}", json.loads(seg_file.read_text()))]
        long_file = seg_file.with_name(stem + ".long.json")
        if long_file.is_file():
            tiers.append(("long", f"Vannacci {n} - {channel} - lungo",
                          json.loads(long_file.read_text())))
        for tier, subfolder, segs in tiers:
            for seg in segs:
                yield {
                    "video_id": vid,
                    "video_number": int(n),
                    "title": info.get("title") or vid,
                    "channel": channel,
                    "tier": tier,
                    "subfolder": subfolder,
                    "name": seg.get("name") or "",
                    "start": seg["start"],
                    "end": seg["end"],
                    "artifact": _prefix(vid, seg),
                }


def main() -> int:
    ap = argparse.ArgumentParser()
    ap.add_argument("--folder-id", required=True)
    ap.add_argument("--data-dir", default="data/tclip/persons/vannacci")
    ap.add_argument("--json-out", default="data/tclip/persons/vannacci/_artifacts_index.json",
                    help="machine-readable index of every planned artifact ('' disables)")
    a = ap.parse_args()

    data = ROOT / a.data_dir
    meta = json.loads((data / "_meta.json").read_text())

    problems: list[str] = []
    artifacts: list[dict] = []
    sub_cache: dict[str, tuple[str, dict[str, dict], str]] = {}
    indexed_cache: dict[str, dict[str, dict]] = {}

    for entry in _plan(data, meta):
        vid, sub_name = entry["video_id"], entry["subfolder"]
        if sub_name not in sub_cache:
            sub_id, sub_found = _resolve_sub(a.folder_id, str(entry["video_number"]), sub_name,
                                             entry["tier"] == "long", vid)
            by_name = {f.get("name", ""): f for f in drv._folder_children(sub_id)} if sub_id else {}
            sub_cache[sub_name] = (sub_id, by_name, sub_found)
            if not sub_id:
                problems.append(f"MISSING FOLDER {sub_name} ({vid})")
        sub_id, by_name, sub_found = sub_cache[sub_name]
        entry["subfolder_found"] = sub_found
        if vid not in indexed_cache:
            indexed_cache[vid] = _indexed_clip_ids(vid)
        indexed = indexed_cache[vid]

        prefix = entry["artifact"]
        hits = [f for name, f in by_name.items() if name.startswith(prefix)]
        row = indexed.get(prefix)
        md = (row or {}).get("metadata") or {}
        entry.update({
            "subfolder_id": sub_id,
            "on_drive": bool(hits),
            "drive_file_id": hits[0].get("id", "") if hits else "",
            "drive_link": hits[0].get("web_view_link", "") if hits else "",
            "drive_file_name": hits[0].get("name", "") if hits else "",
            "indexed": row is not None,
            "index_revision": md.get("index_revision") or "",
            "index_drive_file_id": md.get("drive_file_id") or "",
            "ok": bool(hits) and len(hits) == 1 and row is not None
                  and bool(md.get("drive_file_id") or (row or {}).get("drive_link"))
                  and bool(md.get("index_revision")),
        })
        artifacts.append(entry)

        if not hits:
            problems.append(f"NOT ON DRIVE {prefix} ({sub_name})")
        elif len(hits) > 1:
            problems.append(f"DUPLICATE {prefix} x{len(hits)} ({sub_name})")
        if row is None:
            problems.append(f"NOT INDEXED {prefix} ({sub_name})")
        else:
            if not (md.get("drive_file_id") or row.get("drive_link")):
                problems.append(f"NO DRIVE LINK IN INDEX {prefix}")
            if not md.get("index_revision"):
                problems.append(f"NO index_revision {prefix}")

    total = len(artifacts)
    on_drive = sum(1 for e in artifacts if e["on_drive"])
    indexed = sum(1 for e in artifacts if e["indexed"])
    by_tier = {t: sum(1 for e in artifacts if e["tier"] == t) for t in ("short", "long")}

    if a.json_out:
        report = {
            "generated_at": time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime()),
            "folder_id": a.folder_id,
            "data_dir": a.data_dir,
            "videos": len({e["video_id"] for e in artifacts}),
            "total": total,
            "by_tier": by_tier,
            "on_drive": on_drive,
            "indexed": indexed,
            "complete": not problems,
            "problems": problems,
            "artifacts": artifacts,
        }
        out = (ROOT / a.json_out) if not Path(a.json_out).is_absolute() else Path(a.json_out)
        out.write_text(json.dumps(report, ensure_ascii=False, indent=2), encoding="utf-8")
        print(f"index -> {a.json_out}")

    print(f"checked {total} planned segments ({by_tier['short']} short, {by_tier['long']} long): "
          f"{on_drive} on Drive, {indexed} indexed")
    if problems:
        print(f"{len(problems)} problem(s):")
        for p in problems:
            print("  -", p)
        return 1
    print("OK: every planned segment is on Drive, registered and indexed")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
