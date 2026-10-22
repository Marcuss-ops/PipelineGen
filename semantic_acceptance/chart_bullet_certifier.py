#!/usr/bin/env python3
"""Certify Charts and Bullet Points in an overlay plan (and optionally a rendered MP4).

Usage: python3 chart_bullet_certifier.py --plan overlay_plan.json [--script script.txt] [--mp4 out.mp4]

Verdicts follow the acceptance scale. Charts/bullets are PASS only if every chart
value is present in the script text and, when an MP4 is given, it decodes with ffprobe.
Animation visibility in frames is NOT_RUN unless frames are supplied (not automated here).
"""
import argparse
import json
import shutil
import subprocess
import sys

from semantic_today_acceptance import write_report, exit_code, print_results


def load_items(plan):
    return [i for i in plan.get("items", []) if isinstance(i, dict)]


def certify_plan(plan, script_text):
    results = []
    items = load_items(plan)
    # Plan schema: each item has `kind` and `text`. `number` items are date/number cards,
    # not charts; a chart kind would be counted separately below.
    charts = [i for i in items if i.get("kind") in ("chart", "bar_chart", "chart_item")]
    numbers = [i for i in items if i.get("kind") == "number"]
    bullets = [i for i in items if i.get("kind") == "text_phrase"]
    if not charts:
        results.append({"check": "charts_present", "verdict": "NOT_RUN", "detail": "no chart items in plan (%d number cards present)" % len(numbers)})
    for item in charts:
        value = str(item.get("value", item.get("text", "")))
        found = bool(value) and value in script_text
        results.append({"check": "chart:" + str(item.get("id", "?")), "verdict": "PASS" if found else "FAIL",
                        "detail": "value %r %s in script" % (value, "found" if found else "NOT found")})
    for item in numbers:
        text = str(item.get("text", ""))
        found = bool(text) and text in script_text
        results.append({"check": "number:" + str(item.get("id", "?")), "verdict": "PASS" if found else "FAIL",
                        "detail": "%r %s in script" % (text, "found" if found else "NOT found")})
    if bullets:
        ungrounded = [i.get("id", "?") for i in bullets if str(i.get("text", "")) and str(i.get("text")) not in script_text]
        results.append({"check": "bullets_grounded", "verdict": "PASS" if not ungrounded else "FAIL",
                        "detail": "%d bullets, %d ungrounded" % (len(bullets), len(ungrounded))})
    else:
        results.append({"check": "bullets_present", "verdict": "NOT_RUN", "detail": "no bullet items in plan"})
    return results


def certify_mp4(path):
    if not shutil.which("ffprobe"):
        return [{"check": "mp4_decodes", "verdict": "NOT_RUN", "detail": "ffprobe missing"}]
    probe = subprocess.run(["ffprobe", "-v", "error", "-show_entries", "format=duration", "-of", "json", path],
                           capture_output=True, text=True)
    if probe.returncode != 0:
        return [{"check": "mp4_decodes", "verdict": "FAIL", "detail": probe.stderr.strip()[:200]}]
    decode = subprocess.run(["ffmpeg", "-v", "error", "-i", path, "-f", "null", "-"], capture_output=True, text=True)
    ok = decode.returncode == 0 and not decode.stderr.strip()
    return [{"check": "mp4_decodes", "verdict": "PASS" if ok else "FAIL",
             "detail": "decoded cleanly" if ok else decode.stderr.strip()[:200]},
            {"check": "chart_frames_visible", "verdict": "NOT_RUN",
             "detail": "frame-level animation check needs extracted frames"}]


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--plan", required=True)
    parser.add_argument("--script", required=True, help="plain script text the plan was built from")
    parser.add_argument("--mp4")
    args = parser.parse_args()
    with open(args.plan, encoding="utf-8") as fh:
        plan = json.load(fh)
    with open(args.script, encoding="utf-8") as fh:
        script_text = fh.read()
    results = certify_plan(plan, script_text)
    if args.mp4:
        results += certify_mp4(args.mp4)
    print_results(results)
    print("report:", write_report("chart_bullet.json", results))
    sys.exit(exit_code(results))


if __name__ == "__main__":
    main()
