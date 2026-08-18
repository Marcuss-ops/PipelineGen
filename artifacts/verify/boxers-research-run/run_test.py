#!/usr/bin/env python3
"""Cold script-generate test: 10 boxers, source.type=research, cache force_refresh.

Saves:
  response.json          - full terminal job response
  script.txt             - final script text
  research_report.json   - aggregated research report + evidence pack (if present)
"""

from __future__ import annotations

import json
import os
import sys
import time
import urllib.error
import urllib.request
import uuid
from pathlib import Path

BASE = os.environ.get("VELOX_API", "http://127.0.0.1:8000")
TOKEN = os.environ.get("VELOX_ADMIN_TOKEN", "")
PAYLOAD_PATH = Path(sys.argv[1]) if len(sys.argv) > 1 else Path(__file__).resolve().parents[1] / "boxers-richest-10.payload.json"
OUT_DIR = Path(__file__).resolve().parent


def request(method: str, path: str, payload: dict | None = None, timeout: int = 60) -> tuple[int, dict]:
    body = json.dumps(payload).encode() if payload is not None else None
    headers = {"Authorization": f"Bearer {TOKEN}"}
    if payload is not None:
        headers["Content-Type"] = "application/json"
        headers["Idempotency-Key"] = f"boxers-research-10-{uuid.uuid4().hex}"
    req = urllib.request.Request(f"{BASE}{path}", data=body, method=method, headers=headers)
    try:
        with urllib.request.urlopen(req, timeout=timeout) as resp:
            return resp.status, json.loads(resp.read().decode())
    except urllib.error.HTTPError as exc:
        detail = exc.read().decode(errors="replace")
        return exc.code, {"_http_error": detail[:1000]}


def terminal_status(full: dict) -> str:
    for key in ("status",):
        v = full.get(key)
        if v:
            return str(v)
    job = full.get("job") or {}
    v = job.get("status") or (job.get("result") or {}).get("status")
    if v:
        return str(v)
    v = (full.get("result") or {}).get("status")
    return str(v or "")


def main() -> int:
    if not TOKEN:
        print("VELOX_ADMIN_TOKEN required", file=sys.stderr)
        return 2
    payload = json.loads(PAYLOAD_PATH.read_text())

    code, resp = request("POST", "/api/script/generate", payload)
    print(f"POST /api/script/generate -> HTTP {code}")
    if code not in (200, 202):
        print(json.dumps(resp, ensure_ascii=False)[:2000], file=sys.stderr)
        return 1
    job_id = resp.get("job_id") or resp.get("id") or resp.get("data", {}).get("job_id")
    if not job_id:
        print(f"no job_id in response: {json.dumps(resp)[:500]}", file=sys.stderr)
        return 1
    print(f"job_id={job_id}")

    deadline = time.time() + int(os.environ.get("SMOKE_TIMEOUT_SECONDS", "2700"))
    full = None
    while time.time() < deadline:
        code, full = request("GET", f"/api/jobs/{job_id}/full", timeout=30)
        if code != 200:
            print(f"poll HTTP {code}: {str(full)[:300]}", file=sys.stderr)
            time.sleep(3)
            continue
        status = terminal_status(full).upper()
        if status in {"SUCCEEDED", "SUCCEEDED_WITH_WARNINGS", "FAILED", "CANCELLED", "DEAD_LETTER", "COMPLETED"}:
            print(f"terminal status={status}")
            break
        elapsed = int(time.time() - (deadline - int(os.environ.get("SMOKE_TIMEOUT_SECONDS", "2700"))))
        print(f"  polling... status={status} elapsed={elapsed}s", flush=True)
        time.sleep(5)

    if full is None:
        print("job timeout", file=sys.stderr)
        return 1

    OUT_DIR.mkdir(parents=True, exist_ok=True)
    (OUT_DIR / "response.json").write_text(json.dumps(full, ensure_ascii=False, indent=2))

    # Locate item results across response shapes.
    items = (
        (full.get("result") or {}).get("data", {}).get("items")
        or (full.get("job") or {}).get("result", {}).get("data", {}).get("items")
        or []
    )
    print(f"items in response: {len(items)}")

    for idx, item in enumerate(items):
        out = (item.get("result") or {}).get("output") or item.get("output") or {}
        text = str(out.get("text") or "").strip()
        if not text:
            data = (item.get("result") or {}).get("data") or {}
            out = data.get("output") or {}
            text = str(out.get("text") or "").strip()
        if not text:
            # fallback: script field
            script = out.get("script") or {}
            text = str(script.get("text") or "").strip()
        if text:
            (OUT_DIR / "script.txt").write_text(text)
            print(f"script.txt: {len(text)} chars, {len(text.split())} words")
        research = out.get("research_report") or item.get("research") or item.get("research_report")
        if isinstance(research, dict):
            (OUT_DIR / "research_report.json").write_text(json.dumps(research, ensure_ascii=False, indent=2))
            print(f"research_report.json: {len(json.dumps(research))} chars")
        elif not isinstance(research, dict):
            print("research_report: not found in item output")

    # Also dump the job-level summary bits that matter for the report.
    job = full.get("job") or {}
    result = full.get("result") or {}
    print(json.dumps({
        "status": terminal_status(full),
        "job_type": job.get("type") or result.get("type"),
        "model": (out.get("provenance") or {}).get("model") if items else None,
    }, ensure_ascii=False))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
