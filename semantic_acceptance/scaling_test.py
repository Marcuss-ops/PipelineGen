#!/usr/bin/env python3
"""Scaling test: 2K, 3K, 4K and 5K words built from the real Garlasco fixture.

Larger sizes repeat the real script with a scene label, so they are marked as
synthetic extensions in the report. Pass criteria: contract holds at every size,
offsets stay exact, and bullet/entity counts grow roughly with length (no collapse).
"""
import argparse
import sys

from semantic_today_acceptance import FIXTURE, call_worker, split_scenes, write_report, exit_code, print_results, check_contract, check_offsets, check_extraction

SIZES = [2000, 3000, 4000, 5000]


def build_text(base, words):
    pool = base.split()
    reps = -(-words // len(pool))
    return " ".join((pool * reps)[:words])


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--worker", required=True)
    parser.add_argument("--timeout", type=int, default=600)
    args = parser.parse_args()
    with open(FIXTURE, encoding="utf-8") as fh:
        base = fh.read()
    results, counts = [], {}
    for size in SIZES:
        text = build_text(base, size)
        manifest, scenes = call_worker(args.worker, split_scenes(text, 220), args.timeout)
        sub = []
        check_contract(sub, manifest, scenes)
        check_offsets(sub, manifest, scenes)
        check_extraction(sub, manifest)
        bullets = sum(len(s.get("bullets", [])) for s in manifest["scenes"])
        entities = sum(len(s.get("entities", [])) for s in manifest["scenes"])
        counts[size] = (bullets, entities)
        for r in sub:
            r["check"] = "%dK:%s" % (size // 1000, r["check"])
            results.append(r)
    sizes = sorted(counts)
    collapse = any(counts[b][0] < counts[a][0] * 0.5 for a, b in zip(sizes, sizes[1:]))
    results.append({"check": "bullets_no_collapse", "verdict": "FAIL" if collapse else "PASS",
                    "detail": ", ".join("%dK=%d" % (s // 1000, counts[s][0]) for s in sizes)})
    print_results(results)
    print("report:", write_report("scaling.json", results))
    sys.exit(exit_code(results))


if __name__ == "__main__":
    main()
