#!/usr/bin/env python3
"""Poll Muhammad Ali v3 stock job until terminal state, then probe all
completed clips with ffprobe and report per-clip + total durations."""
import json
import os
import subprocess
import sys
import time

JOB = "job_1786099212099048671_66a4d7ab"
WORK = os.path.join("data", "stock", "workspaces", JOB, "extracted")
POLL_SEC = 20
MAX_MINUTES = 25
TERMINAL = {"SUCCEEDED", "INDEX_PENDING", "FAILED", "CANCELLED"}


def fetch():
    cmd = [
        "scripts/with-velox-auth", "bash", "-c",
        f'curl -sS -H "Authorization: Bearer $VELOX_ADMIN_TOKEN" '
        f'http://127.0.0.1:8000/api/jobs/{JOB}/full',
    ]
    out = subprocess.run(cmd, capture_output=True, text=True, cwd=os.getcwd())
    try:
        return json.loads(out.stdout)
    except Exception:
        return {}


def probe(path):
    r = subprocess.run(
        ["ffprobe", "-v", "error", "-show_entries", "format=duration",
         "-of", "json", path],
        capture_output=True, text=True)
    try:
        return float(json.loads(r.stdout)["format"]["duration"])
    except Exception:
        return None


# ── Poll until terminal ─────────────────────────────────────────────
last = None
started = time.time()
deadline = started + MAX_MINUTES * 60
final = None
while time.time() < deadline:
    d = fetch()
    status = d.get("status", "?")
    upd = d.get("updated_at", "")
    now = time.strftime("%H:%M:%S")
    if status != last:
        print(f"[{now}] === TRANSITION -> {status} === updated={upd}")
        last = status
    if status in TERMINAL:
        final = d
        print(f"FINAL: {status}")
        print("error:", d.get("error"))
        break
    time.sleep(POLL_SEC)
else:
    print("TIMEOUT polling — job still running")

# ── Probe clips ─────────────────────────────────────────────────────
if not os.path.isdir(WORK):
    print(f"workspace missing: {WORK}")
    sys.exit(1)

files = sorted(f for f in os.listdir(WORK) if f.lower().endswith(".mp4"))
completed = [f for f in files if ".part" not in f]
partials = [f for f in files if ".part" in f]

print(f"\n=== CLIPS (final) ===")
print(f"completed: {len(completed)}  partial: {len(partials)}")

durations = []
for f in completed:
    p = os.path.join(WORK, f)
    d = probe(p)
    if d is None:
        print(f"  {f}: PROBE FAILED")
        continue
    durations.append(d)
    flag = "  <-- SHORT" if d < 17.0 else ""
    print(f"  {f}: {d:7.2f}s{flag}")

total = sum(durations)
print(f"\n=== SUMMARY ===")
print(f"clips probed: {len(durations)}/30")
print(f"total real duration: {total:.2f}s (expected ~598-602s)")

if len(durations) < 30:
    print(f"!! CLIPS LOST: {30 - len(durations)} clips missing "
          f"({30 * 20.0 - total:.1f}s short of 600s)")
elif total < 580:
    print("!! CLIPS LOST: total below 580s")
elif total < 598 or total > 602:
    print(f"NOTE: total {total:.2f}s outside 598-602s window")
else:
    print("OK: 30 clips, total in 598-602s window")
