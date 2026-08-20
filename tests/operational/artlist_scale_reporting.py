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


def build_per_clip_report(
    items: list[dict[str, Any]],
    assets: list[dict[str, Any]],
    drive_report: dict[str, Any] | None,
    qdrant_report: dict[str, Any] | None,
) -> list[dict[str, Any]]:
    """Join job items with canonical SQLite, Drive and Qdrant validation data."""
    asset_by_id = {str(row.get("id", "")).strip(): row for row in assets if row.get("id")}
    drive_by_id = (drive_report or {}).get("resolved_by_id", {})
    qdrant_valid_ids = {str(value).strip() for value in (qdrant_report or {}).get("valid_ids", [])}
    report: list[dict[str, Any]] = []

    for item in items:
        clip_id = str(
            item.get("clip_id")
            or item.get("clipId")
            or item.get("asset_id")
            or item.get("assetId")
            or ""
        ).strip()
        asset_id = str(item.get("asset_id") or item.get("assetId") or clip_id).strip()
        asset = asset_by_id.get(asset_id) or asset_by_id.get(clip_id)
        canonical_id = str(asset.get("id", asset_id) if asset else asset_id).strip()
        drive_file_id = str(asset.get("drive_file_id", "") if asset else "").strip()
        drive_entry = drive_by_id.get(drive_file_id, {})
        sqlite_ok = bool(asset) and asset.get("lifecycle_state") == "PUBLISHED" and asset.get("index_state") == "INDEXED"
        download_ok = bool(asset) and bool(asset.get("local_path")) and bool(asset.get("file_hash"))
        drive_ok = bool(drive_entry.get("ok"))
        qdrant_ok = canonical_id in qdrant_valid_ids or asset_id in qdrant_valid_ids or clip_id in qdrant_valid_ids
        report.append({
            "clip_id": clip_id,
            "asset_id": canonical_id,
            "term": item.get("term", ""),
            "run_id": item.get("run_id", ""),
            "job_status": item.get("job_status", "UNKNOWN"),
            "job_elapsed_ms": item.get("job_elapsed_ms", 0),
            "download_ok": download_ok,
            "sqlite_ok": sqlite_ok,
            "drive_ok": drive_ok,
            "qdrant_ok": qdrant_ok,
            "drive_file_id": drive_file_id,
            "file_hash": str(asset.get("file_hash", "") if asset else ""),
            "lifecycle_state": asset.get("lifecycle_state", "") if asset else "",
            "index_state": asset.get("index_state", "") if asset else "",
        })
    return report


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
    per_clip_report: list[dict[str, Any]] | None = None,
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
            "per_clip": {
                "rows": len(per_clip_report or []),
                "download_ok": sum(1 for row in (per_clip_report or []) if row.get("download_ok")),
                "sqlite_ok": sum(1 for row in (per_clip_report or []) if row.get("sqlite_ok")),
                "drive_ok": sum(1 for row in (per_clip_report or []) if row.get("drive_ok")),
                "qdrant_ok": sum(1 for row in (per_clip_report or []) if row.get("qdrant_ok")),
            },
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
