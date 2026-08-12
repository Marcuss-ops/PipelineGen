from __future__ import annotations

import json
from datetime import datetime
from typing import Any

from artlist_scale_config import DIAGNOSTIC_PROBES


def log(message: str) -> None:
    print(f"[{datetime.now().strftime('%H:%M:%S')}] {message}", flush=True)


def record_failure(runner: Any, message: str) -> None:
    runner.failures.append(message)
    runner.log(f"FAIL: {message}")


def write_json(runner: Any, name: str, value: Any) -> None:
    path = runner.s.report_dir / name
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(
        json.dumps(value, indent=2, ensure_ascii=False, default=str) + "\n",
        encoding="utf-8",
    )


def diagnostics_ok(
    payload: Any,
    probes: tuple[str, ...] = DIAGNOSTIC_PROBES,
) -> tuple[bool, list[str]]:
    if not isinstance(payload, dict):
        return False, ["invalid diagnostics response"]
    failed = [
        name
        for name in probes
        if not isinstance(payload.get(name), dict)
        or payload[name].get("ok") is not True
    ]
    return not failed, failed


def build_summary(
    runner: Any,
    *,
    elapsed_ms: int,
    first_audit_before: int,
    first_audit_after: int,
    target_ids: list[str],
    replay_report: dict[str, Any],
    vlm_report: dict[str, Any],
    qdrant_report: dict[str, Any],
    first_items_count: int,
    assets_per_minute: float,
) -> dict[str, Any]:
    return {
        "ok": not runner.failures,
        "report_dir": str(runner.s.report_dir),
        "matrix": {
            "keywords": len(runner.s.keywords),
            "clips_per_keyword": runner.s.clips_per_keyword,
            "requested_items": len(runner.s.keywords) * runner.s.clips_per_keyword,
        },
        "results": {
            "returned_items": first_items_count,
            "unique_assets": len(target_ids),
        },
        "performance": {
            "clip_concurrency": runner.s.clip_concurrency,
            "elapsed_ms": elapsed_ms,
            "assets_per_minute": assets_per_minute,
        },
        "availability": {
            "samples": len(runner.health_samples),
            "unhealthy_samples": sum(
                1
                for sample in runner.health_samples
                if not (
                    sample.get("pipeline_ready")
                    and sample.get("scraper_healthy")
                    and sample.get("diagnostics_ok")
                )
            ),
        },
        "dedup": {
            "first_download_audit_delta": first_audit_after - first_audit_before,
            **replay_report,
        },
        "vlm": vlm_report,
        "qdrant": qdrant_report,
        "failures": runner.failures,
    }


def emit_summary(runner: Any, summary: dict[str, Any]) -> int:
    runner.report = summary
    runner.write_json("summary.json", summary)
    print(json.dumps(summary, indent=2, ensure_ascii=False), flush=True)
    if runner.failures:
        runner.log("FAILURES:")
        for failure in runner.failures:
            print(f"  - {failure}", flush=True)
        return 1
    runner.log("PASS: Artlist scale, Drive, VLM/Qdrant and replay dedup checks succeeded")
    return 0
