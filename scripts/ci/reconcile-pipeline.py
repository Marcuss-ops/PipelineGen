#!/usr/bin/env python3
"""Run the canonical PipelineGen reconciliation gates as one operation.

The script deliberately composes existing commands.  It does not write Drive,
SQLite, or Qdrant directly and it never treats a disabled backend as success.
The default is a dry-run; pass ``--apply`` only when the operator wants the
existing reconciler repair paths to enqueue/apply their idempotent repairs.
"""

from __future__ import annotations

import argparse
import json
import os
import re
import subprocess
import sys
import time
from pathlib import Path
from typing import Iterable


ROOT = Path(__file__).parents[2]
REDACTION = re.compile(r"(?i)(VELOX_(?:ADMIN|WORKER)_TOKEN\s*(?:=|:)|Bearer\s+)([^\s,;]+)")


def redact(value: str) -> str:
    return REDACTION.sub(lambda match: f"{match.group(1)}REDACTED", value)


def run_phase(name: str, argv: list[str], timeout: int) -> dict[str, object]:
    started = time.monotonic()
    try:
        completed = subprocess.run(
            argv,
            cwd=ROOT,
            env=os.environ.copy(),
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
            text=True,
            timeout=timeout,
            check=False,
        )
    except subprocess.TimeoutExpired as exc:
        return {
            "name": name,
            "status": "TIMEOUT",
            "exit_code": None,
            "duration_ms": int((time.monotonic() - started) * 1000),
            "detail": redact(str(exc)),
        }
    detail = redact((completed.stderr or completed.stdout or "").strip())
    return {
        "name": name,
        "status": "PASS" if completed.returncode == 0 else "FAIL",
        "exit_code": completed.returncode,
        "duration_ms": int((time.monotonic() - started) * 1000),
        "detail": detail[-1000:] if detail else "",
    }


def parse_args(argv: Iterable[str] | None = None) -> argparse.Namespace:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--apply", action="store_true", help="apply canonical reconciler repairs")
    parser.add_argument("--timeout", type=int, default=900)
    parser.add_argument("--report", type=Path, default=Path("artifacts/reconcile/pipeline.json"))
    return parser.parse_args(list(argv) if argv is not None else None)


def main(argv: Iterable[str] | None = None) -> int:
    args = parse_args(argv)
    if args.timeout <= 0:
        print("reconcile-pipeline: --timeout must be positive", file=sys.stderr)
        return 2

    apply_args = ["--apply"] if args.apply else []
    phases = [
        run_phase(
            "component-coverage",
            ["python3", "scripts/ci/verify-component-coverage.py"],
            min(args.timeout, 300),
        ),
        run_phase(
            "reconciliation-contracts",
            [
                "go",
                "test",
                "./internal/application/qdrant/reconciler",
                "./internal/application/scripts/adapters",
                "./internal/application/assets/deletion/reconciler",
                "./internal/application/jobs/finalizer",
                "./internal/platform/drive",
                "./internal/platform/sqlite/outboxevents",
                "./internal/application/assets/providers/stock/enrichment",
                "./internal/application/assets/providers/stock/stockpipeline",
                "./internal/application/jobs/completion",
                "./internal/application/jobs/outbox",
            ],
            args.timeout,
        ),
        run_phase(
            "qdrant",
            ["go", "run", "./cmd/admin", "reconcile-qdrant", "--json", *apply_args],
            args.timeout,
        ),
        run_phase(
            "drive",
            ["go", "run", "./cmd/admin", "drive-reconcile", *apply_args, "--sync-assets"]
            if args.apply
            else ["go", "run", "./cmd/admin", "drive-reconcile"],
            args.timeout,
        ),
    ]
    report = {
        "mode": "apply" if args.apply else "dry-run",
        "phases": phases,
        "final": "PASS" if all(phase["status"] == "PASS" for phase in phases) else "FAIL",
    }
    report_path = args.report if args.report.is_absolute() else ROOT / args.report
    report_path.parent.mkdir(parents=True, exist_ok=True)
    report_path.write_text(json.dumps(report, indent=2, ensure_ascii=False) + "\n", encoding="utf-8")
    print(
        f"reconcile-pipeline mode={report['mode']} "
        f"phases={len(phases)} final={report['final']} report={report_path}"
    )
    for phase in phases:
        if phase["status"] != "PASS":
            print(f"{phase['name']}: {phase['status']}: {phase['detail']}", file=sys.stderr)
    return 0 if report["final"] == "PASS" else 1


if __name__ == "__main__":
    raise SystemExit(main())
