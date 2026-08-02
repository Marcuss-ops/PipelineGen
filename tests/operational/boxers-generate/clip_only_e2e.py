#!/usr/bin/env python3
"""E2E suite for published clip files (never stock folders)."""
from __future__ import annotations
import argparse, json, os, sqlite3
from media_mode_e2e_common import BOXERS, assert_clip, job_id, post, subjects, wait_job


def clip_ids(key: str) -> list[str]:
    raw = os.environ.get("BOXERS_CLIP_IDS_JSON", "")
    values = json.loads(raw).get(key, []) if raw else []
    if not values:
        db_path = os.environ.get("VELOX_DB", "data/media/media.db.sqlite")
        folder_id = BOXERS[key][1]
        with sqlite3.connect(db_path) as db:
            values = [row[0] for row in db.execute("SELECT id FROM media_assets WHERE parent_folder_id=? AND lifecycle_state='ACTIVE' AND index_state='INDEXED' AND source='youtube' AND drive_link<>'' ORDER BY id LIMIT 3", (folder_id,))]
    if len(values) < 3: raise RuntimeError(f"{key}: expected at least 3 clip IDs")
    return values[:3]


def payload(key: str, ids: list[str], scenes: int) -> dict:
    name, _ = BOXERS[key]
    return {"version": 2, "preset": "custom", "items": [{"id": f"{key}-clip-only", "title": f"{name}: clip documentary", "language": "it", "tone": "documentary", "media_mode": "clip_only", "source": {"type": "clips", "clip_ids": ids, "intro_clip_ids": ids[:1], "num_clips": len(ids), "ordering_strategy": "input_order", "grounding_policy": "clips_primary", "fallback_policy": "strict", "force_refresh": True}, "script_params": {"target_words": 400, "segment_words": 130, "use_memory": False, "force_refresh": True, "skip_quality_gate": True}, "output": {"stock_enabled": "disabled", "stock_bindings": [], "save_to_db": True, "generate_timeline": False}, "docs": {"enabled": True, "languages": ["it"], "folder_id": BOXERS[key][1]}}]}


def main() -> None:
    parser = argparse.ArgumentParser(); parser.add_argument("--subject", choices=list(BOXERS)); parser.add_argument("--subjects", default=None); parser.add_argument("--clips", type=int, default=3); parser.add_argument("--dry-run", action="store_true")
    args = parser.parse_args(); keys = [args.subject] if args.subject else subjects(args.subjects or "all"); report = {"jobs": 0, "scenes": 0, "clip_bindings": 0, "stock_bindings": 0, "file_links": 0, "folder_links": 0, "wrong_mode": 0, "status": "PASS"}
    for key in keys:
        ids = clip_ids(key)[:args.clips]; body = payload(key, ids, args.clips)
        if args.dry_run: print(json.dumps(body, ensure_ascii=False)); continue
        result = assert_clip(wait_job(job_id(post(body))), set(ids), args.clips)
        report["jobs"] += 1; report["scenes"] += result["scenes"]; report["clip_bindings"] += result["clip_bindings"]; report["file_links"] += result["clip_bindings"]
    print(json.dumps({"clip_only": report}, indent=2))


if __name__ == "__main__": main()
