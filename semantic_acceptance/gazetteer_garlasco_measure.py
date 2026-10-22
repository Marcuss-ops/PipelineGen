#!/usr/bin/env python3
"""Measure the GeoNames gazetteer on the Garlasco corpus.

Compares the curated seed (baseline: `places` and `place_ambiguities` removed)
with the real index, at both levels that matter:

  * VisualNER extraction, where the index types a LOCATION;
  * the semantic-engine manifest, where downstream grouping may absorb a
    location into an organization ("Procura di Pavia").

Writes `gazetteer_garlasco_report.md` and a JSON evidence file next to this
script. The report is deterministic for a fixed corpus, lexicon and binaries.

Usage:
  python3 gazetteer_garlasco_measure.py \
      --visualner rust/target/release/visualner \
      --worker rust/target/release/semantic-engine
"""

from __future__ import annotations

import argparse
import json
import os
import subprocess
import sys
import tempfile
from collections import Counter
from pathlib import Path

HERE = Path(__file__).resolve().parent
sys.path.insert(0, str(HERE))

from semantic_today_acceptance import split_scenes  # noqa: E402

DEFAULT_FIXTURE = HERE / "fixtures" / "garlasco_full.txt"
DEFAULT_GOLD = HERE / "fixtures" / "garlasco_gold.json"
DEFAULT_GAZETTEER = HERE.parent / "rust" / "lexicon" / "entity_gazetteer.json"
DEFAULT_REPORT = HERE / "gazetteer_garlasco_report.md"


def prepare_lexicon(gazetteer_path: Path, enabled: bool, root: Path) -> Path:
    data = json.loads(gazetteer_path.read_text(encoding="utf-8"))
    if not enabled:
        data["places"] = []
        data["place_ambiguities"] = {}
    directory = root / ("enabled" if enabled else "baseline")
    directory.mkdir(parents=True, exist_ok=True)
    (directory / "entity_gazetteer.json").write_text(
        json.dumps(data, ensure_ascii=False), encoding="utf-8"
    )
    return directory


def run_visualner(binary: str, scenes: list[str], lexicon_dir: Path) -> Counter:
    env = dict(os.environ)
    env["SEMANTIC_LEXICON_DIR"] = str(lexicon_dir)
    locations: Counter = Counter()
    for text in scenes:
        request = {"source_text": text, "language": "it", "entity_count": 500}
        proc = subprocess.run(
            [binary],
            input=(json.dumps(request, ensure_ascii=False) + "\n").encode("utf-8"),
            capture_output=True,
            env=env,
            timeout=120,
        )
        lines = proc.stdout.decode("utf-8").strip().splitlines()
        if not lines:
            raise RuntimeError("visualner produced no output: " + proc.stderr.decode()[:300])
        response = json.loads(lines[-1])
        for entity in response.get("entities", []):
            if entity.get("type") == "LOCATION":
                locations[entity["text"]] += 1
    return locations


def run_worker(binary: str, scenes: list[str], lexicon_dir: Path) -> Counter:
    env = dict(os.environ)
    env["SEMANTIC_LEXICON_DIR"] = str(lexicon_dir)
    request = {
        "operation": "analyze_script",
        "language": "it",
        "scenes": [
            {"scene_id": "s%02d" % (index + 1), "text": text}
            for index, text in enumerate(scenes)
        ],
    }
    proc = subprocess.run(
        [binary],
        input=(json.dumps(request, ensure_ascii=False) + "\n").encode("utf-8"),
        capture_output=True,
        env=env,
        timeout=300,
    )
    lines = proc.stdout.decode("utf-8").strip().splitlines()
    if not lines:
        raise RuntimeError("worker produced no output: " + proc.stderr.decode()[:300])
    manifest = json.loads(lines[-1])["manifest"]
    locations: Counter = Counter()
    for scene in manifest["scenes"]:
        for entity in scene.get("entities", []):
            if entity.get("type") == "LOCATION":
                locations[entity["text"]] += 1
    return locations


def recall(locations: Counter, gold_names: list[str]) -> tuple[int, int, list[str]]:
    found = [name for name in gold_names if name.lower() in {key.lower() for key in locations}]
    return len(found), len(gold_names), found


def describe(name: str, locations: Counter, gold_names: list[str]) -> dict:
    found_count, total, found = recall(locations, gold_names)
    return {
        "level": name,
        "unique": len(locations),
        "mentions": sum(locations.values()),
        "gold_recall": "%d/%d" % (found_count, total),
        "gold_found": found,
        "locations": dict(sorted(locations.items(), key=lambda item: (-item[1], item[0]))),
    }


def markdown(report: dict) -> str:
    lines = [
        "# GeoNames gazetteer — misura sul corpus Garlasco",
        "",
        "Corpus: `%s` (%d parole, %d scene da %d parole)."
        % (report["fixture"], report["words"], report["scenes"], report["scene_words"]),
        "",
        "Indice: **%d places**, **%d aliases**, generato da GeoNames `cities500`"
        % (report["places"], report["aliases"]),
        "(feature class P, popolazione >= 5000, più capitali; CC BY 4.0).",
        "",
        "Baseline: seed curato `entity_gazetteer.json` senza `places` né",
        "`place_ambiguities`. Abilitato: indice completo dietro la stessa tabella.",
        "",
        "## Esito",
        "",
        "| Livello | Baseline | GeoNames | Gold recall (baseline -> GeoNames) |",
        "| --- | --- | --- | --- |",
    ]
    for baseline, enabled in zip(report["levels"]["baseline"], report["levels"]["enabled"]):
        lines.append(
            "| %s | %d loc / %d menzioni | %d loc / %d menzioni | %s -> %s |"
            % (
                baseline["level"],
                baseline["unique"],
                baseline["mentions"],
                enabled["unique"],
                enabled["mentions"],
                baseline["gold_recall"],
                enabled["gold_recall"],
            )
        )
    lines += [
        "",
        "## Cosa cambia",
        "",
        "- Aggiunte: " + (", ".join(report["added"]) or "nessuna"),
        "- Rimosse: " + (", ".join(report["removed"]) or "nessuna"),
        "",
        "## Controlli di precisione",
        "",
    ]
    for check in report["precision_checks"]:
        lines.append("- `%s`: %s" % (check["surface"], check["verdict"]))
    lines += [
        "",
        "## Note",
        "",
        "- Il corpus non nomina Garlasco né Vigevano: le uniche località del gold",
        "  sono Milano e Pavia.",
        "- Nel manifest del semantic-engine `Procura di Pavia` resta un'ORG, ma",
        "  l'upgrade legale tocca solo l'entità con lo stesso inizio: `Pavia`",
        "  sopravvive come LOCATION e anche il manifest passa a 2/2.",
        "- La posizione `Corte` (tribunale) e `Come` (avverbio) restano fuori:",
        "  il tag UDPipe e la lista di ambiguità italiane le escludono.",
        "",
    ]
    return "\n".join(lines)


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--visualner", required=True)
    parser.add_argument("--worker")
    parser.add_argument("--fixture", type=Path, default=DEFAULT_FIXTURE)
    parser.add_argument("--gold", type=Path, default=DEFAULT_GOLD)
    parser.add_argument("--gazetteer", type=Path, default=DEFAULT_GAZETTEER)
    parser.add_argument("--report", type=Path, default=DEFAULT_REPORT)
    parser.add_argument("--scene-words", type=int, default=220)
    args = parser.parse_args()

    text = args.fixture.read_text(encoding="utf-8")
    scenes = split_scenes(text, args.scene_words)
    gold = json.loads(args.gold.read_text(encoding="utf-8"))
    gold_names = [entity["canonical"] for entity in gold["entities"] if entity["type"] == "LOCATION"]

    gazetteer = json.loads(args.gazetteer.read_text(encoding="utf-8"))
    places = len(gazetteer.get("places", []))
    aliases = sum(len(place.get("aliases", [])) for place in gazetteer.get("places", []))

    with tempfile.TemporaryDirectory(prefix="gazetteer-garlasco-") as temp:
        root = Path(temp)
        baseline_dir = prepare_lexicon(args.gazetteer, False, root)
        enabled_dir = prepare_lexicon(args.gazetteer, True, root)

        levels = {"baseline": [], "enabled": []}
        per_level = {}
        for mode, directory in (("baseline", baseline_dir), ("enabled", enabled_dir)):
            visualner_locations = run_visualner(args.visualner, scenes, directory)
            per_level[("visualner", mode)] = visualner_locations
            levels[mode].append(describe("VisualNER", visualner_locations, gold_names))
            if args.worker:
                manifest_locations = run_worker(args.worker, scenes, directory)
                per_level[("manifest", mode)] = manifest_locations
                levels[mode].append(
                    describe("semantic-engine manifest", manifest_locations, gold_names)
                )

    baseline = per_level[("visualner", "baseline")]
    enabled = per_level[("visualner", "enabled")]
    added = sorted(set(enabled) - set(baseline))
    removed = sorted(set(baseline) - set(enabled))
    precision_checks = []
    for surface in ("Corte", "Come", "Pavia"):
        present = surface in enabled
        if surface in ("Corte", "Come"):
            verdict = "PASS (escluso)" if not present else "FAIL (entità LOCATION)"
        else:
            verdict = "PASS (riconosciuto)" if present else "FAIL (non riconosciuto)"
        precision_checks.append({"surface": surface, "verdict": verdict})

    report = {
        "fixture": str(args.fixture),
        "words": len(text.split()),
        "scenes": len(scenes),
        "scene_words": args.scene_words,
        "places": places,
        "aliases": aliases,
        "levels": levels,
        "added": added,
        "removed": removed,
        "precision_checks": precision_checks,
    }
    args.report.write_text(markdown(report), encoding="utf-8")
    evidence = args.report.with_suffix(".json")
    evidence.write_text(json.dumps(report, ensure_ascii=False, indent=2), encoding="utf-8")
    print(markdown(report))
    print("report:", args.report)
    print("evidence:", evidence)
    return 0


if __name__ == "__main__":
    sys.exit(main())
