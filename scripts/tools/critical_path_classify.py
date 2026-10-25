#!/usr/bin/env python3
"""critical_path_classify.py — P3 gate 3 (TICKET-RENDER-AHEAD-SPECULATIVE).

Classifies every wall-clock second of a run's trace into exactly one bucket:

  work_useful      — an operation is doing real work (its started..finished
                     window minus its queue wait)
  queue            — the wall is inside an operation's queue_wait window
  polling_sleep    — render-attempt polling sleeps (render_attempt_analytics
                     polling_sleep_ms before the attempt starts)
  serialization    — no active operation, but a stage barrier is holding the
                     wall (gap between stage/operation activity)
  upload           — an upload-class operation is active (drive_publish,
                     objectstore upload, media materialize/upload)
  repeated         — a retry of work already counted (attempts > 1)

Every second of [run_start, run_finish] receives one label; the report sums
the labels and writes a JSON evidence file (schema
pipelinegen.critical-path-classification.v1).

Usage:
  critical_path_classify.py --run-id run_... [--db PATH] [--out report.json]
  critical_path_classify.py --job-id job_...  (resolves the run id via the
                                              golden summary or job full json)
"""
import argparse
import json
import os
import sqlite3
from datetime import datetime, timezone

DEFAULT_OBS_DB = os.path.join(
    os.path.dirname(__file__), "..", "..", "data", "observability", "api_requests.db.sqlite")
DEFAULT_MEDIA_DB = os.path.join(
    os.path.dirname(__file__), "..", "..", "data", "media", "media.db.sqlite")

UPLOAD_OPS = ("drive_publish", "upload", "objectstore_upload", "publish", "materialize")


def parse_ts(value):
    if not value:
        return None
    text = value.replace("Z", "+00:00")
    # SQLite stores nanosecond-precision timestamps; Python 3.10's
    # fromisoformat accepts at most six fractional digits.
    head, sep, tail = text.partition(".")
    if not sep:
        return datetime.fromisoformat(text)
    frac, sign, zone = tail.partition("+")
    frac = (frac + "000000")[:6]
    return datetime.fromisoformat(f"{head}.{frac}{sign}{zone}")


def classify_run(obs_db, media_db, run_id):
    con = sqlite3.connect(obs_db)
    con.row_factory = sqlite3.Row

    stages = con.execute(
        "SELECT name, started_at, finished_at, duration_ms, attempts FROM "
        "run_stage_observations WHERE run_id = ? ORDER BY started_at",
        (run_id,)).fetchall()
    if not stages:
        raise SystemExit(f"no run_stage_observations for run_id={run_id}")

    ops = con.execute(
        "SELECT stage, component, operation, status, duration_ms, attempts, "
        "queue_wait_ms, started_at, finished_at, queued_at FROM "
        "run_operation_observations WHERE run_id = ? ORDER BY started_at",
        (run_id,)).fetchall()

    run_start = parse_ts(stages[0]["started_at"])
    run_finish = max(parse_ts(s["finished_at"]) or run_start for s in stages)
    total_seconds = int((run_finish - run_start).total_seconds()) + 1

    labels = [None] * total_seconds  # one label per wall second

    def mark(start, end, label, priority):
        if start is None or end is None or end <= start:
            return
        i0 = max(0, int((start - run_start).total_seconds()))
        i1 = min(total_seconds, int((end - run_start).total_seconds()) + 1)
        for i in range(i0, i1):
            cur = labels[i]
            if cur is None or priority < cur[1]:
                labels[i] = (label, priority)

    # Priority order: repeated (0) > upload (1) > work_useful (2) > queue (3) >
    # polling_sleep (4) > serialization (5).
    for op in ops:
        op_name = (op["operation"] or "") + " " + (op["component"] or "")
        is_upload = any(u in op_name.lower() for u in UPLOAD_OPS)
        attempts = op["attempts"] or 1
        started, finished = parse_ts(op["started_at"]), parse_ts(op["finished_at"])
        label = "upload" if is_upload else "work_useful"
        if attempts > 1:
            mark(started, finished, "repeated", 0)
        else:
            mark(started, finished, label, 1 if is_upload else 2)
        # queue window: queued_at..started_at (or queue_wait_ms before start)
        queued = parse_ts(op["queued_at"])
        if queued and started and queued < started:
            mark(queued, started, "queue", 3)

    # polling sleeps from render attempts (media DB) if available. The
    # analytics store a different run-id projection (UUID), so match rows by
    # their submission window inside this run's wall span.
    if os.path.exists(media_db):
        mcon = sqlite3.connect(media_db)
        try:
            rows = mcon.execute(
                "SELECT submit_started_at, wait_started_at, queue_started_at, "
                "polling_sleep_ms, completion_wait_ms FROM "
                "render_attempt_analytics WHERE submit_started_at >= ? AND "
                "submit_started_at <= ?",
                (run_start.isoformat(), run_finish.isoformat())).fetchall()
            for submit, wait_start, qstart, sleep_ms, completion_ms in rows:
                s0 = parse_ts(submit)
                s1 = parse_ts(qstart) or parse_ts(wait_start)
                if s0 and s1 and s1 > s0:
                    mark(s0, s1, "polling_sleep", 4)
        except sqlite3.Error:
            pass
        mcon.close()

    # whatever remains while the run is nominally active is barrier/idle time
    for i in range(total_seconds):
        if labels[i] is None:
            labels[i] = ("serialization", 5)

    counts = {}
    for entry in labels:
        counts[entry[0]] = counts.get(entry[0], 0) + 1

    report = {
        "schema": "pipelinegen.critical-path-classification.v1",
        "run_id": run_id,
        "run_start": run_start.isoformat(),
        "run_finish": run_finish.isoformat(),
        "total_seconds": total_seconds,
        "classification": counts,
        "share": {k: round(v / total_seconds, 4) for k, v in counts.items()},
        "stages": [{"name": s["name"], "duration_ms": s["duration_ms"],
                    "attempts": s["attempts"]} for s in stages],
    }
    con.close()
    return report


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--run-id", required=True)
    ap.add_argument("--db", default=os.path.abspath(DEFAULT_OBS_DB))
    ap.add_argument("--media-db", default=os.path.abspath(DEFAULT_MEDIA_DB))
    ap.add_argument("--out")
    args = ap.parse_args()

    report = classify_run(args.db, args.media_db, args.run_id)
    text = json.dumps(report, indent=1)
    if args.out:
        with open(args.out, "w") as fh:
            fh.write(text + "\n")
        print(f"critical-path classification written: {args.out}")
    print(text)


if __name__ == "__main__":
    main()
