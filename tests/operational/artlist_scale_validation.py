from __future__ import annotations

import os
import sqlite3
import subprocess
from typing import Any

from artlist_scale_config import chunks


def audit_count(runner: Any) -> int:
    with sqlite3.connect(f"file:{runner.s.db_path}?mode=ro", uri=True) as conn:
        row = conn.execute("SELECT COUNT(*) FROM artlist_download_audit WHERE status='succeeded'").fetchone()
    return int(row[0]) if row else 0


def load_assets(runner: Any, target_ids: list[str]) -> list[dict[str, Any]]:
    if not target_ids:
        return []
    placeholders = ",".join("?" for _ in target_ids)
    query = f"""
        SELECT id, source, media_type, lifecycle_state, index_state,
               COALESCE(drive_file_id,'') AS drive_file_id,
               COALESCE(drive_link,'') AS drive_link,
               COALESCE(download_link,'') AS download_link,
               COALESCE(local_path,'') AS local_path,
               COALESCE(file_hash,'') AS file_hash,
               COALESCE(source_version,'') AS source_version,
               COALESCE(source_url,'') AS source_url,
               COALESCE(metadata_json,'{{}}') AS metadata_json
        FROM media_assets WHERE id IN ({placeholders})
    """
    with sqlite3.connect(f"file:{runner.s.db_path}?mode=ro", uri=True) as conn:
        conn.row_factory = sqlite3.Row
        return [dict(row) for row in conn.execute(query, target_ids)]


def validate_assets(runner: Any, target_ids: list[str]) -> list[dict[str, Any]]:
    rows = load_assets(runner, target_ids)
    found_ids = {row["id"] for row in rows}
    missing = sorted(set(target_ids) - found_ids)
    invalid: list[str] = []
    m3u8_ids: list[str] = []
    for row in rows:
        valid = all((
            row["source"] == "artlist", row["media_type"] == "video",
            row["lifecycle_state"] == "PUBLISHED", row["index_state"] == "INDEXED",
            bool(row["drive_file_id"]), bool(row["drive_link"]), bool(row["file_hash"]),
            bool(row["source_version"]), bool(row["source_url"] or row["download_link"]),
        ))
        if not valid:
            invalid.append(row["id"])
        if ".m3u8" in " ".join((row["source_url"], row["download_link"], row["metadata_json"])).lower():
            m3u8_ids.append(row["id"])
    report = {"requested_ids": len(target_ids), "found_rows": len(rows), "missing_ids": missing, "invalid_ids": invalid, "m3u8_persisted_count": len(m3u8_ids), "m3u8_persisted_ids": sorted(m3u8_ids), "assets": rows}
    runner.write_json("sqlite/assets.json", report)
    if missing:
        runner.fail(f"SQLite is missing {len(missing)} target assets")
    if invalid:
        runner.fail(f"{len(invalid)} target assets failed publication/index/Drive/hash checks")
    if runner.s.require_m3u8 and not m3u8_ids:
        runner.fail("no target asset persists an m3u8 URL in source_url, download_link or metadata_json")
    return rows


def validate_drive(runner: Any, assets: list[dict[str, Any]]) -> None:
    drive_ids = sorted({row["drive_file_id"] for row in assets if row["drive_file_id"]})
    resolved_total = 0
    invalid: list[str] = []
    responses: list[Any] = []
    for batch in chunks(drive_ids, 50):
        response = runner.http.post(f"{runner.s.base_url}/api/drive/resolve-by-id", {"ids": batch}, admin=True)
        responses.append(response)
        resolved = response.get("resolved", []) if isinstance(response, dict) else []
        resolved_total += int(response.get("resolved_count", len(resolved)))
        for entry in resolved:
            if entry.get("trashed") is not False or int(entry.get("size", 0) or 0) <= 0:
                invalid.append(str(entry.get("id", entry.get("file_id", "unknown"))))
    runner.write_json("drive/resolve.json", responses)
    if resolved_total != len(drive_ids):
        runner.fail(f"Drive resolved {resolved_total}/{len(drive_ids)} unique files")
    if invalid:
        runner.fail(f"Drive contains {len(invalid)} missing, trashed or empty target files")


def run_admin(runner: Any, args: list[str], output_name: str) -> None:
    command = [*runner.s.admin_command, *args]
    completed = subprocess.run(command, cwd=runner.s.repo_root, env=os.environ.copy(), text=True, stdout=subprocess.PIPE, stderr=subprocess.PIPE, timeout=max(runner.s.http_timeout, runner.s.vlm_timeout) * 20, check=False)
    runner.write_json(output_name, {"command": command, "returncode": completed.returncode, "stdout": completed.stdout, "stderr": completed.stderr})
    if completed.returncode != 0:
        raise RuntimeError(f"admin command failed ({completed.returncode}): {' '.join(command)}\n{completed.stderr[-2000:]}")


def validate_vlm(runner: Any, target_ids: list[str]) -> dict[str, Any]:
    if not runner.s.run_vlm:
        report = {"skipped": True, "requested_ids": len(target_ids)}
        runner.write_json("vlm/validation.json", report)
        return report
    runner.log("VLM: generating canonical visual summaries for Artlist assets")
    runner.run_admin(["reindex-visual-summary", "--apply", "--source=artlist", f"--interval={runner.s.vlm_interval}", f"--vlm-timeout={runner.s.vlm_timeout}", "--json"], "vlm/reindex_command.json")
    placeholders = ",".join("?" for _ in target_ids)
    rows: list[dict[str, Any]] = []
    if target_ids:
        query = f"""
            SELECT asset_id, visual_summary_text, visible_actions_json, visible_entities_json,
                   frame_count, interval_seconds, preprocessing_version, model_name,
                   model_version, source_hash, sampled_at
            FROM asset_visual_summaries WHERE asset_id IN ({placeholders})
        """
        with sqlite3.connect(f"file:{runner.s.db_path}?mode=ro", uri=True) as conn:
            conn.row_factory = sqlite3.Row
            rows = [dict(row) for row in conn.execute(query, target_ids)]
    valid_ids = {row["asset_id"] for row in rows if int(row["frame_count"] or 0) > 0 and int(row["interval_seconds"] or 0) > 0 and row["preprocessing_version"] and row["model_name"] and row["model_version"] and row["source_hash"] and row["sampled_at"]}
    invalid_ids = sorted(set(target_ids) - valid_ids)
    report = {"requested_ids": len(target_ids), "rows": len(rows), "valid_rows": len(valid_ids), "invalid_ids": invalid_ids}
    runner.write_json("vlm/validation.json", report)
    if invalid_ids:
        runner.fail(f"VLM produced valid summaries for {len(valid_ids)}/{len(target_ids)} target assets")
    return report


def validate_qdrant(runner: Any, target_ids: list[str]) -> dict[str, Any]:
    if not runner.s.run_vlm:
        report = {"skipped": True, "reason": "VLM disabled"}
        runner.write_json("qdrant/validation.json", report)
        return report
    if runner.s.run_qdrant_reindex:
        runner.log("Qdrant: running blue-green reindex after VLM pass")
        runner.run_admin(["reindex-qdrant", "--apply", "--json"], "qdrant/reindex_command.json")
    headers = {"api-key": runner.s.qdrant_api_key} if runner.s.qdrant_api_key else {}
    payloads: list[dict[str, Any]] = []
    for batch in chunks(target_ids, 50):
        response = runner.http.post(f"{runner.s.qdrant_url}/collections/{runner.s.qdrant_collection}/points/scroll", {"filter": {"must": [{"key": "asset_id", "match": {"any": batch}}]}, "limit": 100, "with_payload": True, "with_vector": False}, headers=headers)
        for point in response.get("result", {}).get("points", []):
            if isinstance(point.get("payload"), dict):
                payloads.append(point["payload"])
    valid_ids = {str(payload.get("asset_id")) for payload in payloads if payload.get("source") == "artlist" and payload.get("lifecycle_state") == "PUBLISHED" and payload.get("visual_preprocessing_version") and payload.get("visual_model_name") and payload.get("visual_model_version")}
    missing = sorted(set(target_ids) - valid_ids)
    report = {"requested_ids": len(target_ids), "valid_payloads": len(valid_ids), "missing_or_invalid_ids": missing}
    runner.write_json("qdrant/validation.json", report)
    if missing:
        runner.fail(f"Qdrant contains valid VLM payloads for {len(valid_ids)}/{len(target_ids)} target assets")
    return report


def identity_snapshot(assets: list[dict[str, Any]]) -> dict[str, dict[str, str]]:
    return {row["id"]: {key: row[key] for key in ("drive_file_id", "drive_link", "file_hash", "source_url", "download_link")} for row in assets}


def replay(runner: Any, target_ids: list[str], identity_before: dict[str, dict[str, str]]) -> dict[str, Any]:
    report: dict[str, Any] = {"enabled": runner.s.run_replay}
    if not runner.s.run_replay:
        runner.write_json("replay/validation.json", report)
        return report
    canary_before = runner.audit_count()
    canary_results = runner.run_phase("replay_canary", limit=1, terms=[runner.s.keywords[0]])
    runner.phase_items("replay_canary", 1)
    canary_delta = runner.audit_count() - canary_before
    report["canary_download_audit_delta"] = canary_delta
    report["canary_statuses"] = [item["status"] for item in canary_results]
    if canary_delta != 0:
        runner.fail(f"replay canary created {canary_delta} successful download-audit rows; full replay aborted to protect Artlist quota")
        runner.write_json("replay/validation.json", report)
        return report
    replay_before = runner.audit_count()
    runner.run_phase("replay")
    runner.phase_items("replay", runner.s.clips_per_keyword)
    replay_delta = runner.audit_count() - replay_before
    identity_after = runner.identity_snapshot(runner.load_assets(target_ids))
    changed = sorted(asset_id for asset_id in target_ids if identity_before.get(asset_id) != identity_after.get(asset_id))
    report.update({"download_audit_delta": replay_delta, "changed_identity_ids": changed})
    if runner.s.require_no_redownload and replay_delta != 0:
        runner.fail(f"full replay created {replay_delta} successful download-audit rows; expected zero")
    if changed:
        runner.fail(f"replay changed Drive/hash identity for {len(changed)} assets")
    runner.write_json("replay/validation.json", report)
    return report
