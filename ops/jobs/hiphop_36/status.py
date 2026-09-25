#!/usr/bin/env python3
"""HipHop 36 — job status (no LLM).

Maps slug -> job_id from cache/poll_*.log ACK lines plus cache/jobs.json
(written by submit.py when present), then prints, per slug, the job
status and every result item with its own status/error so a partial
failure is visible instead of hidden by the job-level rollup.

Usage:
  status.py                 # every slug ever submitted
  status.py queen madonna   # only these
  status.py --items         # dump full item payloads for failures
"""

from __future__ import annotations

import argparse
import json
import re
import sys
import urllib.error
import urllib.request
from pathlib import Path

HERE = Path(__file__).resolve().parent
REPO = HERE.parents[2]
CACHE = HERE / "cache"
ACK_RE = re.compile(r"^ACK\s+(\w+):\s+job_id=(job_\w+)")


def token() -> str:
    import os

    tok = os.environ.get("VELOX_ADMIN_TOKEN")
    if tok:
        return tok.strip()
    for line in (REPO / ".env").read_text().splitlines():
        if line.startswith("VELOX_ADMIN_TOKEN"):
            return line.split("=", 1)[1].strip().strip('"').strip("'")
    raise SystemExit("VELOX_ADMIN_TOKEN not found")


def get(path: str) -> dict:
    req = urllib.request.Request(
        "http://127.0.0.1:8000" + path,
        headers={"Authorization": f"Bearer {token()}"},
    )
    with urllib.request.urlopen(req, timeout=60) as resp:
        return json.load(resp)


def mapping() -> dict[str, str]:
    out: dict[str, str] = {}
    # Legacy poll logs first (they only know the first submission), then
    # cache/jobs.json which submit.py rewrites on every submit → the newest
    # job id wins, which is what a force-retry needs.
    for log in sorted(CACHE.glob("poll_*.log")):
        for line in log.read_text().splitlines():
            m = ACK_RE.match(line)
            if m:
                out[m.group(1)] = m.group(2)
    jobs_json = CACHE / "jobs.json"
    if jobs_json.exists():
        out.update(json.loads(jobs_json.read_text()))
    return out


def main() -> int:
    ap = argparse.ArgumentParser()
    ap.add_argument("slugs", nargs="*")
    ap.add_argument("--items", action="store_true", help="dump item errors")
    args = ap.parse_args()

    known = mapping()
    slugs = args.slugs or sorted(known)
    worst = 0
    for slug in slugs:
        jid = known.get(slug)
        if not jid:
            print(f"{slug:22s} NO-JOB (payload not submitted)")
            worst = max(worst, 1)
            continue
        try:
            d = get(f"/api/jobs/{jid}/full")
        except urllib.error.HTTPError as exc:
            print(f"{slug:22s} {jid} HTTP {exc.code}")
            worst = max(worst, 1)
            continue
        job = d.get("job") or d
        status = job.get("status")
        res = job.get("result") or {}
        items = res.get("items") or []
        ok = sum(1 for i in items if i.get("status") == "processed")
        print(
            f"{slug:22s} {status:12s} {ok}/{len(items)} processed  "
            f"progress={job.get('progress')} {jid}"
        )
        if status != "SUCCEEDED":
            worst = max(worst, 1)
        if args.items or status != "SUCCEEDED":
            for it in items:
                if it.get("status") != "processed" or args.items:
                    err = it.get("error") or job.get("error") or ""
                    print(f"    - {it.get('status')} {it.get('clip_id')} {str(err)[:600]}")
            if status != "SUCCEEDED" and job.get("error"):
                print(f"    job.error: {str(job['error'])[:1200]}")
    return worst


if __name__ == "__main__":
    sys.exit(main())
