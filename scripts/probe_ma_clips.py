#!/usr/bin/env python3
"""Probe all completed MP4 clips in the Muhammad Ali v3 stock workspace
with ffprobe, print per-clip real duration and the total, flag lost clips."""
import json
import os
import subprocess
import sys

JOB = "job_1786099212099048671_66a4d7ab"
WORK = os.path.join("data", "stock", "workspaces", JOB, "extracted")

EXPECTED_CLIPS = 30
EXPECTED_PER = 20.0


def probe(path):
    r = subprocess.run(
        ["ffprobe", "-v", "error", "-show_entries", "format=duration",
         "-of", "json", path],
        capture_output=True, text=True,
    )
    try:
        d = json.loads(r.stdout)
        return float(d["format"]["duration"])
    except Exception:
        return None


if not os.path.isdir(WORK):
    print(f"workspace not found: {WORK}")
    sys.exit(1)

files = sorted(f for f in os.listdir(WORK) if f.lower().endswith(".mp4"))
completed = [f for f in files if ".part." not in f and not f.endswith(".part.mp4")]
partials = [f for f in files if ".part" in f]

print(f"total .mp4 files: {len(files)}")
print(f"completed clips:  {len(completed)}")
print(f"partial clips:    {len(partials)}")

durations = []
print("\n=== per-clip real durations (ffprobe) ===")
for f in completed:
    p = os.path.join(WORK, f)
    d = probe(p)
    if d is None:
        print(f"  {f}: PROBE FAILED")
        continue
    durations.append(d)
    flag = ""
    if d < EXPECTED_PER - 3:
        flag = "  <-- SHORT!"
    print(f"  {f}: {d:7.2f}s{flag}")

total = sum(durations)
print(f"\n=== SUMMARY ===")
print(f"completed clips probed: {len(durations)}")
print(f"total duration: {total:7.2f}s")
print(f"expected:        ~{EXPECTED_CLIPS * EXPECTED_PER:.0f}s (30 x 20s)")

if total < 580:
    print("\n!!! WARNING: total < 580s — CLIPS LOST !!!")
    print(f"missing: {EXPECTED_CLIPS - len(durations)} clips, "
          f"{EXPECTED_CLIPS * EXPECTED_PER - total:.1f}s short")
elif len(durations) < EXPECTED_CLIPS:
    print(f"\nNOTE: {EXPECTED_CLIPS - len(durations)} clips still missing "
          "(job may still be cutting)")
else:
    print("\nOK: total within expected range")
