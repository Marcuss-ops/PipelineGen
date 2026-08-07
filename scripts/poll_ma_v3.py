#!/usr/bin/env python3
"""Poll a stock job every 15s until terminal state."""
import json
import os
import subprocess
import sys
import time

JOB = sys.argv[1] if len(sys.argv) > 1 else "job_1786099212099048671_66a4d7ab"
POLL_SEC = 15
MAX_MINUTES = 30
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
        return {"_raw": out.stdout[:300]}


last_status = None
started = time.time()
deadline = started + MAX_MINUTES * 60
while time.time() < deadline:
    d = fetch()
    status = d.get("status", "?")
    progress = d.get("progress")
    updated = d.get("updated_at", "")
    err = (d.get("error") or "")[:90]
    now = time.strftime("%H:%M:%S")
    if status != last_status:
        print(f"[{now}] === TRANSITION → {status} === progress={progress} updated={updated}")
        last_status = status
    else:
        print(f"[{now}] status={status} progress={progress} updated={updated} err={err}")
    if status in TERMINAL:
        print(f"\nFINAL: {status}")
        print("error:", d.get("error"))
        timeline = d.get("timeline") or []
        print("timeline:")
        for ev in timeline:
            print(f"  {ev.get('created_at')}  {ev.get('type')}  {ev.get('message','')[:110]}")
        break
    time.sleep(POLL_SEC)
else:
    print("TIMEOUT after", MAX_MINUTES, "minutes")
