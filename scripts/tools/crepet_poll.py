#!/usr/bin/env python3
"""Single poller for the Crepet clip-extraction jobs.

Reads jobs.txt (`<videoID> <job_id>`), polls /api/jobs/{id}/full, prints only
state changes and the final per-item summary, exits when every job is terminal
or after the deadline.
"""
from __future__ import annotations

import json
import os
import sys
import time
import urllib.request
from pathlib import Path

BASE = (os.environ.get("VELOX_BASE_URL") or "http://127.0.0.1:8000").rstrip("/")
TOKEN = os.environ.get("VELOX_ADMIN_TOKEN", "")
JOBS = Path(os.environ.get("CREPET_JOBS_FILE")
            or (Path(__file__).resolve().parents[2] / "data" / "tclip" / "crepet" / "jobs.txt"))
TERMINAL = {"SUCCEEDED", "FAILED", "CANCELLED", "DEAD_LETTER", "COMPLETED", "SUCCESS", "DONE"}


def get(path: str) -> dict:
    req = urllib.request.Request(BASE + path, headers={"X-Velox-Admin-Token": TOKEN})
    with urllib.request.urlopen(req, timeout=60) as r:
        return json.load(r)


def main() -> int:
    deadline = time.time() + float(sys.argv[1] if len(sys.argv) > 1 else 540)
    jobs = [l.split() for l in JOBS.read_text().splitlines() if l.strip()]
    last: dict[str, str] = {}
    phase_last: dict[str, str] = {}
    done: set[str] = set()
    while time.time() < deadline:
        for vid, jid in jobs:
            if vid in done:
                continue
            try:
                b = get(f"/api/jobs/{jid}/full")
            except Exception as e:  # noqa: BLE001
                print(f"{vid}: poll error {e}")
                continue
            job = b.get("job") or {}
            status = b.get("status") or job.get("status") or "UNKNOWN"
            if status != last.get(vid):
                print(f"{vid} {jid} -> {status} progress={b.get('progress')} stage={b.get('current_stage')}")
                last[vid] = status
            # Intermediate phases from the durable stage table, printed once each
            # as they change (phase name, percent, detail).
            for row in b.get("stages") or []:
                key = f"{row.get('stage')}|{row.get('status')}|{row.get('progress')}|{row.get('detail')}"
                if key != phase_last.get(vid):
                    phase_last[vid] = key
                    print(f"  {vid} phase {row.get('stage')} [{row.get('status')}] {row.get('progress')}% {row.get('detail') or ''}".rstrip())
            if status in TERMINAL:
                done.add(vid)
                result = (b.get("result") or job.get("result") or {})
                stats = result.get("stats")
                items = result.get("items") or []
                print(f"  {vid} FINAL status={status} stats={stats}")
                for it in items:
                    print(f"    [{it.get('status')}] {it.get('start')}-{it.get('end')} {it.get('name')} {it.get('error','')}".rstrip())
                err = b.get("error") or job.get("error")
                if err:
                    print(f"  {vid} ERROR: {str(err)[:400]}")
        if len(done) == len(jobs):
            print("ALL TERMINAL")
            return 0
        time.sleep(20)
    print("POLL DEADLINE REACHED")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
