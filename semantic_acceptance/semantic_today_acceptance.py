#!/usr/bin/env python3
"""Acceptance harness for the Rust semantic-engine worker (semantic_manifest.v2).

Subcommands:
  selftest                      check the harness itself on a built-in sample
  run   --worker BIN            full battery on the Garlasco fixture
  custom --text-file F --worker BIN [--scene-words N]

Verdicts: PASS, FAIL, NOT_IMPLEMENTED, NEEDS_REVIEW, NOT_RUN.
Exit: 1 if any FAIL, 2 if any NOT_RUN/NEEDS_REVIEW, 0 otherwise.
"""
import argparse
import json
import os
import re
import subprocess
import sys

HERE = os.path.dirname(os.path.abspath(__file__))
FIXTURE = os.path.join(HERE, "fixtures", "garlasco_full.txt")
REPORT_DIR = "/tmp/semantic-today-acceptance"

SELFTEST_TEXT = (
    "Nel 2007 Chiara Poggi viene uccisa a Garlasco. "
    "Il 12 agosto 2014 Alberto Stasi viene assolto in primo grado. "
    "Il 42% dei casi riguarda la prova genetica."
)


def split_scenes(text, scene_words):
    """Split text into scenes of roughly scene_words words, on sentence bounds."""
    sentences = re.split(r"(?<=[.!?])\s+", text.strip())
    scenes, current, count = [], [], 0
    for sentence in sentences:
        current.append(sentence)
        count += len(sentence.split())
        if count >= scene_words:
            scenes.append(" ".join(current))
            current, count = [], 0
    if current:
        scenes.append(" ".join(current))
    return scenes


def call_worker(worker, scenes_text, timeout):
    scenes = [{"scene_id": "s%02d" % (i + 1), "text": t} for i, t in enumerate(scenes_text)]
    request = {"operation": "analyze_script", "language": "it", "scenes": scenes}
    proc = subprocess.run(
        [worker],
        input=(json.dumps(request, ensure_ascii=False) + "\n").encode("utf-8"),
        capture_output=True,
        timeout=timeout,
    )
    line = proc.stdout.decode("utf-8").strip().splitlines()
    if not line:
        raise RuntimeError("worker produced no output: " + proc.stderr.decode("utf-8")[:500])
    response = json.loads(line[-1])
    if not response.get("ok", True) and "manifest" not in response:
        raise RuntimeError("worker error: " + str(response.get("error")))
    return response["manifest"], scenes


def check(results, name, verdict, detail):
    results.append({"check": name, "verdict": verdict, "detail": detail})


def check_offsets(results, manifest, scenes):
    """Every candidate's start/end bytes must slice back to its text."""
    bad = 0
    total = 0
    for scene_manifest, scene in zip(manifest["scenes"], scenes):
        raw = scene["text"].encode("utf-8")
        for group in ("entities", "bullets", "short_phrases", "long_phrases", "quotes"):
            for item in scene_manifest.get(group, []):
                total += 1
                sliced = raw[item["start_byte"]:item["end_byte"]].decode("utf-8", "replace")
                if sliced != item["text"]:
                    bad += 1
    verdict = "PASS" if total and bad == 0 else "FAIL"
    check(results, "offsets_round_trip", verdict, "%d candidates, %d bad offsets" % (total, bad))


def check_contract(results, manifest, scenes):
    ok = manifest.get("schema_version") == "semantic_manifest.v2"
    check(results, "schema_version", "PASS" if ok else "FAIL", str(manifest.get("schema_version")))
    counts = len(manifest.get("scenes", [])) == len(scenes)
    check(results, "scene_count", "PASS" if counts else "FAIL",
          "%d scenes returned for %d sent" % (len(manifest.get("scenes", [])), len(scenes)))


def check_extraction(results, manifest):
    entities = sum(len(s.get("entities", [])) for s in manifest["scenes"])
    facts = sum(len(s.get("entity_facts", [])) for s in manifest["scenes"])
    check(results, "entities_extracted", "PASS" if entities else "FAIL", "%d entity mentions" % entities)
    check(results, "entity_facts_present", "PASS" if facts else "NEEDS_REVIEW", "%d facts" % facts)
    dated = [f for s in manifest["scenes"] for f in s.get("entity_facts", []) if f.get("normalized_date")]
    check(results, "dates_normalized", "PASS" if dated else "NEEDS_REVIEW", "%d dated facts" % len(dated))


def check_bullets(results, manifest, scenes):
    bullets = [b for s in manifest["scenes"] for b in s.get("bullets", [])]
    grounded = 0
    for scene_manifest, scene in zip(manifest["scenes"], scenes):
        for b in scene_manifest.get("bullets", []):
            if b["evidence"] and b["evidence"] in scene["text"]:
                grounded += 1
    scenes_with = sum(1 for s in manifest["scenes"] if s.get("bullets"))
    verdict = "PASS" if bullets and grounded == len(bullets) and scenes_with >= min(3, len(scenes)) else "FAIL"
    check(results, "bullets_grounded", verdict,
          "%d bullets, %d grounded, %d scenes with bullets" % (len(bullets), grounded, scenes_with))


def check_charts(results, manifest, scenes):
    """A chart must be backed by numbers present in its scene text, and never invented."""
    charts = []
    for scene_manifest, scene in zip(manifest["scenes"], scenes):
        for vp in scene_manifest.get("visual_proposals", []):
            if vp.get("chart_items"):
                charts.append((vp, scene["text"]))
    if not charts:
        check(results, "charts_grounded", "NOT_RUN", "no chart proposals produced")
        return
    bad = 0
    for vp, text in charts:
        for item in vp["chart_items"]:
            if str(item["value"]).rstrip("0").rstrip(".") not in text.replace(",", ".") and item.get("text", "") not in text:
                bad += 1
    check(results, "charts_grounded", "PASS" if bad == 0 else "FAIL",
          "%d charts, %d unbacked values" % (len(charts), bad))


def check_micro_summaries(results, manifest):
    summaries = [m for s in manifest["scenes"] for m in s.get("micro_summaries", [])]
    if not summaries:
        check(results, "micro_summaries", "NOT_IMPLEMENTED", "manifest has no micro-summary output")
        return
    linked = sum(1 for m in summaries if m.get("evidence_fact_ids") or m.get("evidence"))
    check(results, "micro_summaries", "PASS" if linked == len(summaries) else "FAIL",
          "%d summaries, %d linked to evidence" % (len(summaries), linked))


def check_groups(results, manifest):
    groups = [g for s in manifest["scenes"] for g in s.get("semantic_groups", [])]
    check(results, "density_groups", "PASS" if groups else "NEEDS_REVIEW", "%d groups" % len(groups))


def check_absent_contracts(results):
    check(results, "story_graph", "NOT_IMPLEMENTED", "no cross-scene fact graph in semantic_manifest.v2")
    check(results, "visual_coverage", "NOT_IMPLEMENTED", "no coverage report in semantic_manifest.v2")
    check(results, "search_feedback", "NOT_IMPLEMENTED", "no feedback field for failed asset searches")


def evaluate(worker, text, scene_words, timeout):
    scenes_text = split_scenes(text, scene_words)
    manifest, scenes = call_worker(worker, scenes_text, timeout)
    results = []
    check_contract(results, manifest, scenes)
    check_offsets(results, manifest, scenes)
    check_extraction(results, manifest)
    check_bullets(results, manifest, scenes)
    check_charts(results, manifest, scenes)
    check_micro_summaries(results, manifest)
    check_groups(results, manifest)
    check_absent_contracts(results)
    return results, manifest


def write_report(name, results):
    os.makedirs(REPORT_DIR, exist_ok=True)
    path = os.path.join(REPORT_DIR, name)
    with open(path, "w", encoding="utf-8") as fh:
        json.dump(results, fh, ensure_ascii=False, indent=2)
    return path


def exit_code(results):
    verdicts = {r["verdict"] for r in results}
    if "FAIL" in verdicts:
        return 1
    if verdicts & {"NOT_RUN", "NEEDS_REVIEW"}:
        return 2
    return 0


def print_results(results):
    for r in results:
        print("%-18s %-22s %s" % (r["verdict"], r["check"], r["detail"]))


def cmd_selftest(args):
    results, _ = evaluate(args.worker, SELFTEST_TEXT, 220, 60)
    print_results(results)
    path = write_report("selftest.json", results)
    print("report:", path)
    return exit_code(results)


def cmd_run(args):
    with open(FIXTURE, encoding="utf-8") as fh:
        text = fh.read()
    results, _ = evaluate(args.worker, text, 220, args.timeout)
    print_results(results)
    print("report:", write_report("report.json", results))
    return exit_code(results)


def cmd_custom(args):
    with open(args.text_file, encoding="utf-8") as fh:
        text = fh.read()
    results, _ = evaluate(args.worker, text, args.scene_words, args.timeout)
    print_results(results)
    print("report:", write_report("custom.json", results))
    return exit_code(results)


def main():
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    sub = parser.add_subparsers(dest="cmd", required=True)
    for name in ("selftest", "run", "custom"):
        p = sub.add_parser(name)
        p.add_argument("--worker", required=True)
        p.add_argument("--timeout", type=int, default=480)
        if name == "custom":
            p.add_argument("--text-file", required=True)
            p.add_argument("--scene-words", type=int, default=220)
    args = parser.parse_args()
    handler = {"selftest": cmd_selftest, "run": cmd_run, "custom": cmd_custom}[args.cmd]
    sys.exit(handler(args))


if __name__ == "__main__":
    main()
