#!/usr/bin/env python3
"""Regenerate the 3,127-word Garlasco report with the current engine.

The 2026-10-23 report showed pre-fix symptoms (RITROVAMENTO/Chiara/Poggi as
LOCATION, a five-member comparison group) and presented the fixture's own
2014/2016/2017/2019/2020 chronology without the caveat that those years are in
the source text. This report reruns the same fixture and separates:

  * what the engine now does (T01 symptom checks, facts, groups, coverage);
  * what the fixture itself says (the chronology is the fixture's, not an
    engine inference; the corrected 2009-2015 fixture is separate).

Usage:
  python3 garlasco_3k_report.py --worker BIN [--out PATH]
"""
import argparse
import json
import os
import re
import sys
from collections import Counter

HERE = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, HERE)

from semantic_today_acceptance import call_worker, split_scenes

FIXTURE = os.path.join(HERE, "fixtures", "sempio_chiara_poggi_youtube_3k.txt")
REPORT_DIR = os.path.join(HERE, "reports")
DEFAULT_OUT = os.path.join(REPORT_DIR, "SEMANTIC_3K_REPORT_2026-10-24.md")
JSON_OUT = os.path.join(REPORT_DIR, "semantic_3k_2026-10-24.json")


def norm(value):
    return re.sub(r"\s+", " ", (value or "").lower().replace("’", "'")).strip()


def fact_rows(manifest):
    labels = {}
    for scene in manifest["scenes"]:
        for identity in scene.get("identities", []):
            labels[identity["id"]] = identity.get("canonical_surface", identity["id"])
    rows = []
    for scene in manifest["scenes"]:
        for fact in scene.get("entity_facts", []):
            subject_id = fact.get("subject_entity_id")
            rows.append(
                {
                    "scene": scene["scene_id"],
                    "predicate": fact.get("predicate"),
                    "value": fact.get("normalized_value"),
                    "date": fact.get("normalized_date"),
                    "status": fact.get("status"),
                    "evidence": fact.get("evidence", ""),
                    "subject_id": subject_id,
                    "subject": labels.get(subject_id, subject_id),
                }
            )
    return rows


def entity_type_counts(manifest):
    counts = Counter()
    samples = {}
    for scene in manifest["scenes"]:
        for entity in scene.get("entities", []):
            kind = entity.get("type", "")
            counts[kind] += 1
            samples.setdefault(kind, [])
            if entity.get("text") and entity["text"] not in samples[kind]:
                samples[kind].append(entity["text"])
    return counts, samples


def bad_locations(manifest):
    out = []
    banned = {"ritrovamento", "chiara", "poggi"}
    for scene in manifest["scenes"]:
        for entity in scene.get("entities", []):
            if entity.get("type") == "LOCATION" and norm(entity.get("text")) in banned:
                out.append({"scene_id": scene["scene_id"], "text": entity.get("text")})
    return out


def comparison_groups(manifest):
    out = []
    for scene in manifest["scenes"]:
        for group in scene.get("semantic_groups", []):
            if group.get("group_type") == "comparison":
                out.append(
                    {
                        "scene_id": scene["scene_id"],
                        "members": len(group.get("members", [])),
                        "title": group.get("title", ""),
                        "texts": [member.get("text", "") for member in group.get("members", [])],
                    }
                )
    return out


def timeline_proposals(manifest):
    out = []
    for scene in manifest["scenes"]:
        for proposal in scene.get("visual_proposals", []):
            if proposal.get("proposal_type") == "timeline":
                out.append(
                    {
                        "scene_id": scene["scene_id"],
                        "id": proposal.get("id"),
                        "surfaces": proposal.get("temporal_surfaces", []),
                        "evidence": proposal.get("evidence", ""),
                    }
                )
    return out


def query_stats(manifest):
    entries = manifest.get("search_feedback", {}).get("entries", [])
    exact = Counter()
    for entry in entries:
        for query in entry.get("queries", []):
            if query.get("kind") == "exact_mention":
                exact[norm(query.get("text", ""))] += 1
    return {
        "entries": len(entries),
        "exact": sum(exact.values()),
        "unique_exact": len(exact),
        "top": exact.most_common(20),
    }


def consolidation_summary(manifest):
    """The selective production view over the complete 3K inventory."""
    consolidation = manifest.get("visual_consolidation") or {}
    candidates = consolidation.get("production_candidates") or []
    return {
        "schema": consolidation.get("schema", ""),
        "inventory": consolidation.get("inventory") or {},
        "metrics": consolidation.get("metrics") or {},
        "candidates": len(candidates),
        "timelines": sum(1 for c in candidates if c.get("recipe") == "timeline"),
        "cards": sum(1 for c in candidates if c.get("recipe") == "fact_card"),
        "canonical_facts": len(consolidation.get("facts") or []),
        "covered_facts": sum(1 for fact in consolidation.get("facts") or [] if fact.get("representation")),
        "uncovered_facts": sum(
            1
            for fact in consolidation.get("facts") or []
            if not fact.get("representation") and not (fact.get("uncovered_reason") or "").strip()
        ),
    }


def build_report(manifest, scenes, worker, fixture, text):
    counts, samples = entity_type_counts(manifest)
    bad = bad_locations(manifest)
    comparisons = comparison_groups(manifest)
    timelines = timeline_proposals(manifest)
    facts = fact_rows(manifest)
    micro = [item for scene in manifest["scenes"] for item in scene.get("micro_summaries", [])]
    groups = [
        {"scene_id": scene["scene_id"], "group": group}
        for scene in manifest["scenes"]
        for group in scene.get("semantic_groups", [])
    ]
    lines = [
        "# Test 3.127 parole — cosa ha trovato il semantic engine (2026-10-24)",
        "",
        "Fixture: `%s` (%d parole, %d scene da ~220 parole)." % (fixture, len(text.split()), len(scenes)),
        "Worker: `%s`." % worker,
        "Fingerprint: `%s`." % manifest.get("fingerprint", ""),
        "",
        "> **Nota sulla cronologia.** Questa fixture contiene nel proprio testo gli anni 2014, 2016, 2017, 2019 e 2020. Il motore li estrae fedelmente: non sono un errore di normalizzazione. La fixture separata `garlasco_chronology_corrected.txt` è quella che verifica la sequenza documentata 2009–2015. Il vecchio report mescolava le due cose.",
        "",
        "## T01 — sintomi pre-fix",
        "",
        "- LOCATION vietate (`RITROVAMENTO`, `Chiara`, `Poggi`): **%d** %s" % (len(bad), bad if bad else "— nessuna"),
        "- Gruppi `comparison` con più di 2 membri: **%d** %s" % (len([c for c in comparisons if c["members"] > 2]), [(c["scene_id"], c["members"], c["title"]) for c in comparisons if c["members"] > 2]),
        "- Entità per tipo: %s" % ", ".join("%s=%d" % (kind, count) for kind, count in sorted(counts.items())),
        "",
    ]
    if not bad and not [c for c in comparisons if c["members"] > 2]:
        lines.append("**Esito T01: i due sintomi del report 2026-10-23 non sono più presenti su questa fixture.**")
    else:
        lines.append("**Esito T01: restano sintomi; vedi elenco sopra.**")
    lines.extend(["", "## Fatti (`entity_facts`)", ""])
    if not facts:
        lines.append("- nessun fatto")
    for fact in facts:
        lines.append(
            "- **%s** · `%s` · soggetto `%s` · valore `%s` · data `%s` · status `%s`"
            % (fact["scene"], fact["predicate"], fact["subject"], fact["value"], fact["date"], fact["status"])
        )
    lines.extend(["", "## Micro-summary", ""])
    if not micro:
        lines.append("- nessuna")
    for item in micro:
        lines.append("- «%s» · `%s` · data `%s`" % (item.get("text", ""), item.get("kind", ""), item.get("normalized_date", "")))
    lines.extend(["", "## Timeline proposte", ""])
    if not timelines:
        lines.append("- nessuna")
    for item in timelines:
        lines.append("- %s · `%s` · superfici %s" % (item["scene_id"], item["id"], item["surfaces"]))
        lines.append("  > %s" % item["evidence"])
    lines.extend(["", "## Story graph", ""])
    for node in manifest.get("story_graph", {}).get("nodes", []):
        lines.append(
            "- `%s` · %s · «%s» · scene %s · data %s"
            % (node.get("id"), node.get("kind"), node.get("label"), node.get("scene_ids"), node.get("normalized_date"))
        )
    for edge in manifest.get("story_graph", {}).get("edges", []):
        lines.append(
            "- edge `%s`: `%s` → `%s` · cross_scene=%s"
            % (edge.get("kind"), edge.get("from_id"), edge.get("to_id"), edge.get("cross_scene"))
        )
    lines.extend(["", "## Visual coverage", ""])
    for scene in manifest.get("visual_coverage", {}).get("scenes", []):
        for fact in scene.get("facts", []):
            lines.append(
                "- %s · `%s` · **%s** · %s · importanza %.2f"
                % (scene["scene_id"], fact.get("fact_id"), fact.get("status"), fact.get("proposal_types"), fact.get("importance", 0.0))
            )
    lines.append(
        "- totale: %d/%d fatti coperti"
        % (manifest.get("visual_coverage", {}).get("covered_facts", 0), manifest.get("visual_coverage", {}).get("total_facts", 0))
    )
    c = consolidation_summary(manifest)
    inventory = c["inventory"]
    metrics = c["metrics"]
    lines.extend(["", "## Consolidamento semantico", ""])
    lines.append(
        "- schema `%s`: %d candidati di produzione da %d proposte raw"
        % (c["schema"] or "assente", c["candidates"], inventory.get("proposals", 0))
    )
    lines.append(
        "- fatti canonici: %d da %d; date canoniche: %d da %d; micro canoniche: %d da %d"
        % (
            inventory.get("canonical_facts", 0),
            inventory.get("facts", 0),
            inventory.get("canonical_dates", 0),
            inventory.get("temporal_facts", 0),
            inventory.get("canonical_micro_summaries", 0),
            inventory.get("micro_summaries", 0),
        )
    )
    lines.append(
        "- job di ricerca: %d (da %d entry); query di produzione: %d (da %d); timeline: %d; fact card: %d"
        % (
            metrics.get("estimated_search_jobs", 0),
            metrics.get("raw_search_entries", 0),
            metrics.get("production_queries", 0),
            inventory.get("queries", 0),
            c["timelines"],
            c["cards"],
        )
    )
    lines.append(
        "- copertura canonica: %d/%d fatti rappresentati; %d fatti senza rappresentazione né motivo"
        % (c["covered_facts"], c["canonical_facts"], c["uncovered_facts"])
    )
    lines.extend(["", "## Gruppi semantici", ""])
    if not groups:
        lines.append("- nessuno")
    for item in groups:
        group = item["group"]
        lines.append("- %s · %s · «%s» · %d membri" % (item["scene_id"], group.get("group_type"), group.get("title"), len(group.get("members", []))))
        for member in group.get("members", [])[:8]:
            lines.append("  - %s" % member.get("text", ""))
    lines.extend(["", "## Keyword (`important_words`) più frequenti", ""])
    words = Counter()
    for scene in manifest["scenes"]:
        for word in scene.get("important_words", []):
            words[word.get("text", "")] += 1
    lines.append(", ".join("%s ×%d" % (word, count) for word, count in words.most_common(30)))
    lines.extend(["", "## Query", ""])
    q = query_stats(manifest)
    lines.append("- %d entry, %d exact_mention (%d uniche)" % (q["entries"], q["exact"], q["unique_exact"]))
    lines.append("- duplicati principali: %s" % ", ".join("`%s` ×%d" % (word, count) for word, count in q["top"][:10]))
    lines.extend(["", "## Entità per tipo (campione)", ""])
    for kind, count in sorted(counts.items()):
        lines.append("- **%s** (%d menzioni): %s" % (kind, count, ", ".join(samples.get(kind, [])[:20])))
    lines.extend(
        [
            "",
            "## Cosa cambia rispetto al report 2026-10-23",
            "",
            "- Il report vecchio elencava `RITROVAMENTO`, `Chiara` e `Poggi` come `LOCATION`: qui la verifica è esplicita e il risultato è sopra.",
            "- Il report vecchio mostrava un gruppo `comparison` da 5 membri: qui la verifica è esplicita e il risultato è sopra.",
            "- Gli anni 2014/2016/2017/2019/2020 restano quelli scritti nella fixture; per la sequenza corretta usare `garlasco_chronology_corrected.txt`.",
            "- Il numero di query raw resta alto: è un problema di prodotto (budget del planner), non un errore di grounding. La consolidazione lo riduce a livello di produzione: vedi la sezione dedicata.",
            "",
        ]
    )
    return "\n".join(lines) + "\n", {
        "fixture": fixture,
        "words": len(text.split()),
        "scenes": len(scenes),
        "fingerprint": manifest.get("fingerprint"),
        "bad_locations": bad,
        "comparisons": comparisons,
        "facts": facts,
        "micro_count": len(micro),
        "timelines": timelines,
        "query_stats": q,
        "consolidation": c,
        "entity_counts": dict(counts),
    }


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--worker", required=True)
    parser.add_argument("--fixture", default=FIXTURE)
    parser.add_argument("--scene-words", type=int, default=220)
    parser.add_argument("--timeout", type=int, default=600)
    parser.add_argument("--out", default=DEFAULT_OUT)
    args = parser.parse_args()

    with open(args.fixture, encoding="utf-8") as fh:
        text = fh.read()
    scenes = split_scenes(text, args.scene_words)
    manifest, sent = call_worker(args.worker, scenes, args.timeout)
    report, summary = build_report(manifest, sent, args.worker, args.fixture, text)
    os.makedirs(os.path.dirname(args.out), exist_ok=True)
    with open(args.out, "w", encoding="utf-8") as fh:
        fh.write(report)
    with open(JSON_OUT, "w", encoding="utf-8") as fh:
        json.dump(summary, fh, ensure_ascii=False, indent=2)
    print(report)
    print("report:", args.out)
    print("report:", JSON_OUT)
    return 0


if __name__ == "__main__":
    sys.exit(main())
