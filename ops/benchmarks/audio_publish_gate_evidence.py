#!/usr/bin/env python3
"""audio_publish_gate_evidence.py — verify the fair Drive gate on the final-audio path.

Motivation (2026-09-28, gate Drive fair): before the shared `FairSemaphore` was
wired into `finalAudioPublisherAdapter`, the `audio_publish` stage did NOT acquire
the Drive upload gate, so cross-job contention serialized it behind whatever else
was uploading. Under 3 concurrent publication phases it reached 85 741 ms (see
PIPELINE-WASTE-AUDIT-2026-09-12.md §19) — far above the 10 s acceptance bar.

This tool reads the canonical observability store (`run_observability.report_json`,
the RunReport JSON) and, for a given job id, proves two things on the REAL run:

  1. the `audio_publish` stage wall is under the threshold (default 10 000 ms);
  2. a `semaphore_wait` interval with `component == "drive"` falls INSIDE the
     `audio_publish` window — i.e. the final-audio path actually acquired the
     shared gate. Pre-gate runs (deployed before 2026-09-28T19:35:40Z) show
     `drive_waits_inside_audio_publish == 0` even when the stage explodes.

Usage:
  python3 ops/benchmarks/audio_publish_gate_evidence.py --job job_... [--job job_...]
  python3 ops/benchmarks/audio_publish_gate_evidence.py --since 2026-09-28T19:35:40Z
  python3 ops/benchmarks/audio_publish_gate_evidence.py --job job_... --json out.json
  python3 ops/benchmarks/audio_publish_gate_evidence.py --self-test

Exit codes: 0 = every evaluated job passes, 1 = at least one fails / no data,
2 = usage error. `--self-test` exercises the window/counting logic on fixtures
without touching the store.
"""

from __future__ import annotations

import argparse
import json
import re
import sqlite3
import sys
from datetime import datetime, timezone

# The observability plane stamps RFC3339 with NANOSECOND precision
# ("...T19:58:48.052850969Z"). datetime.fromisoformat only learned 9-digit
# fractions in Python 3.11, and the deployed interpreter here is 3.10, so the
# fraction is clamped to the 6 digits microsecond resolution can hold before
# parsing. Truncation (not rounding) keeps interval comparisons monotone.
_FRAC_RE = re.compile(r"(\.\d{6})\d*")

DEFAULT_DB = "data/observability/api_requests.db.sqlite"
DEFAULT_THRESHOLD_MS = 10_000
GATE_COMPONENT = "drive"
STAGE = "audio_publish"


# --------------------------------------------------------------------------- #
# parsing helpers
# --------------------------------------------------------------------------- #
def parse_ts(value: str | None) -> datetime | None:
    """Parse an RFC3339 timestamp; return None when absent or malformed."""
    if not value:
        return None
    txt = value.strip()
    if txt.endswith("Z"):
        txt = txt[:-1] + "+00:00"
    txt = _FRAC_RE.sub(r"\1", txt)
    try:
        dt = datetime.fromisoformat(txt)
    except ValueError:
        return None
    if dt.tzinfo is None:
        dt = dt.replace(tzinfo=timezone.utc)
    return dt


def _stage_span(report: dict) -> tuple[datetime | None, datetime | None, int]:
    """Return (start, finish, duration_ms) of the LAST audio_publish stage.

    A run may publish sibling audio artifacts; the certified final-audio upload is
    the last one, and its wall is what the acceptance criterion bounds.
    """
    best: tuple[datetime | None, datetime | None, int] | None = None
    for stage in report.get("stages") or []:
        if stage.get("name") != STAGE:
            continue
        start = parse_ts(stage.get("started_at"))
        finish = parse_ts(stage.get("finished_at"))
        dur = int(stage.get("duration_ms") or 0)
        best = (start, finish, dur)
    if best is None:
        return (None, None, 0)
    return best


def _operation_wall_ms(report: dict) -> int:
    """Sum of work_ms over audio_publish operations (the inner upload cost)."""
    total = 0
    for op in report.get("operations") or []:
        if op.get("stage") != STAGE:
            continue
        total += int(op.get("duration_ms") or 0)
    return total


def _drive_waits_inside(report: dict, start: datetime | None, finish: datetime | None) -> list[dict]:
    """Drive semaphore_wait intervals that overlap the audio_publish window."""
    if start is None or finish is None:
        return []
    hits: list[dict] = []
    for wait in report.get("waits") or []:
        if wait.get("kind") != "semaphore_wait":
            continue
        if wait.get("component") != GATE_COMPONENT:
            continue
        w_start = parse_ts(wait.get("started_at"))
        w_finish = parse_ts(wait.get("finished_at")) or w_start
        if w_start is None:
            continue
        # Overlap (inclusive) against [start, finish].
        if w_start <= finish and (w_finish or w_start) >= start:
            hits.append(wait)
    return hits


def evaluate_run(job_id: str, report: dict, threshold_ms: int) -> dict:
    start, finish, dur = _stage_span(report)
    waits = _drive_waits_inside(report, start, finish)
    max_wait = max((int(w.get("duration_ms") or 0) for w in waits), default=0)
    return {
        "job_id": job_id,
        "status": report.get("status"),
        "audio_publish_stage_ms": dur,
        "audio_publish_operation_work_ms": _operation_wall_ms(report),
        "audio_publish_window_started_at": start.isoformat() if start else None,
        "audio_publish_window_finished_at": finish.isoformat() if finish else None,
        "drive_waits_inside_audio_publish": len(waits),
        "max_drive_wait_ms": max_wait,
        "threshold_ms": threshold_ms,
        "gate_engaged": len(waits) > 0,
        "under_threshold": 0 < dur < threshold_ms,
        "pass": len(waits) > 0 and 0 < dur < threshold_ms,
    }


# --------------------------------------------------------------------------- #
# store access
# --------------------------------------------------------------------------- #
def load_runs(db_path: str, job_ids: list[str] | None, since: str | None) -> list[tuple[str, dict]]:
    con = sqlite3.connect(f"file:{db_path}?mode=ro", uri=True)
    try:
        if job_ids:
            out: list[tuple[str, dict]] = []
            for job_id in job_ids:
                row = con.execute(
                    "SELECT report_json FROM run_observability WHERE job_id=?"
                    " ORDER BY created_at DESC LIMIT 1",
                    (job_id,),
                ).fetchone()
                if row is None:
                    raise SystemExit(f"job not found in observability store: {job_id}")
                out.append((job_id, json.loads(row[0])))
            return out
        if not since:
            raise SystemExit("provide --job or --since")
        # A single job_id can own several runs (planning, retries, sub-runs);
        # only the runs that actually reached the certified final-audio stage
        # carry an `audio_publish` observation. Keep the strongest one per job:
        # comparing every auxiliary row would dilute the verdict with zeroes.
        rows = con.execute(
            "SELECT job_id, report_json FROM run_observability"
            " WHERE created_at >= ? AND report_json LIKE ? ORDER BY created_at",
            (since, f'%"{STAGE}"%'),
        ).fetchall()
        return [(r[0], json.loads(r[1])) for r in rows]
    finally:
        con.close()


# --------------------------------------------------------------------------- #
# self-test (fixtures only, no store)
# --------------------------------------------------------------------------- #
def _fixture(pre: bool) -> dict:
    """Build a ReportReport-ish fixture mirroring the real pre/post-gate shapes."""
    stage = {
        "name": STAGE,
        "started_at": "2026-09-28T19:58:48.052850969Z",
        "finished_at": "2026-09-28T19:58:54.352315315Z",
        "duration_ms": 85_741 if pre else 6_299,
    }
    waits = []
    if not pre:
        waits.append(
            {
                "kind": "semaphore_wait",
                "component": GATE_COMPONENT,
                "started_at": "2026-09-28T19:58:48.053151614Z",
                "finished_at": "2026-09-28T19:58:48.053155908Z",
                "duration_ms": 0,
            }
        )
    return {"status": "SUCCEEDED", "stages": [stage], "operations": [], "waits": waits}


def self_test() -> int:
    pre = evaluate_run("pre-gate", _fixture(pre=True), DEFAULT_THRESHOLD_MS)
    post = evaluate_run("post-gate", _fixture(pre=False), DEFAULT_THRESHOLD_MS)
    problems: list[str] = []
    if pre["pass"] or pre["gate_engaged"]:
        problems.append("pre-gate fixture must fail and show no drive wait")
    if not post["pass"]:
        problems.append("post-gate fixture must pass (gate engaged + under threshold)")
    # A drive wait OUTSIDE the window must not count.
    shifted = _fixture(pre=False)
    shifted["stages"][0]["started_at"] = "2026-09-28T19:58:00.000000Z"
    shifted["stages"][0]["finished_at"] = "2026-09-28T19:58:05.000000Z"
    outside = evaluate_run("outside", shifted, DEFAULT_THRESHOLD_MS)
    if outside["drive_waits_inside_audio_publish"] != 0:
        problems.append("drive wait outside the window must not be counted")
    if problems:
        for p in problems:
            print(f"self-test FAIL: {p}", file=sys.stderr)
        return 1
    print("self-test OK: pre-gate fails (0 waits), post-gate passes (gate engaged), window filtering works")
    return 0


def main() -> int:
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("--db", default=DEFAULT_DB, help=f"observability sqlite path (default {DEFAULT_DB})")
    ap.add_argument("--job", action="append", dest="jobs", help="job id (repeatable)")
    ap.add_argument("--since", help="evaluate script runs created at/after this RFC3339 instant")
    ap.add_argument("--threshold-ms", type=int, default=DEFAULT_THRESHOLD_MS)
    ap.add_argument("--json", dest="json_out", help="write the full evidence bundle here")
    ap.add_argument("--self-test", action="store_true")
    args = ap.parse_args()

    if args.self_test:
        return self_test()
    if not args.jobs and not args.since:
        ap.print_usage()
        return 2

    runs = load_runs(args.db, args.jobs, args.since)
    results = [evaluate_run(job_id, report, args.threshold_ms) for job_id, report in runs]
    if args.since and not args.jobs:
        # Collapse auxiliary/duplicate rows of the same job onto the run with the
        # longest audio_publish window (the one the criterion is about).
        best: dict[str, dict] = {}
        for r in results:
            prev = best.get(r["job_id"])
            if prev is None or r["audio_publish_stage_ms"] > prev["audio_publish_stage_ms"]:
                best[r["job_id"]] = r
        results = [best[k] for k in sorted(best)]
    if not results:
        print("no runs matched", file=sys.stderr)
        return 1

    failed = [r for r in results if not r["pass"]]
    for r in results:
        flag = "PASS" if r["pass"] else "FAIL"
        print(
            f"[{flag}] {r['job_id']} audio_publish={r['audio_publish_stage_ms']}ms "
            f"work={r['audio_publish_operation_work_ms']}ms "
            f"drive_waits={r['drive_waits_inside_audio_publish']} "
            f"(threshold {r['threshold_ms']}ms)"
        )

    bundle = {
        "tool": "audio_publish_gate_evidence.py",
        "threshold_ms": args.threshold_ms,
        "evaluated": len(results),
        "passed": len(results) - len(failed),
        "failed": len(failed),
        "worst_audio_publish_ms": max(r["audio_publish_stage_ms"] for r in results),
        "all_pass": not failed,
        "results": results,
    }
    if args.json_out:
        with open(args.json_out, "w", encoding="utf-8") as fh:
            json.dump(bundle, fh, indent=2)
        print(f"wrote {args.json_out}")
    print(
        f"verdict: {bundle['passed']}/{bundle['evaluated']} under {args.threshold_ms}ms with the gate engaged"
        f" (worst {bundle['worst_audio_publish_ms']}ms)"
    )
    return 0 if not failed else 1


if __name__ == "__main__":
    raise SystemExit(main())
