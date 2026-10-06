#!/usr/bin/env python3
"""Durable job-timing snapshot.

Answers "dove perdiamo tempo?" from the authoritative stores instead of from
per-run result JSONs that only exist for a handful of rehearsals:

  * ``data/jobs/jobs.db.sqlite``            -- jobs, job_steps, job_registry_metrics
  * ``data/media/media.db.sqlite``          -- performance_runs / performance_steps
  * ``data/observability/api_requests.db``  -- api_requests (HTTP latency)

Both stores are read through ``file:...?mode=ro`` URIs, so this tool can never
mutate them. It writes ONE versioned JSON artifact, which is the point: the
``jobs`` tables are periodically reset (they currently span about a week),
while ``performance_runs`` reaches back further -- so the aggregate has to be
persisted somewhere durable or the history is gone with the next reset.

Usage:

    python3 ops/jobs/timing_snapshot.py
    python3 ops/jobs/timing_snapshot.py --out ops/benchmarks/job-timing.json
    python3 ops/jobs/timing_snapshot.py --refactored-root /path/to/refactored

Schema: ``job-timing-snapshot/v1``.

The ``run_observability`` / ``run_stage_observations`` /
``run_operation_observations`` tables in the observability database are the
preferred source: they carry the queue/active/blocked split as real columns
and reach further back than the rolling ``jobs`` tables.
"""

from __future__ import annotations

import argparse
import datetime as dt
import json
import os
import sqlite3
import sys
from pathlib import Path

SCHEMA = "job-timing-snapshot/v1"

# Metric names carried by job_registry_metrics that describe a wait rather than
# work. They are the reason the queue/CPU split exists at all.
WAIT_METRICS = ("queue_wait_ms",)
WORK_METRICS = ("wall_time_ms",)

# A ratio alone is not a finding. A fixture job that waits 3 s to run for 0.1 s
# scores a worse amplification than a real 44 h stall, so a finding has to
# clear an absolute materiality floor before it is reported.
MIN_FINDING_QUEUE_HOURS = 1.0

# Scheduled maintenance is queued like any other work, so it scores a huge
# amplification (it waits hours to run for a moment). That is the scheduler
# behaving correctly, not a bottleneck: reporting it buries the real ones.
HOUSEKEEPING_JOB_TYPES = frozenset({"system.cleanup"})

# Waits projected into performance_runs.metadata_json.waits by the completion
# projector. ``outbox_delivery_ms`` is listed even though it is currently never
# populated: a zero that is *declared* is a finding, not an absence of one.
DURABLE_WAITS = ("queue_ms", "blocked_ms", "completion_ms", "outbox_delivery_ms")


def open_ro(path: Path) -> sqlite3.Connection | None:
    """Open ``path`` read-only. A missing database is reported, never created."""
    if not path.is_file():
        return None
    uri = f"file:{path}?mode=ro"
    return sqlite3.connect(uri, uri=True, timeout=10)


def table_names(con: sqlite3.Connection) -> set[str]:
    rows = con.execute("SELECT name FROM sqlite_master WHERE type='table'")
    return {r[0] for r in rows}


def table_count(con: sqlite3.Connection, table: str) -> int | None:
    if table not in table_names(con):
        return None
    return con.execute(f"SELECT COUNT(*) FROM {table}").fetchone()[0]


def scalar(con: sqlite3.Connection, sql: str, default=0.0) -> float:
    row = con.execute(sql).fetchone()
    if row is None or row[0] is None:
        return default
    return float(row[0])


def describe_source(label: str, path: Path, con: sqlite3.Connection | None, tables: list[str]) -> dict:
    src: dict = {
        "label": label,
        "path": str(path),
        "present": con is not None,
        "size_bytes": path.stat().st_size if path.is_file() else 0,
        "tables": {},
    }
    if con is not None:
        for t in tables:
            src["tables"][t] = table_count(con, t)
    return src


def job_type_breakdown(con: sqlite3.Connection) -> list[dict]:
    """Per job type: how much time is queue wait vs real execution work.

    ``job_registry_metrics`` carries both numbers as separate rows per job, so
    the two metrics are pivoted per job first -- averaging them together would
    mix a wait with a duration and produce a meaningless mean.
    """
    sql = """
    WITH m AS (
        SELECT job_id,
               MAX(CASE WHEN metric_name = 'queue_wait_ms' THEN metric_value END) AS queue_ms,
               MAX(CASE WHEN metric_name = 'wall_time_ms'  THEN metric_value END) AS wall_ms
        FROM job_registry_metrics
        GROUP BY job_id
    )
    SELECT j.type,
           COUNT(*)                                   AS jobs,
           COALESCE(SUM(m.queue_ms), 0)               AS queue_ms,
           COALESCE(SUM(m.wall_ms), 0)                AS wall_ms,
           COALESCE(AVG(m.queue_ms), 0)               AS avg_queue_ms,
           COALESCE(MAX(m.queue_ms), 0)               AS max_queue_ms
    FROM jobs j
    JOIN m ON m.job_id = j.id
    GROUP BY j.type
    ORDER BY SUM(COALESCE(m.queue_ms, 0)) DESC
    """
    out = []
    for jtype, jobs, queue_ms, wall_ms, avg_queue, max_queue in con.execute(sql):
        queue_h = queue_ms / 3_600_000.0
        wall_h = wall_ms / 3_600_000.0
        out.append(
            {
                "job_type": jtype,
                "jobs": jobs,
                "queue_hours": round(queue_h, 2),
                "work_hours": round(wall_h, 2),
                "avg_queue_s": round(avg_queue / 1000.0, 1),
                "max_queue_s": round(max_queue / 1000.0, 1),
                # >1 means the jobs of this type wait longer than they run.
                "wait_amplification": round(queue_h / wall_h, 2) if wall_h > 0 else None,
            }
        )
    return out


def failure_cost(con: sqlite3.Connection) -> list[dict]:
    sql = """
    SELECT type, COUNT(*) AS jobs, COALESCE(SUM(duration_ms), 0) AS wasted_ms
    FROM jobs WHERE status = 'FAILED'
    GROUP BY type ORDER BY SUM(duration_ms) DESC
    """
    return [
        {"job_type": t, "jobs": n, "wasted_hours": round(ms / 3_600_000.0, 2)}
        for t, n, ms in con.execute(sql)
    ]


def step_breakdown(con: sqlite3.Connection) -> list[dict]:
    sql = """
    SELECT step_name, status, COUNT(*) AS n,
           COALESCE(SUM(duration_ms), 0) AS total_ms,
           COALESCE(AVG(duration_ms), 0) AS avg_ms,
           COALESCE(MAX(duration_ms), 0) AS max_ms
    FROM job_steps GROUP BY step_name, status
    ORDER BY SUM(duration_ms) DESC
    """
    return [
        {
            "step": name,
            "status": status,
            "count": n,
            "total_hours": round(total_ms / 3_600_000.0, 3),
            "avg_s": round(avg_ms / 1000.0, 3),
            "max_s": round(max_ms / 1000.0, 1),
        }
        for name, status, n, total_ms, avg_ms, max_ms in con.execute(sql)
    ]


def metric_breakdown(con: sqlite3.Connection) -> list[dict]:
    sql = """
    SELECT metric_name, unit, COUNT(*) AS n,
           SUM(metric_value) AS total, AVG(metric_value) AS avg, MAX(metric_value) AS max
    FROM job_registry_metrics GROUP BY metric_name, unit
    ORDER BY SUM(metric_value) DESC
    """
    return [
        {
            "metric": name,
            "unit": unit or "",
            "count": n,
            "total_hours": round(total / 3_600_000.0, 2) if (unit or "") == "ms" else None,
            "avg": round(avg, 1),
            "max": round(mx, 1),
        }
        for name, unit, n, total, avg, mx in con.execute(sql)
    ]


def durable_registry(con: sqlite3.Connection) -> dict:
    """Aggregate the durable performance registry (outlives the jobs tables)."""
    out: dict = {"runs": 0, "wall_hours": 0.0, "window": {}, "waits": {}, "steps": []}
    if "performance_runs" not in table_names(con):
        return out

    runs = con.execute(
        "SELECT status, COUNT(*), COALESCE(SUM(wall_ms),0) FROM performance_runs GROUP BY status"
    ).fetchall()
    out["runs"] = sum(r[1] for r in runs)
    out["by_status"] = {r[0]: {"runs": r[1], "wall_hours": round(r[2] / 3_600_000.0, 1)} for r in runs}
    out["wall_hours"] = round(sum(r[2] for r in runs) / 3_600_000.0, 1)

    row = con.execute("SELECT MIN(started_at), MAX(started_at) FROM performance_runs").fetchone()
    out["window"] = {"first": row[0], "last": row[1]}

    # The waits live inside metadata_json: parse rather than guess at a schema.
    acc = {k: {"hours": 0.0, "runs_with_value": 0} for k in DURABLE_WAITS}
    for (meta,) in con.execute("SELECT metadata_json FROM performance_runs"):
        try:
            doc = json.loads(meta or "{}")
        except json.JSONDecodeError:
            continue
        waits = doc.get("waits") or {}
        for key in DURABLE_WAITS:
            try:
                ms = float(waits.get(key) or 0)
            except (TypeError, ValueError):
                continue
            if ms > 0:
                acc[key]["hours"] += ms / 3_600_000.0
                acc[key]["runs_with_value"] += 1
    out["waits"] = {
        k: {"hours": round(v["hours"], 1), "runs_with_value": v["runs_with_value"]}
        for k, v in acc.items()
    }

    if "performance_steps" in table_names(con):
        sql = """
        SELECT name, status, COUNT(*), COALESCE(SUM(duration_ms),0), COALESCE(AVG(duration_ms),0)
        FROM performance_steps GROUP BY name, status ORDER BY SUM(duration_ms) DESC
        """
        out["steps"] = [
            {
                "step": name,
                "status": status,
                "count": n,
                "total_hours": round(total / 3_600_000.0, 2),
                "avg_s": round(avg / 1000.0, 2),
            }
            for name, status, n, total, avg in con.execute(sql)
        ]
    return out


# The durable run-observability plane. ``run_observability`` carries the
# queue/active/blocked split as real columns, which makes it the only store
# that answers "where did the elapsed time go" without parsing JSON, and it
# reaches back further than the jobs tables.
RUN_OBSERVABILITY_COLUMNS = (
    "queue_wait_ms",
    "active_ms",
    "blocked_ms",
    "wall_time_ms",
    "accumulated_operation_ms",
)


def observability_runs(con: sqlite3.Connection) -> dict:
    tables = table_names(con)
    out: dict = {"runs": 0, "totals": {}, "by_job_type": [], "degraded_runs": 0}
    if "run_observability" not in tables:
        return out

    row = con.execute("SELECT MIN(created_at), MAX(created_at), COUNT(*) FROM run_observability").fetchone()
    out["window"] = {"first": row[0], "last": row[1]}
    out["runs"] = row[2]

    sums = ", ".join(f"COALESCE(SUM({c}),0)" for c in RUN_OBSERVABILITY_COLUMNS)
    vals = con.execute(f"SELECT {sums}, COALESCE(SUM(observability_degraded),0) FROM run_observability").fetchone()
    out["totals"] = {c: round(v / 3_600_000.0, 1) for c, v in zip(RUN_OBSERVABILITY_COLUMNS, vals)}
    out["totals"] = {k + "_hours": v for k, v in out["totals"].items()}
    out["degraded_runs"] = int(vals[-1])

    cols = ", ".join(f"COALESCE(SUM({c}),0)" for c in RUN_OBSERVABILITY_COLUMNS)
    sql = f"""
    SELECT job_type, COUNT(*), {cols},
           COALESCE(SUM(observability_degraded),0)
    FROM run_observability GROUP BY job_type
    ORDER BY COALESCE(SUM(queue_wait_ms),0) DESC
    """
    for row in con.execute(sql):
        jtype, n = row[0], row[1]
        measured = dict(zip(RUN_OBSERVABILITY_COLUMNS, row[2:2 + len(RUN_OBSERVABILITY_COLUMNS)]))
        entry = {"job_type": jtype, "runs": n, "degraded_runs": int(row[-1])}
        entry.update({k + "_hours": round(v / 3_600_000.0, 1) for k, v in measured.items()})
        wall = measured["wall_time_ms"] / 3_600_000.0
        queue = measured["queue_wait_ms"] / 3_600_000.0
        entry["wait_amplification"] = round(queue / wall, 2) if wall > 0 else None
        out["by_job_type"].append(entry)
    return out


def observability_stages(con: sqlite3.Connection) -> list[dict]:
    if "run_stage_observations" not in table_names(con):
        return []
    sql = """
    SELECT name, status, COUNT(*), COALESCE(SUM(duration_ms),0),
           COALESCE(AVG(duration_ms),0), COALESCE(MAX(duration_ms),0)
    FROM run_stage_observations GROUP BY name, status
    ORDER BY COALESCE(SUM(duration_ms),0) DESC
    """
    return [
        {
            "stage": name,
            "status": status,
            "count": n,
            "total_hours": round(total / 3_600_000.0, 2),
            "avg_s": round(avg / 1000.0, 2),
            "max_s": round(mx / 1000.0, 1),
        }
        for name, status, n, total, avg, mx in con.execute(sql)
    ]


def observability_operations(con: sqlite3.Connection) -> dict:
    """Per-operation cost, including the operation-level queue wait.

    The value is a dict with two views: ``operations`` holds the per
    stage/component/operation rows across ALL stages; ``overlay_render``
    holds that stage's decomposition — within it the boundary operations
    (component ``render_queue``: submit, wait_completion) and the
    worker-reported phases (component ``renderinggen``) are summarized
    separately, so the stage wall splits into submit + GPU-lane wait +
    Σ render work + Σ encode/upload without re-parsing raw rows.
    """
    if "run_operation_observations" not in table_names(con):
        return {"operations": [], "overlay_render": []}
    return {"operations": _operation_rows(con), "overlay_render": overlay_render_decomposition(con)}


def _operation_rows(con: sqlite3.Connection) -> list[dict]:
    """Per stage/component/operation rows across all stages, top 40 by total
    duration, including the operation-level queue wait."""
    sql = """
    SELECT stage, component, operation, status, COUNT(*),
           COALESCE(SUM(duration_ms),0), COALESCE(SUM(queue_wait_ms),0),
           COALESCE(AVG(duration_ms),0), COALESCE(MAX(duration_ms),0),
           COALESCE(SUM(cache_hit),0)
    FROM run_operation_observations GROUP BY stage, component, operation, status
    ORDER BY COALESCE(SUM(duration_ms),0) DESC
    LIMIT 40
    """
    out = []
    for stage, comp, op, status, n, total, queue, avg, mx, hits in con.execute(sql):
        out.append(
            {
                "stage": stage,
                "component": comp,
                "operation": op,
                "status": status,
                "count": n,
                "total_hours": round(total / 3_600_000.0, 2),
                "queue_hours": round(queue / 3_600_000.0, 2),
                "avg_s": round(avg / 1000.0, 3),
                "max_s": round(mx / 1000.0, 1),
                "cache_hits": int(hits),
            }
        )
    return out


def overlay_render_decomposition(con: sqlite3.Connection) -> list[dict]:
    """Sums the overlay_render stage's own operations per component and
    operation, ordered the way the wall decomposes: boundary first (submit,
    wait), then owner-reported work. Dates come from created_at, which the
    recorder now stamps at write time for owner-measured rows; the historical
    undated rows are excluded from the window but reported as a count so the
    gap stays visible.
    """
    out: list[dict] = []
    rows = []
    try:
        rows = con.execute(
            """
            SELECT component, operation, COUNT(*), COALESCE(SUM(duration_ms),0),
                   COALESCE(AVG(duration_ms),0)
            FROM run_operation_observations
            WHERE stage = 'overlay_render' AND created_at != ''
            GROUP BY component, operation
            ORDER BY COALESCE(SUM(duration_ms),0) DESC
            """
        ).fetchall()
        undated = con.execute(
            "SELECT COUNT(*) FROM run_operation_observations WHERE stage = 'overlay_render' AND created_at = ''"
        ).fetchone()[0]
    except sqlite3.Error:
        undated = 0
    for comp, op, n, total, avg in rows:
        out.append(
            {
                "component": comp,
                "operation": op,
                "count": n,
                "total_hours": round(total / 3_600_000.0, 2),
                "avg_s": round(avg / 1000.0, 3),
            }
        )
    if undated:
        out.append({"component": "(historical, undated rows excluded from windows)", "operation": "", "count": undated, "total_hours": None, "avg_s": None})
    return out


def api_requests(con: sqlite3.Connection) -> dict:
    """The HTTP request log. Reported even when empty: a 300 MB database with
    a zero-row request table is itself the finding."""
    out: dict = {"requests": 0, "present": "api_requests" in table_names(con)}
    if not out["present"]:
        return out
    out["requests"] = table_count(con, "api_requests")
    row = con.execute("SELECT MIN(ts), MAX(ts) FROM api_requests").fetchone()
    out["window"] = {"first": row[0], "last": row[1]}
    return out


def derive_findings(job_types: list[dict], durable: dict, failures: list[dict], obs: dict | None = None) -> list[dict]:
    """Rank the measured bottlenecks. Only measured facts, no speculation."""
    findings = []

    # The durable observability plane is preferred when present: it carries the
    # whole month instead of the jobs tables' rolling window.
    obs = obs or {}
    obs_totals = obs.get("totals") or {}
    if obs_totals.get("queue_wait_ms_hours"):
        queue_h = obs_totals["queue_wait_ms_hours"]
        wall_h = obs_totals.get("wall_time_ms_hours", 0.0)
        blocked_h = obs_totals.get("blocked_ms_hours", 0.0)
        findings.append(
            {
                "kind": "queue_dominates",
                "detail": "durable run observability: queue wait exceeds measured wall time",
                "window": obs.get("window"),
                "queue_hours": queue_h,
                "wall_hours": wall_h,
                "blocked_hours": blocked_h,
                "queue_share_pct": round(100.0 * queue_h / (queue_h + wall_h), 1) if (queue_h + wall_h) else None,
            }
        )
        for entry in obs.get("by_job_type") or []:
            amp = entry.get("wait_amplification")
            if entry.get("job_type") in HOUSEKEEPING_JOB_TYPES:
                continue
            if amp and amp >= 3 and entry.get("queue_wait_ms_hours", 0) >= MIN_FINDING_QUEUE_HOURS:
                findings.append(
                    {
                        "kind": "starvation",
                        "detail": f"{entry['job_type']} waits {amp}x its own wall time",
                        "job_type": entry["job_type"],
                        "queue_hours": entry["queue_wait_ms_hours"],
                        "wall_hours": entry.get("wall_time_ms_hours"),
                    }
                )
        if obs.get("degraded_runs"):
            findings.append(
                {
                    "kind": "observability_degraded",
                    "detail": "runs report partial observability",
                    "runs": obs["degraded_runs"],
                }
            )
        if obs_totals.get("active_ms_hours") == 0:
            findings.append(
                {
                    "kind": "instrumentation_gap",
                    "detail": "run_observability.active_ms is never populated",
                    "field": "run_observability.active_ms",
                }
            )

    total_queue = sum(t["queue_hours"] for t in job_types)
    total_work = sum(t["work_hours"] for t in job_types)
    if total_queue + total_work > 0:
        findings.append(
            {
                "kind": "queue_dominates_jobs_table",
                "detail": "jobs tables (rolling window): queue wait exceeds measured execution work",
                "queue_hours": round(total_queue, 1),
                "work_hours": round(total_work, 1),
                "queue_share_pct": round(100.0 * total_queue / (total_queue + total_work), 1),
            }
        )

    for t in job_types:
        amp = t["wait_amplification"]
        if t.get("job_type") in HOUSEKEEPING_JOB_TYPES:
            continue
        if amp is not None and amp >= 3 and t["queue_hours"] >= MIN_FINDING_QUEUE_HOURS:
            findings.append(
                {
                    "kind": "starvation",
                    "detail": f"{t['job_type']} waits {amp}x its own execution time",
                    "job_type": t["job_type"],
                    "queue_hours": t["queue_hours"],
                    "work_hours": t["work_hours"],
                    "avg_queue_s": t["avg_queue_s"],
                }
            )

    worst = max(job_types, key=lambda t: t["max_queue_s"], default=None)
    if worst and worst["max_queue_s"] > 1800:
        findings.append(
            {
                "kind": "head_of_line_blocking",
                "detail": f"{worst['job_type']} sat in queue for {worst['max_queue_s']}s",
                "job_type": worst["job_type"],
                "max_queue_s": worst["max_queue_s"],
            }
        )

    wasted = sum(f["wasted_hours"] for f in failures)
    if wasted > 0:
        findings.append(
            {
                "kind": "failure_cost",
                "detail": "terminal failures burned measured execution time",
                "wasted_hours": round(wasted, 1),
                "by_type": failures,
            }
        )

    for key, val in (durable.get("waits") or {}).items():
        if key == "outbox_delivery_ms" and val["hours"] == 0 and val["runs_with_value"] == 0:
            findings.append(
                {
                    "kind": "instrumentation_gap",
                    "detail": "declared wait is never populated in the durable registry",
                    "field": f"performance_runs.metadata_json.waits.{key}",
                    "runs_with_value": 0,
                }
            )

    dq = (durable.get("waits") or {}).get("queue_ms", {}).get("hours", 0)
    if dq and durable.get("wall_hours") and dq > durable["wall_hours"]:
        findings.append(
            {
                "kind": "queue_dominates_durable",
                "detail": "durable registry agrees: aggregated queue wait exceeds aggregated wall",
                "queue_hours": dq,
                "wall_hours": durable["wall_hours"],
            }
        )
    return findings


def build(refactored: Path) -> dict:
    jobs_db = refactored / "data" / "jobs" / "jobs.db.sqlite"
    media_db = refactored / "data" / "media" / "media.db.sqlite"
    obs_db = refactored / "data" / "observability" / "api_requests.db.sqlite"

    con_jobs = open_ro(jobs_db)
    con_media = open_ro(media_db)
    con_obs = open_ro(obs_db)

    snap: dict = {
        "schema": SCHEMA,
        "generated_at": dt.datetime.now(dt.timezone.utc).replace(microsecond=0).isoformat().replace("+00:00", "Z"),
        "refactored_root": str(refactored),
        "sources": [
            describe_source("jobs", jobs_db, con_jobs, ["jobs", "job_steps", "job_registry_metrics", "job_events"]),
            describe_source("media", media_db, con_media, ["performance_runs", "performance_steps", "performance_operations"]),
            describe_source(
                "observability",
                obs_db,
                con_obs,
                [
                    "run_observability",
                    "run_stage_observations",
                    "run_operation_observations",
                    "job_attempts",
                    "api_requests",
                ],
            ),
        ],
    }

    if con_jobs is not None:
        row = con_jobs.execute("SELECT MIN(created_at), MAX(created_at) FROM jobs").fetchone()
        snap["jobs_window"] = {"first": row[0], "last": row[1]}
        snap["job_types"] = job_type_breakdown(con_jobs)
        snap["steps"] = step_breakdown(con_jobs)
        snap["metrics"] = metric_breakdown(con_jobs)
        snap["failures"] = failure_cost(con_jobs)
    else:
        snap["job_types"], snap["steps"], snap["metrics"], snap["failures"] = [], [], [], []

    snap["durable_registry"] = durable_registry(con_media) if con_media is not None else {}
    if con_obs is not None:
        snap["run_observability"] = observability_runs(con_obs)
        snap["stage_observations"] = observability_stages(con_obs)
        snap["operation_observations"] = observability_operations(con_obs)
        snap["api_requests"] = api_requests(con_obs)
    else:
        snap["run_observability"] = {}
        snap["stage_observations"] = []
        snap["operation_observations"] = {}
        snap["api_requests"] = {"requests": 0, "present": False}

    snap["findings"] = derive_findings(
        snap["job_types"], snap["durable_registry"], snap["failures"], snap["run_observability"]
    )

    for con in (con_jobs, con_media, con_obs):
        if con is not None:
            con.close()
    return snap


def default_out(refactored: Path) -> Path:
    stamp = dt.datetime.now(dt.timezone.utc).strftime("%Y%m%dT%H%M%SZ")
    return refactored / "ops" / "benchmarks" / f"job-timing-{stamp}.json"


def main(argv: list[str]) -> int:
    here = Path(__file__).resolve().parent
    parser = argparse.ArgumentParser(description="Durable job-timing snapshot (read-only).")
    parser.add_argument("--refactored-root", default=str(here.parent.parent),
                        help="refactored/ root holding data/ (default: two levels above this script)")
    parser.add_argument("--out", default=None, help="output JSON path (default: ops/benchmarks/job-timing-<ts>.json)")
    parser.add_argument("--stdout", action="store_true", help="print the snapshot instead of only writing it")
    args = parser.parse_args(argv)

    refactored = Path(args.refactored_root).resolve()
    if not (refactored / "data").is_dir():
        print(f"error: {refactored}/data not found; pass --refactored-root", file=sys.stderr)
        return 2

    snap = build(refactored)
    out = Path(args.out).resolve() if args.out else default_out(refactored)
    out.parent.mkdir(parents=True, exist_ok=True)
    out.write_text(json.dumps(snap, indent=2, ensure_ascii=False) + "\n", encoding="utf-8")

    if args.stdout:
        json.dump(snap, sys.stdout, indent=2, ensure_ascii=False)
        sys.stdout.write(os.linesep)

    total_queue = sum(t["queue_hours"] for t in snap["job_types"])
    total_work = sum(t["work_hours"] for t in snap["job_types"])
    obs_totals = (snap.get("run_observability") or {}).get("totals") or {}
    print(f"wrote {out}")
    print(f"  jobs window      : {snap.get('jobs_window', {}).get('first')} -> {snap.get('jobs_window', {}).get('last')}")
    print(f"  jobs queue/work  : {total_queue:.1f} h vs {total_work:.1f} h")
    print(f"  durable runs     : {snap['durable_registry'].get('runs', 0)} ({snap['durable_registry'].get('wall_hours', 0)} h wall)")
    if obs_totals:
        print(
            "  run observability: "
            f"queue {obs_totals.get('queue_wait_ms_hours', 0)} h | "
            f"wall {obs_totals.get('wall_time_ms_hours', 0)} h | "
            f"blocked {obs_totals.get('blocked_ms_hours', 0)} h | "
            f"ops {obs_totals.get('accumulated_operation_ms_hours', 0)} h"
        )
    print(f"  findings         : {len(snap['findings'])}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main(sys.argv[1:]))
