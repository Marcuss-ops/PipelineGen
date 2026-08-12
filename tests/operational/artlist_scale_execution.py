from __future__ import annotations

import time
from typing import Any

from artlist_scale_reporting import build_summary, emit_summary


def execute(runner: Any) -> int:
    started = time.monotonic()
    first_audit_before = 0
    first_audit_after = 0
    target_ids: list[str] = []
    vlm_report: dict[str, Any] = {}
    qdrant_report: dict[str, Any] = {}
    replay_report: dict[str, Any] = {}
    try:
        runner.validate_settings()
        runner.preflight()
        runner.warmup()
        runner.start_health_monitor()
        first_audit_before = runner.audit_count()
        runner.run_phase("first")
        first_items = runner.phase_items("first", runner.s.clips_per_keyword)
        first_audit_after = runner.audit_count()
        target_ids = sorted(
            {
                str(item.get("clip_id", "")).strip()
                for item in first_items
                if item.get("clip_id")
            }
        )
        if not target_ids:
            raise RuntimeError("first phase produced no target clip IDs")
        assets = runner.validate_assets(target_ids)
        runner.validate_drive(assets)
        identity_before = runner.identity_snapshot(assets)
        runner.write_json("sqlite/identity_before.json", identity_before)
        vlm_report = runner.validate_vlm(target_ids)
        qdrant_report = runner.validate_qdrant(target_ids)
        replay_report = runner.replay(target_ids, identity_before)
    except Exception as exc:  # noqa: BLE001 - final operational envelope
        runner.fail(str(exc))
    finally:
        if runner.health_thread is not None:
            runner.stop_health_monitor()

    elapsed_ms = round((time.monotonic() - started) * 1000)
    first_items_count = sum(
        len(result.get("job", {}).get("result", {}).get("items", []))
        if isinstance(result.get("job", {}).get("result", {}), dict)
        else 0
        for result in runner.phase_results.get("first", [])
    )
    assets_per_minute = (
        round(first_items_count / (elapsed_ms / 60000), 3)
        if elapsed_ms > 0
        else 0.0
    )
    if assets_per_minute < runner.s.min_assets_per_minute:
        runner.fail(
            f"throughput {assets_per_minute} assets/min is below minimum "
            f"{runner.s.min_assets_per_minute}"
        )

    summary = build_summary(
        runner,
        elapsed_ms=elapsed_ms,
        first_audit_before=first_audit_before,
        first_audit_after=first_audit_after,
        target_ids=target_ids,
        replay_report=replay_report,
        vlm_report=vlm_report,
        qdrant_report=qdrant_report,
        first_items_count=first_items_count,
        assets_per_minute=assets_per_minute,
    )
    return emit_summary(runner, summary)
