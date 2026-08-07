#!/usr/bin/env python3
"""Check Muhammad Ali v3 job status, then locate produced MP4 clips and
probe each with ffprobe, printing per-clip duration and total."""
import json
import os
import subprocess
import sys
import time

JOB = "job_1786099212099048671_66a4d7ab"
ROOT = os.getcwd()


def sh(cmd):
    r = subprocess.run(cmd, capture_output=True, text=True, shell=True)
    return r.stdout.strip()


def fetch_job():
    cmd = [
        "scripts/with-velox-auth", "bash", "-c",
        f'curl -sS -H "Authorization: Bearer $VELOX_ADMIN_TOKEN" '
        f'http://127.0.0.1:8000/api/jobs/{JOB}/full',
    ]
    out = subprocess.run(cmd, capture_output=True, text=True, cwd=ROOT)
    try:
        return json.loads(out.stdout)
    except Exception:
        return {"_raw": out.stdout[:400]}


print("=== JOB STATUS ===")
d = fetch_job()
print("status:", d.get("status"))
print("progress:", d.get("progress"))
print("error:", (d.get("error") or "")[:250])
print("updated_at:", d.get("updated_at"))

print("\n=== CANDIDATE CLIP DIRECTORIES ===")
# Look for clip outputs from the stock pipeline runs.
search_dirs = [
    "data/tmp",
    "data",
]
candidates = []
for base in search_dirs:
    if not os.path.isdir(base):
        continue
    for dirpath, dirnames, filenames in os.walk(base):
        # Skip huge media trees we don't need
        mp4s = [f for f in filenames if f.lower().endswith(".mp4")]
        if mp4s:
            for f in mp4s:
                p = os.path.join(dirpath, f)
                try:
                    mtime = os.path.getmtime(p)
                except OSError:
                    continue
                candidates.append((mtime, p))

# Only files from today's runs (>= 09:30 UTC approx) to avoid old library clips
cutoff = time.time() - 6 * 3600  # last 6 hours
recent = [p for (m, p) in candidates if m >= cutoff]
recent.sort()

print(f"recent MP4 files (last 6h): {len(recent)}")
for p in recent:
    sz = os.path.getsize(p)
    print(f"  {sz:>12,} bytes  {p}")
