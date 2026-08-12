#!/usr/bin/env python3
"""Quota-expensive Artlist/VidRush scale, indexing and replay battery.

The shell wrapper and fail-closed entrypoint import ``ScaleRunner`` and
``Settings`` from this module. Transport, configuration, workflow, and
persistence checks are split into sibling modules so this file remains the
stable compatibility surface.
"""

from __future__ import annotations

import json
import sys
import threading
import time
from datetime import datetime
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
from artlist_scale_http import HttpClient
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
        print(f"[{datetime.now().strftime('%H:%M:%S')}] {message}", flush=True)

    def fail(self, message: str) -> None:
        self.failures.append(message)
        self.log(f"FAIL: {message}")

    def write_json(self, name: str, value: Any) -> None:
        path = self.s.report_dir / name
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text(json.dumps(value, indent=2, ensure_ascii=False, default=str) + "\n", encoding="utf-8")

    @staticmethod
    def diagnostics_ok(payload: Any) -> tuple[bool, list[str]]:
        if not isinstance(payload, dict):
            return False, ["invalid diagnostics response"]
        failed = [name for name in DIAGNOSTIC_PROBES if not isinstance(payload.get(name), dict) or payload[name].get("ok") is not True]
        return not failed, failed

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
        started = time.monotonic()
        first_audit_before = 0
        first_audit_after = 0
        target_ids: list[str] = []
        vlm_report: dict[str, Any] = {}
        qdrant_report: dict[str, Any] = {}
        replay_report: dict[str, Any] = {}
        try:
            self.validate_settings()
            self.preflight()
            self.warmup()
            self.start_health_monitor()
            first_audit_before = self.audit_count()
            self.run_phase("first")
            first_items = self.phase_items("first", self.s.clips_per_keyword)
            first_audit_after = self.audit_count()
            target_ids = sorted({str(item.get("clip_id", "")).strip() for item in first_items if item.get("clip_id")})
            if not target_ids:
                raise RuntimeError("first phase produced no target clip IDs")
            assets = self.validate_assets(target_ids)
            self.validate_drive(assets)
            identity_before = self.identity_snapshot(assets)
            self.write_json("sqlite/identity_before.json", identity_before)
            vlm_report = self.validate_vlm(target_ids)
            qdrant_report = self.validate_qdrant(target_ids)
            replay_report = self.replay(target_ids, identity_before)
        except Exception as exc:  # noqa: BLE001 - final operational envelope
            self.fail(str(exc))
        finally:
            if self.health_thread is not None:
                self.stop_health_monitor()

        elapsed_ms = round((time.monotonic() - started) * 1000)
        first_items_count = sum(len(result.get("job", {}).get("result", {}).get("items", [])) if isinstance(result.get("job", {}).get("result", {}), dict) else 0 for result in self.phase_results.get("first", []))
        assets_per_minute = round(first_items_count / (elapsed_ms / 60000), 3) if elapsed_ms > 0 else 0.0
        if assets_per_minute < self.s.min_assets_per_minute:
            self.fail(f"throughput {assets_per_minute} assets/min is below minimum {self.s.min_assets_per_minute}")

        summary = {
            "ok": not self.failures,
            "report_dir": str(self.s.report_dir),
            "matrix": {"keywords": len(self.s.keywords), "clips_per_keyword": self.s.clips_per_keyword, "requested_items": len(self.s.keywords) * self.s.clips_per_keyword},
            "results": {"returned_items": first_items_count, "unique_assets": len(target_ids)},
            "performance": {"clip_concurrency": self.s.clip_concurrency, "elapsed_ms": elapsed_ms, "assets_per_minute": assets_per_minute},
            "availability": {"samples": len(self.health_samples), "unhealthy_samples": sum(1 for sample in self.health_samples if not (sample.get("pipeline_ready") and sample.get("scraper_healthy") and sample.get("diagnostics_ok")))},
            "dedup": {"first_download_audit_delta": first_audit_after - first_audit_before, **replay_report},
            "vlm": vlm_report,
            "qdrant": qdrant_report,
            "failures": self.failures,
        }
        self.report = summary
        self.write_json("summary.json", summary)
        print(json.dumps(summary, indent=2, ensure_ascii=False), flush=True)
        if self.failures:
            self.log("FAILURES:")
            for failure in self.failures:
                print(f"  - {failure}", flush=True)
            return 1
        self.log("PASS: Artlist scale, Drive, VLM/Qdrant and replay dedup checks succeeded")
        return 0


def main() -> int:
    try:
        settings = Settings.load()
    except Exception as exc:  # noqa: BLE001
        print(f"configuration error: {exc}", file=sys.stderr)
        return 2
    return ScaleRunner(settings).execute()


if __name__ == "__main__":
    raise SystemExit(main())
