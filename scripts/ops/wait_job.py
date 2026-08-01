#!/usr/bin/env python3
"""wait_job.py — single canonical job poller for PipelineGen.

Polls ``GET /api/jobs/{id}/full`` for one or more jobs and prints ONLY
state transitions (``PENDING → RUNNING → SUCCEEDED`` plus progress
deltas). It never re-analyzes the job on every check, so a whole run's
polling is one process with one steady cadence.

Design rules (operational protocol, August 2026):
  * Single poller for all jobs — pass parent and child job ids together.
  * Defaults: --interval 10, --timeout 600.
  * Prints only when status or progress actually changes.
  * Stdlib-only (urllib), mirroring scripts/velox_client.py.
  * Auth: ``Authorization: Bearer`` with the canonical admin token
    (VELOX_ADMIN_TOKEN or /etc/pipelinegen/pipelinegen.env).

Exit codes:
  0  all jobs reached a successful terminal state (SUCCEEDED/COMPLETED)
  1  at least one job failed (FAILED/CANCELLED/DEAD_LETTERED/PARTIALLY_SUCCEEDED)
  2  timeout before terminal state
  3  usage/configuration error (bad token, HTTP error)

Usage:
    scripts/ops/wait_job.py --job-id job_abc123 [--job-id job_def456] \\
        [--interval 10] [--timeout 600] [--base-url http://127.0.0.1:8000]
"""

from __future__ import annotations

import argparse
import json
import os
import re
import sys
import time
import urllib.error
import urllib.request
from typing import Any, Dict, List, Optional

TOKEN_FILE = "/etc/pipelinegen/pipelinegen.env"
TOKEN_RE = re.compile(r"^[a-fA-F0-9]{64}$")

# Canonical broker status surface (internal/kernel/job). PARTIALLY_SUCCEEDED
# and DEAD_LETTERED are terminal but NOT a clean PASS.
SUCCESS_TERMINAL = frozenset({"SUCCEEDED", "COMPLETED"})
FAILED_TERMINAL = frozenset({"FAILED", "CANCELLED", "DEAD_LETTERED", "PARTIALLY_SUCCEEDED"})
TERMINAL = SUCCESS_TERMINAL | FAILED_TERMINAL


def load_token() -> str:
    """Return the canonical admin token from env or the SSOT env file.

    The 64-hex SSOT validation (AGENTS.md) applies to BOTH sources so a
    malformed env var cannot silently bypass the gate.
    """
    token = os.environ.get("VELOX_ADMIN_TOKEN", "")
    if not token:
        try:
            with open(TOKEN_FILE, encoding="utf-8") as handle:
                for line in handle:
                    if line.startswith("VELOX_ADMIN_TOKEN="):
                        token = line.split("=", 1)[1].strip()
                        break
        except OSError:
            return ""
    if not TOKEN_RE.match(token):
        return ""
    return token


def fetch_job(base_url: str, token: str, job_id: str) -> Dict[str, Any]:
    """GET /api/jobs/{id}/full. Raises RuntimeError on HTTP/transport error."""
    base_url = (base_url or "").rstrip("/")
    if not base_url:
        raise RuntimeError("empty --base-url (set VELOX_BASE_URL or pass --base-url)")
    url = f"{base_url}/api/jobs/{job_id}/full"
    req = urllib.request.Request(url, headers={"Authorization": f"Bearer {token}"})
    try:
        with urllib.request.urlopen(req, timeout=30) as response:
            body = json.loads(response.read().decode("utf-8"))
        if not isinstance(body, dict):
            raise RuntimeError(f"job {job_id}: unexpected non-object payload")
        return body
    except ValueError as exc:
        raise RuntimeError(f"job {job_id}: invalid JSON payload: {exc}") from exc
    except urllib.error.HTTPError as exc:
        raise RuntimeError(
            f"job {job_id}: HTTP {exc.code} from {url}"
        ) from exc
    except (urllib.error.URLError, TimeoutError, OSError) as exc:
        raise RuntimeError(f"job {job_id}: transport error: {exc}") from exc


def main(argv: Optional[List[str]] = None) -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument(
        "--job-id",
        action="append",
        required=True,
        metavar="ID",
        help="job id to poll (repeatable: parent + children)",
    )
    parser.add_argument("--interval", type=int, default=10, help="poll interval seconds (default 10)")
    parser.add_argument("--timeout", type=int, default=600, help="max wait seconds (default 600)")
    parser.add_argument(
        "--base-url",
        default=os.environ.get("VELOX_BASE_URL") or "http://127.0.0.1:8000",
    )
    args = parser.parse_args(argv)

    if args.interval <= 0 or args.timeout <= 0:
        print("wait_job: interval and timeout must be positive", file=sys.stderr)
        return 3

    token = load_token()
    if not token:
        print(
            "wait_job: VELOX_ADMIN_TOKEN unset and no readable "
            f"{TOKEN_FILE} (use scripts/with-velox-auth)",
            file=sys.stderr,
        )
        return 3

    job_ids = args.job_id
    # last_seen[job_id] = (status, progress)
    last_seen: Dict[str, tuple] = {jid: (None, None) for jid in job_ids}
    outcomes: Dict[str, str] = {}
    deadline = time.monotonic() + args.timeout
    started = time.strftime("%H:%M:%S")

    print(f"wait_job: watching {len(job_ids)} job(s) [{started}] interval={args.interval}s timeout={args.timeout}s")

    remaining = job_ids
    while time.monotonic() < deadline:
        # Poll once, read all deltas for every job on this tick.
        deltas: List[str] = []
        progress_deltas: List[str] = []
        next_remaining: List[str] = []
        for jid in remaining:
            try:
                body = fetch_job(args.base_url, token, jid)
            except RuntimeError as exc:
                print(f"wait_job: {exc}", file=sys.stderr)
                return 3
            status = str(body.get("status", "")).upper()
            if not status:
                next_remaining.append(jid)
                continue
            try:
                progress = int(body.get("progress", 0) or 0)
            except (TypeError, ValueError):
                progress = 0
            prev_status, prev_progress = last_seen[jid]
            if status != prev_status:
                arrow = f"{prev_status} → {status}" if prev_status else status
                deltas.append(f"{jid} {arrow}")
                last_seen[jid] = (status, progress)
            elif progress != prev_progress:
                progress_deltas.append(f"{jid} progress {prev_progress}% → {progress}%")
                last_seen[jid] = (status, progress)
            if status in TERMINAL:
                outcomes[jid] = status
            else:
                next_remaining.append(jid)
        remaining = next_remaining

        now = time.strftime("%H:%M:%S")
        for delta in deltas:
            print(f"[{now}] {delta}")
        for delta in progress_deltas:
            print(f"[{now}] {delta}")

        if not remaining:
            break
        time.sleep(args.interval)

    # All jobs terminal within budget?
    pending = [jid for jid in job_ids if jid not in outcomes]
    if pending:
        print(
            f"wait_job: TIMEOUT after {args.timeout}s — still active: {', '.join(pending)}",
            file=sys.stderr,
        )
        return 2

    failed = [jid for jid, st in outcomes.items() if st in FAILED_TERMINAL]
    for jid in job_ids:
        print(f"[{time.strftime('%H:%M:%S')}] {jid} final={outcomes[jid]}")
    if failed:
        print(f"wait_job: FAILED job(s): {', '.join(failed)}", file=sys.stderr)
        return 1
    print("wait_job: PASS")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
