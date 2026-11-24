#!/usr/bin/env python3
"""Build canonical POST /api/clips/process payloads for the Garlasco interviews.

Input: data/<nn>_<video_id>.json, one file per source video, with the manually
curated main clips and hook clips (topic + dense vector description + summary).

Output: payloads/<nn>_<video_id>.process.json (dto.ExtractRequest) and a
manifest.json with the counts and destination.

The destination is the existing Garlasco Drive stock folder
(Garlasco_TG_Interviste_Stock_20x60s_5s). A dated child folder keeps this
batch separate from the 572 files already in the stock pool.
"""

from __future__ import annotations

import hashlib
import json
from datetime import datetime, timezone
from pathlib import Path

HERE = Path(__file__).resolve().parent
DATA = HERE / "data"
PAYLOADS = HERE / "payloads"

# Garlasco_TG_Interviste_Stock_20x60s_5s (Saggezza/Garlasco)
DRIVE_PARENT = "1c-pjyGbD56VzwycK8N0yqvycjKpnPrPM"
DRIVE_SUBFOLDER = "Interviste_20261124"

CATEGORY = "garlasco_interview"
PROGRAM_TAG = "intervista"

# Canonical people mentioned in the Garlasco interviews. The full name is used
# only when it appears in the clip text; this keeps the search index honest.
PEOPLE = [
    "Andrea Sempio",
    "Chiara Poggi",
    "Alberto Stasi",
    "Marco Poggi",
    "Giuseppe Sempio",
    "Massimo Lovati",
    "Angela Taccia",
    "Mario Venditti",
    "Stefano Vitelli",
    "Carmelo Schininà",
    "Leonardo Mendolicchio",
    "Gianluca Zanella",
    "Andrea Tosatto",
    "Barbara Fabroni",
    "Marcello Piras",
]

SPEAKER_HINTS = [
    ("Sempio", "Andrea Sempio"),
    ("Lovati", "Massimo Lovati"),
    ("Vitelli", "Stefano Vitelli"),
    ("Schininà", "Carmelo Schininà"),
    ("Mendolicchio", "Leonardo Mendolicchio"),
    ("Taccia", "Angela Taccia"),
    ("Piras", "Marcello Piras"),
    ("Zanella", "Gianluca Zanella"),
    ("Tosatto", "Andrea Tosatto"),
    ("Fabroni", "Barbara Fabroni"),
]


def parse_ts(value: str) -> tuple[int, int, int]:
    parts = [int(p) for p in str(value).strip().split(":")]
    if len(parts) == 2:
        return 0, parts[0], parts[1]
    if len(parts) == 3:
        return parts[0], parts[1], parts[2]
    raise ValueError(f"timestamp non valido: {value!r}")


def canonical_ts(value: str) -> str:
    h, m, s = parse_ts(value)
    return f"{h:02d}:{m:02d}:{s:02d}"


def seconds(value: str) -> int:
    h, m, s = parse_ts(value)
    return h * 3600 + m * 60 + s


def dedupe(items: list[str]) -> list[str]:
    out: list[str] = []
    seen: set[str] = set()
    for item in items:
        key = item.strip().lower()
        if item.strip() and key not in seen:
            seen.add(key)
            out.append(item.strip())
    return out


def build_segment(video: dict, clip: dict, *, hook: bool) -> dict:
    start = canonical_ts(clip["start"])
    end = canonical_ts(clip["end"])
    topic = str(clip["topic"]).strip()
    vector = str(clip["vector"]).strip()
    summary = str(clip["summary"]).strip()
    combined = " ".join([video["title"], topic, vector, summary])

    tags = ["Garlasco", "Chiara Poggi", "Andrea Sempio", video["program"], PROGRAM_TAG]
    if hook:
        tags.append("hook")
    tags = dedupe(tags)

    mentioned = [name for name in PEOPLE if name.lower() in combined.lower()]
    speakers = [name for hint, name in SPEAKER_HINTS if hint.lower() in combined.lower()]
    if not speakers:
        speakers = [video["program"]]

    return {
        "start": start,
        "end": end,
        "name": topic,
        "category": CATEGORY,
        "source_title": video["title"],
        "source_channel": video["program"],
        "tags": tags,
        "summary": summary,
        "topics": [vector, topic],
        "speakers": dedupe(speakers),
        "mentioned_people": dedupe(mentioned),
        "hook": summary if hook else "",
        "quality_score": 0.9 if hook else 1.0,
        "search_visibility": "high",
        "texts": [
            {
                "language_code": "it",
                "title": topic,
                "summary": summary,
                "description": vector,
            }
        ],
    }


def validate(video: dict, segments: list[dict]) -> list[str]:
    errs: list[str] = []
    if len(video["clips"]) < 1:
        errs.append(f"{video['video_id']}: nessuna clip principale")
    if len(video["hook_clips"]) < 1:
        errs.append(f"{video['video_id']}: nessun hook")
    seen_windows: dict[tuple[int, int], str] = {}
    seen_names: dict[str, str] = {}
    for segment in segments:
        start, end = seconds(segment["start"]), seconds(segment["end"])
        duration = end - start
        is_hook = "hook" in segment["tags"]
        if duration < 4:
            errs.append(f"{video['video_id']}/{segment['name']}: durata {duration}s < 4s")
        if is_hook and duration > 60:
            errs.append(f"{video['video_id']}/{segment['name']}: hook {duration}s > 60s")
        if not is_hook and duration > 600:
            errs.append(f"{video['video_id']}/{segment['name']}: clip {duration}s > 600s")
        key = (start, end)
        if key in seen_windows:
            errs.append(
                f"{video['video_id']}: finestra duplicata {segment['start']}-{segment['end']} "
                f"({seen_windows[key]} / {segment['name']})"
            )
        seen_windows[key] = segment["name"]
        name_key = segment["name"].strip().lower()
        if name_key in seen_names:
            errs.append(
                f"{video['video_id']}: nome duplicato {segment['name']!r} "
                f"({seen_names[name_key]} / {segment['name']})"
            )
        seen_names[name_key] = segment["name"]
        if len(segment["summary"]) < 40:
            errs.append(f"{video['video_id']}/{segment['name']}: summary troppo corta")
        # Gli hook sono headline brevi per design: la soglia di densità è
        # più bassa, come nel validator del batch HipHop (100 vs 120).
        if len(" ".join(segment["topics"])) < (55 if is_hook else 80):
            errs.append(f"{video['video_id']}/{segment['name']}: topics troppo poveri")
    return errs


def main() -> int:
    files = sorted(DATA.glob("*.json"))
    if not files:
        raise SystemExit(f"nessun file in {DATA}")

    PAYLOADS.mkdir(exist_ok=True)
    seen_videos: set[str] = set()
    errors: list[str] = []
    entries: list[dict] = []
    total_main = total_hooks = 0

    for path in files:
        video = json.loads(path.read_text(encoding="utf-8"))
        video_id = str(video["video_id"]).strip()
        if video_id in seen_videos:
            errors.append(f"{path.name}: video duplicato {video_id}")
            continue
        seen_videos.add(video_id)

        main_segments = [build_segment(video, clip, hook=False) for clip in video["clips"]]
        hook_segments = [build_segment(video, clip, hook=True) for clip in video["hook_clips"]]
        segments = main_segments + hook_segments
        errors.extend(validate(video, segments))

        payload = {
            "url": video["url"],
            "segments": segments,
            "force_keyframes": True,
            "keep_audio": True,
            "write_summary": True,
            "strategy": "verify",
            "concurrency": 2,
            "destination": {
                "folder_id": DRIVE_PARENT,
                "subfolder_name": DRIVE_SUBFOLDER,
                "create_subfolder": True,
            },
        }
        out = PAYLOADS / f"{path.stem}.process.json"
        out.write_text(json.dumps(payload, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")
        digest = hashlib.sha256(out.read_bytes()).hexdigest()
        total_main += len(main_segments)
        total_hooks += len(hook_segments)
        entries.append(
            {
                "video_id": video_id,
                "title": video["title"],
                "program": video["program"],
                "source_file": path.name,
                "payload": str(out.relative_to(HERE)),
                "sha256": digest,
                "main_clips": len(main_segments),
                "hook_clips": len(hook_segments),
                "segments": len(segments),
            }
        )

    manifest = {
        "batch": "garlasco_interviste_20261124",
        "generated_at": datetime.now(timezone.utc).isoformat(),
        "destination": {
            "folder_id": DRIVE_PARENT,
            "folder_name": "Garlasco_TG_Interviste_Stock_20x60s_5s",
            "subfolder_name": DRIVE_SUBFOLDER,
            "create_subfolder": True,
        },
        "totals": {
            "videos": len(entries),
            "main_clips": total_main,
            "hook_clips": total_hooks,
            "segments": total_main + total_hooks,
        },
        "videos": entries,
    }
    (HERE / "manifest.json").write_text(
        json.dumps(manifest, ensure_ascii=False, indent=2) + "\n", encoding="utf-8"
    )

    for entry in entries:
        print(
            f"{entry['video_id']:<12} main={entry['main_clips']:<2} "
            f"hooks={entry['hook_clips']:<2} payload={entry['payload']}"
        )
    print(
        f"\nvideos={len(entries)} main={total_main} hooks={total_hooks} "
        f"segments={total_main + total_hooks} errors={len(errors)}"
    )
    for err in errors:
        print(f"  ! {err}")
    return 1 if errors else 0


if __name__ == "__main__":
    raise SystemExit(main())
