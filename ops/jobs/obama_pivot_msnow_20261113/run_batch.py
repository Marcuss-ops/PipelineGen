#!/usr/bin/env python3
"""Sequential runner for the Pivot / MS NOW Obama batch.

Reuses the proven Obama runner with its own manifest and state. Adds a
preflight duration/source-range check before any clip is submitted.
"""
from __future__ import annotations

import json
import sys
from pathlib import Path

HERE = Path(__file__).resolve().parent
REFACTORED = HERE.parents[2]
sys.path.insert(0, str(REFACTORED / "ops/jobs/obama_extra_20261113"))

import run_extra as re  # noqa: E402

rr = re.rr
rr.OUT = HERE
rr.MANIFEST = HERE / "manifest.json"
rr.STATE = HERE / "state.json"
rr.REPORT = HERE / "BATCH-REPORT.json"


def hms(ts: str) -> int:
    parts = [int(p) for p in ts.strip().split(":")]
    while len(parts) < 3:
        parts.insert(0, 0)
    return parts[0] * 3600 + parts[1] * 60 + parts[2]


def validate() -> dict:
    manifest = json.loads(rr.MANIFEST.read_text())
    summary = {"videos": [], "problems": []}
    for video in manifest["videos"]:
        rows = []
        seen = set()
        for clip in video["clips"]:
            kind = (clip.get("kind") or "moment").lower()
            start = hms(clip["start"])
            end = hms(clip["end"])
            duration = end - start
            limit = 15 if kind == "hook" else 60
            if end <= start:
                summary["problems"].append(f"{video['id']} {clip['start']}-{clip['end']}: empty")
            if duration > limit:
                summary["problems"].append(
                    f"{video['id']} {clip['start']}-{clip['end']}: {duration}s > {limit}s ({kind})"
                )
            if end > video["source_duration_s"]:
                summary["problems"].append(
                    f"{video['id']} {clip['start']}-{clip['end']}: beyond source "
                    f"{video['source_duration_s']}s"
                )
            key = (clip["start"], clip["end"], kind)
            if key in seen:
                summary["problems"].append(f"{video['id']}: duplicate window {key}")
            seen.add(key)
            rows.append(
                {
                    "kind": kind,
                    "start": clip["start"],
                    "end": clip["end"],
                    "duration_seconds": duration,
                    "limit_seconds": limit,
                    "name": clip["name"],
                }
            )
        summary["videos"].append(
            {
                "n": video["n"],
                "id": video["id"],
                "source_duration_s": video["source_duration_s"],
                "segments": len(rows),
                "clips": sum(1 for r in rows if r["kind"] != "hook"),
                "hooks": sum(1 for r in rows if r["kind"] == "hook"),
                "max_segment_seconds": max(r["duration_seconds"] for r in rows),
                "rows": rows,
            }
        )
    if summary["problems"]:
        raise SystemExit("preflight failed: " + "; ".join(summary["problems"]))
    (HERE / "preflight.json").write_text(json.dumps(summary, indent=2, ensure_ascii=False))
    for v in summary["videos"]:
        rr.log(
            f"preflight video {v['n']} {v['id']}: {v['clips']} clips + {v['hooks']} hooks, "
            f"max {v['max_segment_seconds']}s"
        )
    return summary


if __name__ == "__main__":
    validate()
    rr.main()
