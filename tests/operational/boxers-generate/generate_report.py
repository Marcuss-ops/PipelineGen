#!/usr/bin/env python3
"""Build the structured report for the boxer multilang operational scenario."""

from __future__ import annotations

import argparse
import json
import os
import sqlite3
import math
import sys
from typing import Any

from stock_registry import load_resolved_stock, scene_expectations


LANG_CODES = ["it", "en", "es", "pt", "fr", "de", "nl", "pl", "ro", "tr"]


def deep_get(value: Any, *keys: str, default: Any = None) -> Any:
    for key in keys:
        if isinstance(value, dict):
            value = value.get(key, default)
        else:
            return default
    return value


def _request_items(data: dict[str, Any]) -> dict[str, dict[str, Any]]:
    """Index request metadata so translated children retain their target language."""
    candidates = (
        deep_get(data, "job", "payload", "items", default=[]),
        deep_get(data, "payload", "items", default=[]),
        deep_get(data, "result", "data", "payload", "items", default=[]),
    )
    indexed: dict[str, dict[str, Any]] = {}
    for candidate_items in candidates:
        if not isinstance(candidate_items, list):
            continue
        for candidate in candidate_items:
            if not isinstance(candidate, dict):
                continue
            item_id = candidate.get("id") or candidate.get("item_id")
            if item_id:
                indexed[str(item_id)] = candidate
    return indexed


def _attach_request_metadata(
    items: list[dict[str, Any]], request_items: dict[str, dict[str, Any]]
) -> list[dict[str, Any]]:
    enriched: list[dict[str, Any]] = []
    for item in items:
        if not isinstance(item, dict):
            continue
        item_id = item.get("item_id") or deep_get(item, "result", "item_id", default="")
        request = request_items.get(str(item_id)) if item_id else None
        if request:
            item = {**item, "_request": request}
        enriched.append(item)
    return enriched


def extract_items(data: dict[str, Any], db_path: str = "data/media/media.db.sqlite") -> list[dict[str, Any]]:
    request_items = _request_items(data)
    items = deep_get(data, "result", "data", "items", default=[])
    if not items:
        items = deep_get(data, "job", "result", "data", "items", default=[])
    if not items:
        items = deep_get(data, "job", "result", "data", "data", "items", default=[])
    if items:
        return _attach_request_metadata(items, request_items)

    child_ids = (
        deep_get(data, "result", "child_job_ids", default=[])
        or deep_get(data, "job", "result", "child_job_ids", default=[])
        or deep_get(data, "result", "data", "child_job_ids", default=[])
        or deep_get(data, "job", "result", "data", "child_job_ids", default=[])
    )
    if not child_ids or not db_path or not os.path.exists(db_path):
        return []

    reconstructed = []
    try:
        with sqlite3.connect(db_path) as connection:
            for child_id in child_ids:
                row = connection.execute(
                    "SELECT status, payload_json, result_json FROM jobs WHERE id = ?",
                    (child_id,),
                ).fetchone()
                if row is None:
                    continue
                status, payload_json, result_json = row
                payload = json.loads(payload_json)
                result = json.loads(result_json) if result_json else {}
                item = payload.get("item", {})
                script = connection.execute(
                    "SELECT id, narrative_text, specscene FROM scripts "
                    "WHERE title = ? AND language = ? ORDER BY created_at DESC LIMIT 1",
                    (item.get("title", ""), item.get("language", "")),
                ).fetchone()
                script_id, text, specscene = (None, "", {})
                if script:
                    script_id, text, raw_specscene = script
                    try:
                        specscene = json.loads(raw_specscene) if raw_specscene else {}
                    except json.JSONDecodeError:
                        specscene = {}
                reconstructed.append({
                    "item_id": item.get("id", ""),
                    "_request": item,
                    "result": {
                        "status": status,
                        "script_id": script_id,
                        "artifacts": {
                            "document": {
                                "doc_id": result.get("doc_id", ""),
                                "doc_link": result.get("doc_link", ""),
                            }
                        },
                        "output": {"text": text, "specscene": specscene},
                    },
                })
    except (OSError, sqlite3.Error, json.JSONDecodeError):
        return []
    return reconstructed


def _drive_state_counts(data: dict[str, Any], items: list[dict[str, Any]]) -> dict[str, int]:
    counts = {"verified": 0, "missing": 0, "trashed": 0, "inaccessible": 0, "invalid_links": 0}
    states = {"MISSING", "TRASHED", "INACCESSIBLE"}
    for item in items:
        output = deep_get(item, "result", "output", default={}) or deep_get(
            item, "result", "data", "output", default={}
        )
        for scene in deep_get(output, "specscene", "scenes", default=[]):
            for binding_name in ("stock", "clip", "voiceover"):
                binding = deep_get(scene, "bindings", binding_name, default={})
                if not isinstance(binding, dict):
                    continue
                state = str(binding.get("drive_verification_state", "")).upper()
                if state in states:
                    counts[state.lower()] += 1
                    counts["invalid_links"] += 1
                elif binding.get("drive_link") or binding.get("link"):
                    counts["verified"] += 1
    for detail in data.get("details", []):
        state = str(detail.get("state", "")).upper() if isinstance(detail, dict) else ""
        if state in states:
            counts[state.lower()] += 1
            counts["invalid_links"] += 1
    return counts


def _voiceover_link(voiceover: dict[str, Any]) -> str:
    return str(voiceover.get("drive_link") or voiceover.get("link") or "").strip()


def _voiceover_errors(
    voiceover: Any, expected_language: str, expected_folder_id: str
) -> list[str]:
    if not isinstance(voiceover, dict):
        return ["voiceover binding is missing"]
    errors: list[str] = []
    status = str(voiceover.get("status", "")).strip().casefold()
    if status not in {"completed", "succeeded"}:
        errors.append("status is not terminal-positive")
    if not _voiceover_link(voiceover):
        errors.append("drive_link is empty")
    if not str(voiceover.get("voice", "")).strip():
        errors.append("voice is empty")
    actual_language = str(voiceover.get("language", "")).strip()
    if not actual_language or actual_language.casefold() != expected_language.casefold():
        errors.append(
            f"language={actual_language or '(missing)'}, expected {expected_language}"
        )
    actual_folder = str(voiceover.get("folder_id", "")).strip()
    if actual_folder != expected_folder_id:
        errors.append(
            f"folder_id={actual_folder or '(missing)'}, expected {expected_folder_id}"
        )
    try:
        duration = float(voiceover.get("duration_seconds", 0))
    except (TypeError, ValueError):
        duration = 0
    if not math.isfinite(duration) or duration <= 0:
        errors.append("duration_seconds must be > 0")
    return errors


def _first_language(value: Any) -> str:
    if isinstance(value, str) and value.strip():
        return value.strip()
    if isinstance(value, list):
        for language in value:
            if isinstance(language, str) and language.strip():
                return language.strip()
    return ""


def _item_language(item: dict[str, Any]) -> str:
    """Resolve report language with verifier-compatible precedence."""
    containers = [item]
    request = item.get("_request")
    if isinstance(request, dict):
        containers.append(request)
    result = item.get("result")
    if isinstance(result, dict):
        containers.append(result)
        data = result.get("data")
        if isinstance(data, dict):
            containers.append(data)

    for container in containers:
        output = container.get("output")
        if isinstance(output, dict):
            language = _first_language(output.get("translate_to"))
            if language:
                return language
    for container in containers:
        docs = container.get("docs")
        if isinstance(docs, dict):
            language = _first_language(docs.get("languages"))
            if language:
                return language
    for container in containers:
        language = _first_language(container.get("language"))
        if language:
            return language
    item_id = str(item.get("item_id", ""))
    return item_id.rsplit("-", 1)[-1].strip()


def generate_report(
    job_path: str,
    report_path: str,
    registry_path: str,
    voiceover_folder_id: str,
) -> dict[str, Any]:
    """Generate a report using the single runtime voiceover folder value."""
    voiceover_folder_id = str(voiceover_folder_id or "").strip()
    if not voiceover_folder_id:
        raise ValueError("BOXERS_VOICEOVER_FOLDER_ID is required")
    with open(job_path, encoding="utf-8") as handle:
        data = json.load(handle)

    resolved = load_resolved_stock(registry_path)
    expectations = scene_expectations(resolved)
    expected_assets = {entry["index"]: entry["asset_id"] for entry in expectations}
    expected_items = len(LANG_CODES)
    expected_scenes = expected_items * len(expectations)
    items = extract_items(data)

    report: dict[str, Any] = {
        "scenario": "top5_boxers_multilang",
        "items_requested": expected_items,
        "items_completed": 0,
        "languages": {language: "UNKNOWN" for language in LANG_CODES},
        "source_segments": {"expected": expected_scenes, "verified": 0},
        "stock_bindings": {
            "expected": expected_scenes, "verified": 0, "wrong_subject": 0,
            "fallback": 0, "artlist": 0,
        },
        "voiceovers": {
            "expected": expected_scenes,
            "completed": 0,
            "failed": 0,
            "invalid": [],
            "folder_id": voiceover_folder_id,
        },
        "documents": {"expected": expected_items, "created": 0, "wrong_folder": 0},
        "sqlite": {"scripts": 0, "completed": 0},
        "drive_verification": _drive_state_counts(data, items),
        # Machine-readable outcome contract. Generation may have completed
        # while Drive reconciliation failed; the overall result must still
        # be non-successful so one invalid link can never become a false
        # SUCCEEDED publication.
        "generation_status": "unknown",
        "drive_reconciliation_status": "unknown",
        "overall_status": "unknown",
        "final": "PASS",
    }

    language_results: dict[str, str] = {}
    for item in items:
        language = item.get("item_id", "").split("-")[-1]
        status = deep_get(item, "result", "status", default="UNKNOWN")
        if status in ("completed", "SUCCEEDED"):
            report["items_completed"] += 1
            report["sqlite"]["scripts"] += 1
            report["sqlite"]["completed"] += 1
            language_results[language] = "PASS"
        else:
            language_results[language] = "FAIL"
    report["languages"] = {language: language_results.get(language, "UNKNOWN") for language in LANG_CODES}

    for item in items:
        item_id = item.get("item_id", "")
        output = deep_get(item, "result", "output", default={}) or deep_get(
            item, "result", "data", "output", default={}
        ) or {}
        scenes = deep_get(output, "specscene", "scenes", default=[])
        for scene_index, scene in enumerate(scenes):
            report["source_segments"]["verified"] += 1
            stock = deep_get(scene, "bindings", "stock", default={})
            if isinstance(stock, dict):
                asset_id = stock.get("asset_id", "")
                if asset_id:
                    report["stock_bindings"]["verified"] += 1
                    if expected_assets.get(scene_index) != asset_id:
                        report["stock_bindings"]["wrong_subject"] += 1
                if stock.get("fallback", False):
                    report["stock_bindings"]["fallback"] += 1
                if stock.get("source", "") == "artlist":
                    report["stock_bindings"]["artlist"] += 1
            voiceover = deep_get(scene, "bindings", "voiceover", default={})
            voiceover_errors = _voiceover_errors(
                voiceover, _item_language(item), voiceover_folder_id
            )
            if voiceover_errors:
                report["voiceovers"]["failed"] += 1
                report["voiceovers"]["invalid"].append({
                    "item_id": item_id,
                    "scene": scene_index,
                    "errors": voiceover_errors,
                })
            else:
                report["voiceovers"]["completed"] += 1
        if deep_get(item, "result", "artifacts", "document", "doc_link") or deep_get(
            item, "result", "data", "artifacts", "document", "doc_link"
        ):
            report["documents"]["created"] += 1

    # Distinguish the generation step from the publication/reconciliation
    # gate. A completed generation is not an overall success when Drive
    # contains even one invalid link.
    report["generation_status"] = (
        "completed" if report["items_completed"] == expected_items else "failed"
    )
    report["drive_reconciliation_status"] = (
        "passed" if report["drive_verification"]["invalid_links"] == 0 else "failed"
    )

    failures = []
    if report["items_completed"] != expected_items:
        failures.append(f"items_completed={report['items_completed']}/{expected_items}")
    if report["source_segments"]["verified"] != expected_scenes:
        failures.append(f"source_segments={report['source_segments']['verified']}/{expected_scenes}")
    if report["stock_bindings"]["verified"] != expected_scenes:
        failures.append(f"stock_bindings={report['stock_bindings']['verified']}/{expected_scenes}")
    for key in ("wrong_subject", "fallback", "artlist"):
        if report["stock_bindings"][key] > 0:
            failures.append(f"{key}={report['stock_bindings'][key]}")
    if report["voiceovers"]["completed"] != expected_scenes:
        failures.append(f"voiceovers={report['voiceovers']['completed']}/{expected_scenes}")
    if report["voiceovers"]["invalid"]:
        failures.append(
            f"invalid_voiceovers={len(report['voiceovers']['invalid'])}"
        )
    if report["documents"]["created"] != expected_items:
        failures.append(f"documents={report['documents']['created']}/{expected_items}")
    if report["sqlite"]["completed"] != expected_items:
        failures.append(f"sqlite_scripts={report['sqlite']['completed']}/{expected_items}")
    if report["drive_verification"]["invalid_links"]:
        failures.append(f"invalid_drive_links={report['drive_verification']['invalid_links']}")
    if failures:
        report["final"] = "FAIL"
        report["overall_status"] = "failed"
        report["_failures"] = failures
    else:
        report["overall_status"] = "succeeded"

    if report_path == "--stdout":
        print(json.dumps(report, indent=2))
    else:
        os.makedirs(os.path.dirname(report_path) or ".", exist_ok=True)
        with open(report_path, "w", encoding="utf-8") as handle:
            json.dump(report, handle, indent=2)
            handle.write("\n")
        print(f"Report saved to {report_path}")
        print(f"  Final: {report['final']}")
        for failure in failures:
            print(f"  → {failure}")
    return report


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("job_path")
    parser.add_argument("report_path", nargs="?", default="--stdout")
    parser.add_argument("--registry", required=True)
    parser.add_argument(
        "--voiceover-folder-id",
        required=True,
        help="Runtime BOXERS_VOICEOVER_FOLDER_ID used by report validation",
    )
    args = parser.parse_args(argv)
    try:
        generate_report(
            args.job_path,
            args.report_path,
            args.registry,
            args.voiceover_folder_id,
        )
    except (OSError, ValueError, json.JSONDecodeError) as exc:
        print(f"report generation error: {exc}", file=sys.stderr)
        return 2
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
