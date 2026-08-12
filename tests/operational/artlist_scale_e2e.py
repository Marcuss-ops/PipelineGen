#!/usr/bin/env python3
"""Stable public façade for the Artlist/VidRush scale battery.

The shell wrapper and fail-closed entrypoint import ``ScaleRunner`` and
``Settings`` from this module. Configuration, transport, workflow,
validation, execution and reporting live in sibling modules; this file keeps
only the compatibility façade and runner method hooks.
"""

from __future__ import annotations

import threading
import sys
from typing import Any

from artlist_scale_config import (
    DEFAULT_KEYWORDS,
    DIAGNOSTIC_PROBES,
    SUCCESS_JOB_STATUSES,
    TERMINAL_JOB_STATUSES,
    Settings,
    chunks,
    env_bool,
    env_float,
    env_int,
    utc_now,
)
from artlist_scale_execution import execute as execute_runner
from artlist_scale_http import HttpClient
from artlist_scale_reporting import (
    diagnostics_ok as diagnostics_check,
    log as report_log,
    record_failure,
    write_json as persist_json,
)
from artlist_scale_validation import (
    audit_count,
    identity_snapshot,
    load_assets,
    replay,
    run_admin,
    validate_assets,
    validate_drive,
    validate_qdrant,
    validate_vlm,
)
from artlist_scale_workflow import (
    health_sample,
    phase_items,
    poll_job,
    preflight,
    run_phase,
    start_health_monitor,
    stop_health_monitor,
    submit_run,
    validate_settings,
    warmup,
)


__all__ = [
    "DEFAULT_KEYWORDS",
    "DIAGNOSTIC_PROBES",
    "SUCCESS_JOB_STATUSES",
    "TERMINAL_JOB_STATUSES",
    "Settings",
    "ScaleRunner",
    "HttpClient",
    "chunks",
    "env_bool",
    "env_float",
    "env_int",
    "utc_now",
    "audit_count",
    "identity_snapshot",
    "load_assets",
    "replay",
    "run_admin",
    "validate_assets",
    "validate_drive",
    "validate_qdrant",
    "validate_vlm",
    "health_sample",
    "phase_items",
    "poll_job",
    "preflight",
    "run_phase",
    "start_health_monitor",
    "stop_health_monitor",
    "submit_run",
    "validate_settings",
    "warmup",
]


class ScaleRunner:
    """Stable stateful façade over the split Artlist scale workflow."""

    def __init__(self, settings: Settings) -> None:
        self.s = settings
        self.http = HttpClient(settings)
        self.failures: list[str] = []
        self.health_samples: list[dict[str, Any]] = []
        self.stop_health = threading.Event()
        self.health_thread: threading.Thread | None = None
        self.phase_results: dict[str, list[dict[str, Any]]] = {}
        self.report: dict[str, Any] = {}
        self.s.report_dir.mkdir(parents=True, exist_ok=True)

    def log(self, message: str) -> None:
        report_log(message)

    def fail(self, message: str) -> None:
        record_failure(self, message)

    def write_json(self, name: str, value: Any) -> None:
        persist_json(self, name, value)

    @staticmethod
    def diagnostics_ok(payload: Any) -> tuple[bool, list[str]]:
        return diagnostics_check(payload, DIAGNOSTIC_PROBES)

    def validate_settings(self) -> None:
        validate_settings(self)

    def preflight(self) -> None:
        preflight(self)

    def warmup(self) -> None:
        warmup(self)

    def health_sample(self) -> dict[str, Any]:
        return health_sample(self)

    def health_loop(self) -> None:
        while not self.stop_health.is_set():
            self.health_samples.append(self.health_sample())
            self.stop_health.wait(self.s.health_interval)

    def start_health_monitor(self) -> None:
        start_health_monitor(self)

    def stop_health_monitor(self) -> None:
        stop_health_monitor(self)

    def audit_count(self) -> int:
        return audit_count(self)

    def submit_run(self, term: str, limit: int) -> str:
        return submit_run(self, term, limit)

    def poll_job(self, phase: str, index: int, term: str, run_id: str) -> dict[str, Any]:
        return poll_job(self, phase, index, term, run_id)

    def run_phase(self, phase: str, *, limit: int | None = None, terms: list[str] | None = None) -> list[dict[str, Any]]:
        return run_phase(self, phase, limit=limit, terms=terms)

    def phase_items(self, phase: str, expected_per_job: int) -> list[dict[str, Any]]:
        return phase_items(self, phase, expected_per_job)

    def load_assets(self, target_ids: list[str]) -> list[dict[str, Any]]:
        return load_assets(self, target_ids)

    def validate_assets(self, target_ids: list[str]) -> list[dict[str, Any]]:
        return validate_assets(self, target_ids)

    def validate_drive(self, assets: list[dict[str, Any]]) -> None:
        validate_drive(self, assets)

    def run_admin(self, args: list[str], output_name: str) -> None:
        run_admin(self, args, output_name)

    def validate_vlm(self, target_ids: list[str]) -> dict[str, Any]:
        return validate_vlm(self, target_ids)

    def validate_qdrant(self, target_ids: list[str]) -> dict[str, Any]:
        return validate_qdrant(self, target_ids)

    @staticmethod
    def identity_snapshot(assets: list[dict[str, Any]]) -> dict[str, dict[str, str]]:
        return identity_snapshot(assets)

    def replay(self, target_ids: list[str], identity_before: dict[str, dict[str, str]]) -> dict[str, Any]:
        return replay(self, target_ids, identity_before)

    def execute(self) -> int:
        return execute_runner(self)


def main() -> int:
    try:
        settings = Settings.load()
    except Exception as exc:  # noqa: BLE001
        print(f"configuration error: {exc}", file=sys.stderr)
        return 2
    return ScaleRunner(settings).execute()


if __name__ == "__main__":
    raise SystemExit(main())
