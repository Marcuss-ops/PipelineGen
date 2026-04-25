#!/usr/bin/env python3
from __future__ import annotations

import json
from pathlib import Path


def main() -> int:
    repo_root = Path(__file__).resolve().parents[1]
    source_candidates = [
        repo_root / "src/go-master/data/artlist_local.db.json.bak_floyd_reset_20260419_135447",
        repo_root / "src/go-master/data/artlist_local.db.json",
        repo_root / "src/node-scraper/artlist_video_links.txt",
    ]
    output_path = repo_root / "data/artlist_stock_index.json"

    source_path = next((path for path in source_candidates if path.exists()), None)
    if source_path is None:
        raise SystemExit("no Artlist source file found")

    folder_id = ""
    if output_path.exists():
        try:
            current = json.loads(output_path.read_text(encoding="utf-8"))
            folder_id = str(current.get("folder_id", "")).strip()
        except Exception:
            folder_id = ""

    clips: list[dict[str, object]] = []
    seen: set[str] = set()

    if source_path.suffix == ".txt":
        for raw_line in source_path.read_text(encoding="utf-8").splitlines():
            url = raw_line.strip()
            if not url or url in seen:
                continue
            seen.add(url)
            clips.append({
                "clip_id": url.rsplit("/", 1)[-1],
                "filename": url.rsplit("/", 1)[-1],
                "title": url.rsplit("/", 1)[-1],
                "url": url,
                "source": "artlist",
                "tags": [],
            })
    else:
        payload = json.loads(source_path.read_text(encoding="utf-8"))
        for search in payload.get("searches", {}).values():
            for clip in search.get("clips", []):
                link = str(clip.get("drive_url") or clip.get("url") or clip.get("original_url") or "").strip()
                clip_id = str(clip.get("drive_file_id") or clip.get("video_id") or clip.get("id") or "").strip()
                if not link and not clip_id:
                    continue
                dedupe_key = link or clip_id
                if dedupe_key in seen:
                    continue
                seen.add(dedupe_key)
                tags = clip.get("tags") or []
                if not isinstance(tags, list):
                    tags = []
                clips.append({
                    "clip_id": clip_id,
                    "folder_id": str(clip.get("folder_id") or "").strip(),
                    "filename": str(clip.get("name") or clip.get("title") or clip.get("filename") or clip_id).strip(),
                    "title": str(clip.get("title") or clip.get("name") or clip.get("filename") or clip_id).strip(),
                    "name": str(clip.get("name") or clip.get("title") or clip.get("filename") or clip_id).strip(),
                    "url": str(clip.get("url") or "").strip(),
                    "drive_url": str(clip.get("drive_url") or "").strip(),
                    "folder": str(clip.get("folder") or "").strip(),
                    "category": str(clip.get("category") or "").strip(),
                    "source": str(clip.get("source") or "artlist").strip() or "artlist",
                    "tags": [str(tag).strip() for tag in tags if str(tag).strip()],
                    "duration": int(clip.get("duration") or 0),
                    "downloaded": bool(clip.get("downloaded")),
                })

    clips.sort(key=lambda item: (
        str(item.get("folder", "")),
        str(item.get("category", "")),
        str(item.get("title", "")),
    ))

    output = {
        "folder_id": folder_id,
        "clips": clips,
    }
    output_path.write_text(json.dumps(output, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")
    print(f"Wrote {len(clips)} Artlist entries to {output_path}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
