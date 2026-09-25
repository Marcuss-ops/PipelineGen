#!/usr/bin/env python3
"""HipHop 36 — submit + poll (no LLM).

POSTs each <slug>/process.json to /api/clips/process with the admin
token, binds to the job_id returned in the ACK (Sept 2026 additive
field) and polls GET /api/jobs/{id}/full until the job reaches a
terminal status. Prints per-item status so a partial failure is
visible instead of being softened into a generic success.

The Idempotency-Key is sha256(payload)[:32] so a re-run of the same
payload is served from the server-side replay cache and returns the
SAME job (including a FAILED one). `--force <tag>` appends a tag to the
key so a terminal job can be retried as a genuinely new job while the
clip ids stay deterministic (upsert at the asset layer).

Usage:
  submit.py abba elton_john david_bowie
  submit.py --all --no-wait
  submit.py --all --wait 2700
  submit.py queen --force retry1
"""

from __future__ import annotations

import argparse
import hashlib
import json
import os
import sys
import time
import urllib.error
import urllib.parse
import urllib.request
from pathlib import Path

HERE = Path(__file__).resolve().parent
REPO = HERE.parents[2]
BASE = os.environ.get("VELOX_BASE", "http://127.0.0.1:8000")
TERMINAL = {"SUCCEEDED", "FAILED", "CANCELLED", "DEAD_LETTERED", "EXPIRED"}


def token() -> str:
    tok = os.environ.get("VELOX_ADMIN_TOKEN")
    if tok:
        return tok.strip()
    env = REPO / ".env"
    for line in env.read_text().splitlines():
        if line.startswith("VELOX_ADMIN_TOKEN="):
            return line.split("=", 1)[1].strip()
    sys.exit("VELOX_ADMIN_TOKEN not found")


def request(method: str, path: str, body: bytes | None = None, idem: str = "", timeout: int = 120) -> dict:
    headers = {"Authorization": f"Bearer {token()}"}
    if body is not None:
        headers["Content-Type"] = "application/json"
    if idem:
        headers["Idempotency-Key"] = idem
    req = urllib.request.Request(f"{BASE}{path}", data=body, headers=headers, method=method)
    try:
        with urllib.request.urlopen(req, timeout=timeout) as resp:
            return json.loads(resp.read().decode())
    except urllib.error.HTTPError as exc:
        detail = exc.read().decode(errors="replace")[:600]
        return {"ok": False, "http_status": exc.code, "error": detail}


def submit(slug: str, force: str = "") -> str:
    path = HERE / slug / "process.json"
    raw = path.read_bytes()
    idem = hashlib.sha256(raw).hexdigest()[:32]
    if force:
        idem = f"{idem}-{force}"
    ack = request("POST", "/api/clips/process", raw, idem=idem, timeout=180)
    if not ack.get("ok"):
        print(f"FAIL {slug}: submit rejected: {json.dumps(ack, ensure_ascii=False)[:500]}")
        return ""
    raw_job = ack.get("job_id") or ack.get("jobId") or ""
    # Sept 2026 ACK: `job_id` carries the full queued job record, not a bare id.
    job = raw_job.get("id", "") if isinstance(raw_job, dict) else str(raw_job)
    print(f"ACK  {slug}: job_id={job or '(none in ACK)'} msg={str(ack.get('message'))[:90]}")
    if job:
        jobs_file = HERE / "cache" / "jobs.json"
        jobs_file.parent.mkdir(parents=True, exist_ok=True)
        known = json.loads(jobs_file.read_text()) if jobs_file.exists() else {}
        known[slug] = job
        jobs_file.write_text(json.dumps(known, indent=1, sort_keys=True))
    return job


def job_summary(job: dict) -> tuple[str, list[str]]:
    status = str(job.get("status") or job.get("current_step") or "?")
    lines: list[str] = []
    result = job.get("result") or {}
    items = None
    for key in ("items", "results", "clips"):
        if isinstance(result.get(key), list):
            items = result[key]
            break
    if items:
        ok = sum(1 for i in items if str(i.get("status")) in {"processed", "ok", "success"})
        bad = [i for i in items if str(i.get("status")) not in {"processed", "ok", "success"}]
        lines.append(f"items={len(items)} processed={ok} not_processed={len(bad)}")
        for i in bad[:8]:
            lines.append(
                f"  - {str(i.get('name'))[:52]!r} status={i.get('status')} "
                f"failure_code={i.get('failure_code')} err={str(i.get('error'))[:120]}"
            )
    err = job.get("error")
    if err:
        lines.append(f"error={str(err)[:300]}")
    return status, lines


def poll(slug: str, job_id: str, deadline: float) -> None:
    if not job_id:
        return
    seen = ""
    while time.time() < deadline:
        data = request("GET", f"/api/jobs/{urllib.parse.quote(job_id)}/full", timeout=120)
        job = data.get("job") or data
        status, lines = job_summary(job)
        stamp = f"{time.strftime('%H:%M:%S')} {slug} {status} progress={job.get('progress')}"
        if stamp != seen:
            print(stamp, flush=True)
            seen = stamp
        if status.upper() in TERMINAL:
            for line in lines:
                print(f"     {line}")
            if status.upper() != "SUCCEEDED":
                print(f"     FAILED {slug}: {json.dumps(job.get('error') or job.get('current_step'), ensure_ascii=False)[:300]}")
            return
        time.sleep(20)
    print(f"TIMEOUT {slug} job {job_id} still running after the wait budget")


def main() -> int:
    ap = argparse.ArgumentParser()
    ap.add_argument("slugs", nargs="*")
    ap.add_argument("--all", action="store_true")
    ap.add_argument("--wait", type=int, default=2400, help="seconds to poll after submitting")
    ap.add_argument("--no-wait", action="store_true")
    ap.add_argument("--force", default="", help="nonce appended to the Idempotency-Key to retry a terminal job")
    args = ap.parse_args()

    slugs = args.slugs
    if args.all or not slugs:
        slugs = sorted(p.name for p in HERE.iterdir() if (p / "process.json").exists())
    if not slugs:
        return 1

    jobs = {}
    for slug in slugs:
        job = submit(slug, force=args.force)
        if job:
            jobs[slug] = job
        time.sleep(1)
    if args.no_wait or not jobs:
        return 0

    deadline = time.time() + args.wait
    from concurrent.futures import ThreadPoolExecutor

    with ThreadPoolExecutor(max_workers=4) as pool:
        list(pool.map(lambda kv: poll(kv[0], kv[1], deadline), jobs.items()))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
