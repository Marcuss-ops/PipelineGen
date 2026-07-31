#!/usr/bin/env python3
"""Build the structured report for the boxer multilang operational scenario."""

from __future__ import annotations

import argparse
import json
import os
import sqlite3
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


def extract_items(data: dict[str, Any], db_path: str = "data/media/media.db.sqlite") -> list[dict[str, Any]]:
    items = deep_get(data, "result", "data", "items", default=[])
    if not items:
        items = deep_get(data, "job", "result", "data", "items", default=[])
    if not items:
        items = deep_get(data, "job", "result", "data", "data", "items", default=[])
    if items:
        return items

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


def generate_report(job_path: str, report_path: str, registry_path: str) -> dict[str, Any]:
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
        "voiceovers": {"expected": expected_scenes, "completed": 0, "failed": 0},
        "documents": {"expected": expected_items, "created": 0, "wrong_folder": 0},
        "sqlite": {"scripts": 0, "completed": 0},
        "drive_verification": _drive_state_counts(data, items),
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
            if isinstance(voiceover, dict):
                if voiceover.get("status") == "completed" and voiceover.get("link"):
                    report["voiceovers"]["completed"] += 1
                else:
                    report["voiceovers"]["failed"] += 1
        if deep_get(item, "result", "artifacts", "document", "doc_link") or deep_get(
            item, "result", "data", "artifacts", "document", "doc_link"
        ):
            report["documents"]["created"] += 1

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
    if report["documents"]["created"] != expected_items:
        failures.append(f"documents={report['documents']['created']}/{expected_items}")
    if report["sqlite"]["completed"] != expected_items:
        failures.append(f"sqlite_scripts={report['sqlite']['completed']}/{expected_items}")
    if report["drive_verification"]["invalid_links"]:
        failures.append(f"invalid_drive_links={report['drive_verification']['invalid_links']}")
    if failures:
        report["final"] = "FAIL"
        report["_failures"] = failures

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
    args = parser.parse_args(argv)
    try:
        generate_report(args.job_path, args.report_path, args.registry)
    except (OSError, ValueError, json.JSONDecodeError) as exc:
        print(f"report generation error: {exc}", file=sys.stderr)
        return 2
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
