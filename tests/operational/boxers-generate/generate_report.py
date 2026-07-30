#!/usr/bin/env python3
"""
generate_report.py — Extract structured test report from aggregated job response JSON.

Usage:
  python3 generate_report.py <aggregated_job_json> <report_output_path>
  python3 generate_report.py <aggregated_job_json> --stdout
"""

import json
import sys
import os


EXPECTED_ASSETS = {
    0: "yt_0vnOfawuQF4_20_24_v1",   # Mike Tyson
    1: "yt_6kEmuFoEy54_8_12_v1",    # Muhammad Ali
    2: "yt_VJAk5sy1xoI_8_12_v1",    # Manny Pacquiao
    3: "yt_66Dg_n0H8rQ_8_12_v1",    # Floyd Mayweather
    4: "yt_n_M4SFK8NCc_8_12_v1",    # Sugar Ray Robinson
}

BOXER_NAMES = [
    "Mike Tyson", "Muhammad Ali", "Manny Pacquiao",
    "Floyd Mayweather", "Sugar Ray Robinson",
]

LANG_CODES = ["it", "en", "es", "pt", "fr", "de", "nl", "pl", "ro", "tr"]


def deep_get(d, *keys, default=None):
    for k in keys:
        if isinstance(d, dict):
            d = d.get(k, default)
        else:
            return default
    return d


def extract_items(data):
    items = deep_get(data, "result", "data", "items", default=[])
    if not items:
        items = deep_get(data, "job", "result", "data", "items", default=[])
    if not items:
        items = deep_get(data, "job", "result", "data", "data", "items", default=[])
    return items


def generate_report(job_path, report_path):
    with open(job_path) as f:
        data = json.load(f)

    items = extract_items(data)

    # ── Base counts ──────────────────────────────────────────────
    report = {
        "scenario": "top5_boxers_multilang",
        "items_requested": 10,
        "items_completed": 0,
        "languages": {},
        "source_segments": {"expected": 50, "verified": 0},
        "stock_bindings": {"expected": 50, "verified": 0, "wrong_subject": 0, "fallback": 0, "artlist": 0},
        "voiceovers": {"expected": 50, "completed": 0, "failed": 0},
        "documents": {"expected": 10, "created": 0, "wrong_folder": 0},
        "sqlite": {"scripts": 0, "completed": 0},
        "final": "PASS",
    }

    # ── Per-language status ───────────────────────────────────────
    for lang in LANG_CODES:
        report["languages"][lang] = "PENDING"

    lang_results = {}
    for item in items:
        item_id = item.get("item_id", "")
        lang = item_id.split("-")[-1] if item_id else "??"
        status = deep_get(item, "result", "status", default="UNKNOWN")
        if status in ("completed", "SUCCEEDED"):
            report["items_completed"] += 1
            report["sqlite"]["scripts"] += 1
            report["sqlite"]["completed"] += 1
            lang_results[lang] = "PASS"
        elif status in ("SUCCEEDED_WITH_WARNINGS",):
            # SWW means translation/voiceover was silently skipped — fatal per test plan
            lang_results[lang] = "FAIL"
        else:
            lang_results[lang] = "FAIL"

    report["languages"] = {k: lang_results.get(k, "UNKNOWN") for k in LANG_CODES}

    # ── Per-item scene analysis ──────────────────────────────────
    for item in items:
        item_id = item.get("item_id", "")
        output = deep_get(item, "result", "output") or deep_get(item, "result", "data", "output") or {}
        scenes = deep_get(output, "specscene", "scenes", default=[])

        for s_idx, scene in enumerate(scenes):
            report["source_segments"]["verified"] += 1

            stock = deep_get(scene, "bindings", "stock", default={})
            if stock:
                asset_id = stock.get("asset_id", "")
                if asset_id:
                    report["stock_bindings"]["verified"] += 1
                    if s_idx in EXPECTED_ASSETS and asset_id != EXPECTED_ASSETS[s_idx]:
                        report["stock_bindings"]["wrong_subject"] += 1
                if stock.get("fallback", False):
                    report["stock_bindings"]["fallback"] += 1
                if stock.get("source", "") == "artlist":
                    report["stock_bindings"]["artlist"] += 1

            vo = deep_get(scene, "bindings", "voiceover", default={})
            if vo:
                if vo.get("status") == "completed" and vo.get("link"):
                    report["voiceovers"]["completed"] += 1
                else:
                    report["voiceovers"]["failed"] += 1

        doc_link = (deep_get(item, "result", "artifacts", "document", "doc_link") or
                    deep_get(item, "result", "data", "artifacts", "document", "doc_link"))
        if doc_link:
            report["documents"]["created"] += 1

    # ── Final verdict ────────────────────────────────────────────
    failures = []
    if report["items_completed"] != 10:
        failures.append(f"items_completed={report['items_completed']}/10")
    if report["source_segments"]["verified"] != 50:
        failures.append(f"source_segments={report['source_segments']['verified']}/50")
    if report["stock_bindings"]["verified"] != 50:
        failures.append(f"stock_bindings={report['stock_bindings']['verified']}/50")
    if report["stock_bindings"]["wrong_subject"] > 0:
        failures.append(f"wrong_subject={report['stock_bindings']['wrong_subject']}")
    if report["stock_bindings"]["artlist"] > 0:
        failures.append(f"artlist_bindings={report['stock_bindings']['artlist']}")
    if report["voiceovers"]["completed"] != 50:
        failures.append(f"voiceovers={report['voiceovers']['completed']}/50")
    if report["documents"]["created"] != 10:
        failures.append(f"documents={report['documents']['created']}/10")
    if report["sqlite"]["completed"] != 10:
        failures.append(f"sqlite_scripts={report['sqlite']['completed']}/10")

    if failures:
        report["final"] = "FAIL"
        report["_failures"] = failures

    # ── Output ───────────────────────────────────────────────────
    if report_path == "--stdout":
        print(json.dumps(report, indent=2))
    else:
        os.makedirs(os.path.dirname(report_path), exist_ok=True)
        with open(report_path, "w") as f:
            json.dump(report, f, indent=2)
        print(f"Report saved to {report_path}")
        print(f"  Final: {report['final']}")
        if failures:
            for f2 in failures:
                print(f"  → {f2}")


if __name__ == "__main__":
    if len(sys.argv) < 2:
        print("Usage: python3 generate_report.py <aggregated_job_json> [report_output_path]")
        print("       python3 generate_report.py <aggregated_job_json> --stdout")
        sys.exit(2)

    job_path = sys.argv[1]
    if len(sys.argv) > 2:
        out_path = sys.argv[2]
    else:
        out_path = "--stdout"

    generate_report(job_path, out_path)
