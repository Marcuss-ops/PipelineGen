#!/usr/bin/env python3
"""Re-cut the four Obama segments that exceeded 60s / 15s (hook)."""
import json
import sys
import time
from pathlib import Path

HERE = Path(__file__).resolve().parent
sys.path.insert(0, str(HERE))
import run_remaining as rr  # noqa: E402

MANIFEST = HERE / "manifest-remaining.json"
OUT = HERE / "fix-results.json"

FIXES = {
    "9": {
        "id": "xOl7HTXuLvk",
        # original_start, original_end, new_end, kind
        "segments": [("51:00", "52:15", "52:00", None)],
    },
    "12": {
        "id": "yvYCzh0BcmM",
        "segments": [
            ("40:02", "41:15", "41:02", None),
            ("00:39", "00:55", "00:54", "hook"),
            ("40:02", "40:39", "40:17", "hook"),
        ],
    },
}


def find_video(manifest, n):
    for v in manifest["videos"]:
        if str(v["n"]) == str(n):
            return v
    raise RuntimeError(f"video {n} not in manifest")


def fixed_clips(video, windows):
    clips = []
    for start, original_end, end, kind in windows:
        candidates = [
            c
            for c in video["clips"]
            if c["start"] == start
            and c["end"] == original_end
            and (kind is None or (c.get("kind") or "moment").lower() == kind)
        ]
        if not candidates:
            raise RuntimeError(
                f"no manifest clip {start}-{original_end} kind={kind}"
            )
        c = dict(candidates[0])
        c["end"] = end
        clips.append(c)
    return clips


def process(n, video, windows):
    clips = fixed_clips(video, windows)
    fixed = dict(video)
    fixed["clips"] = clips
    segments = rr.build_segments(fixed)
    for seg in segments:
        lang, text = rr.transcript_for_window(video["url"], seg["start"], seg["end"])
        if text and lang == "en":
            seg.setdefault("texts", []).insert(
                0,
                {
                    "language_code": "en",
                    "source_type": "provided",
                    "is_original": True,
                    "transcript": text,
                },
            )
        else:
            rr.log(f"    no en transcript for {seg['start']}-{seg['end']}; server fallback")

    payload = {
        "url": video["url"],
        "strategy": "verify",
        "concurrency": 1,
        "keep_audio": True,
        "write_summary": True,
        "destination": {"folder_id": rr.FOLDER_ID, "create_subfolder": False},
        "segments": segments,
    }
    key = "obama-en-fix-v1:" + video["id"] + ":" + "|".join(
        f"{s['start']}-{s['end']}" for s in segments
    )
    ack = rr.http(
        "POST",
        "/api/clips/process",
        body=payload,
        timeout=120,
        extra_headers={"Idempotency-Key": key},
    )
    job_id = ack.get("job_id")
    if isinstance(job_id, dict):
        job_id = job_id.get("id")
    if not job_id:
        raise RuntimeError(f"no job_id: {ack}")
    rr.log(f"    fix job {job_id} submitted ({len(segments)} segments)")
    d = rr.poll_job(job_id)
    res = d.get("result") or (d.get("job") or {}).get("result") or {}
    items = res.get("items") or []
    if len(items) != len(segments):
        raise RuntimeError(f"expected {len(segments)} items, got {len(items)}")
    ids = []
    for it in items:
        if it.get("status") not in ("processed", "succeeded", "skipped"):
            raise RuntimeError(f"clip {it.get('id')} status={it.get('status')} error={it.get('error')}")
        ids.append(it["id"])
    rr.log(f"    fix clips committed: {ids}")
    report = rr.run_backfill(ids)
    rows = rr.wait_assets(ids)
    return {
        "video": n,
        "video_id": video["id"],
        "job_id": job_id,
        "segments": [
            {
                "start": s["start"],
                "end": s["end"],
                "duration_seconds": rr.hms_to_sec(s["end"]) - rr.hms_to_sec(s["start"]),
                "name": s["name"],
                "kind": s.get("kind", "moment"),
            }
            for s in segments
        ],
        "items": items,
        "asset_ids": ids,
        "assets": rows,
        "backfill": report,
        "finished_at": time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime()),
    }


def main():
    manifest = json.loads(MANIFEST.read_text())
    results = []
    for n, cfg in FIXES.items():
        video = find_video(manifest, n)
        rr.log(f"=== fix video {n} {video['id']} ===")
        results.append(process(n, video, cfg["segments"]))
        OUT.write_text(json.dumps({"results": results}, indent=2, ensure_ascii=False))
    rr.log("fix complete")


if __name__ == "__main__":
    main()
