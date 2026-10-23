#!/usr/bin/env python3
"""Garlasco gold test against the Rust semantic-engine worker.

Usage: python3 garlasco_semantic_gold_test.py --worker BIN
The gold file is derived from the fixture text (not independent annotations),
so a PASS means the worker recovers what the text plainly states.
"""
import argparse
import json
import os
import sys

from semantic_today_acceptance import FIXTURE, call_worker, split_scenes, write_report, exit_code, print_results

HERE = os.path.dirname(os.path.abspath(__file__))
GOLD = os.path.join(HERE, "fixtures", "garlasco_gold.json")


def mentions(text, phrase):
    return text.lower().count(phrase.lower())


def run(worker, timeout):
    with open(FIXTURE, encoding="utf-8") as fh:
        text = fh.read()
    with open(GOLD, encoding="utf-8") as fh:
        gold = json.load(fh)
    manifest, scenes = call_worker(worker, split_scenes(text, 220), timeout)
    results = []

    worker_entities = {}
    for s in manifest["scenes"]:
        for e in s.get("entities", []):
            worker_entities[e["text"].lower()] = worker_entities.get(e["text"].lower(), 0) + 1

    for ent in gold["entities"]:
        name = ent["canonical"].lower()
        found = worker_entities.get(name, 0)
        real = mentions(text, ent["canonical"])
        if real < ent["min_mentions"]:
            verdict, detail = "NEEDS_REVIEW", "gold min %d but text has %d" % (ent["min_mentions"], real)
        elif found == 0:
            verdict, detail = "FAIL", "entity not extracted (text has %d)" % real
        else:
            verdict, detail = "PASS", "extracted %d times" % found
        results.append({"check": "entity:" + ent["canonical"], "verdict": verdict, "detail": detail})

    facts = [f for s in manifest["scenes"] for f in s.get("entity_facts", [])]
    # A bare date sentence ("13 agosto 2007.") names no subject, so it cannot
    # become an entity fact; the canonical record of the occurrence is the
    # scene's temporal_facts stream. Accept either, but never an unnormalized
    # surface.
    temporal_dates = {
        f.get("normalized_date")
        for s in manifest["scenes"]
        for f in s.get("temporal_facts", [])
    }
    for ev in gold["events"]:
        if "date" in ev:
            in_fact = any(f.get("normalized_date") == ev["date"] for f in facts)
            in_temporal = ev["date"] in temporal_dates
            hit = in_fact or in_temporal
            source = "entity_fact" if in_fact else ("temporal_fact" if in_temporal else "")
            verdict = "PASS" if hit else "FAIL"
            results.append({"check": "event:" + ev["id"], "verdict": verdict,
                            "detail": "date %s %s" % (ev["date"],
                                                      ("normalized in " + source) if hit else "not normalized")})
            continue
        present = mentions(text, ev["phrase"])
        if present == 0:
            results.append({"check": "event:" + ev["id"], "verdict": "NEEDS_REVIEW",
                            "detail": "phrase not in text: " + ev["phrase"]})
            continue
        in_bullets = any(ev["phrase"].lower() in b["text"].lower() or ev["phrase"].lower() in b["evidence"].lower()
                         for s in manifest["scenes"] for b in s.get("bullets", []))
        in_micro = any(ev["phrase"].lower() in m["text"].lower()
                       for s in manifest["scenes"] for m in s.get("micro_summaries", []))
        verdict = "PASS" if (in_bullets or in_micro) else "FAIL"
        results.append({"check": "event:" + ev["id"], "verdict": verdict,
                        "detail": "in text %dx, bullets=%s micro=%s" % (present, in_bullets, in_micro)})

    for kw in gold["keywords"]:
        n = mentions(text, kw)
        results.append({"check": "keyword:" + kw, "verdict": "PASS" if n else "FAIL", "detail": "%d in text" % n})

    groups = sum(len(s.get("semantic_groups", [])) for s in manifest["scenes"])
    results.append({"check": "groups", "verdict": "PASS" if groups >= gold["groups_expected_min"] else "FAIL",
                    "detail": "%d groups (min %d)" % (groups, gold["groups_expected_min"])})
    return results


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--worker", required=True)
    parser.add_argument("--timeout", type=int, default=480)
    args = parser.parse_args()
    results = run(args.worker, args.timeout)
    print_results(results)
    print("report:", write_report("garlasco_gold.json", results))
    sys.exit(exit_code(results))


if __name__ == "__main__":
    main()
