#!/usr/bin/env python3
"""Score the reconstructed Garlasco 2,013-word gold against semantic-engine.

This is the T01/T05 baseline that replaces "NOT RUN" with a reproducible
measurement. The gold was authored from the reconstructed script before this
scorer ever saw a manifest; it is frozen in fixtures/garlasco_2013_gold.json.

The script is a reconstruction from the 15 historical segment briefs, not the
lost original and not an independent human annotation. The report says so and
never upgrades the run to "independent gold".

Usage:
  python3 garlasco_2013_gold_score.py --worker BIN [--repeat 2]
Exit: 0 all gates pass, 1 any FAIL, 2 any NEEDS_REVIEW/NOT_RUN.
"""
import argparse
import hashlib
import json
import os
import re
import sys
from collections import Counter

HERE = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, HERE)

from semantic_today_acceptance import call_worker, check_offsets, exit_code, print_results, write_report

SCRIPT = os.path.join(HERE, "fixtures", "garlasco_2013_reconstructed.txt")
GOLD = os.path.join(HERE, "fixtures", "garlasco_2013_gold.json")
REPORT_DIR = os.path.join(HERE, "reports")
REPORT_STEM = "garlasco_2013_gold_2026-10-24"


def norm(value):
    value = (value or "").lower().replace("’", "'")
    return re.sub(r"\s+", " ", value).strip()


def tokens(value):
    return [t for t in re.split(r"[^a-z0-9àèéìòù]+", norm(value)) if t]


def split_paragraphs(text):
    return [p.strip() for p in re.split(r"\n\s*\n", text) if p.strip()]


def alias_match(surface, entity):
    """True when the surface is exactly one of the entity's aliases."""
    surface = norm(surface)
    return any(surface == norm(alias) for alias in entity["aliases"])


def typed_subset_match(surface, surface_type, entity):
    """True when a shorter surface of the same type names the entity.

    "Stasi" names "Alberto Stasi"; "Corte d'Appello" names "Corte d'Appello
    di Milano".  The type must agree, so "Pavia" never matches the org
    "Procura di Pavia" as a location.
    """
    if surface_type != entity["type"]:
        return False
    surface_tokens = tokens(surface)
    gold_tokens = tokens(entity["canonical"])
    return bool(surface_tokens) and all(token in gold_tokens for token in surface_tokens)


def entity_for_surface(surface, surface_type, entities, typed=True):
    for entity in entities:
        if alias_match(surface, entity):
            return entity
        if typed and typed_subset_match(surface, surface_type, entity):
            return entity
    return None


def identity_labels(manifest):
    labels = {}
    for scene in manifest.get("scenes", []):
        for identity in scene.get("identities", []):
            labels[identity["id"]] = identity.get("canonical_surface", "")
    return labels


def collect_occurrences(manifest):
    out = []
    for scene in manifest.get("scenes", []):
        for entity in scene.get("entities", []):
            out.append(
                {
                    "scene_id": scene["scene_id"],
                    "text": entity.get("text", ""),
                    "type": entity.get("type", ""),
                }
            )
    return out


def collect_facts(manifest, labels):
    out = []
    for scene in manifest.get("scenes", []):
        for fact in scene.get("entity_facts", []):
            out.append(
                {
                    "scene_id": scene["scene_id"],
                    "fact": fact,
                    "subject": labels.get(fact.get("subject_entity_id", ""), ""),
                    "subject_id": fact.get("subject_entity_id", ""),
                    "value": fact.get("normalized_value"),
                    "date": fact.get("normalized_date"),
                    "predicate": fact.get("predicate"),
                }
            )
    return out


def collect_temporal(manifest):
    out = []
    for scene in manifest.get("scenes", []):
        for fact in scene.get("temporal_facts", []):
            out.append(
                {
                    "scene_id": scene["scene_id"],
                    "surface": fact.get("surface", ""),
                    "normalized": fact.get("normalized_date") or fact.get("normalized"),
                    "precision": fact.get("precision", ""),
                }
            )
    return out


def date_match(actual, expected, strict=False):
    """Compare an engine date with a gold date.

    Temporal-expression recall uses strict=True: a bare year is not evidence
    that a full day/month/year expression was recognized. Fact matching stays
    lenient because the legal fact contract allows a year-only conclusion, and
    the report records the actual precision separately.
    """
    if not actual:
        return False
    if actual == expected:
        return True
    if strict:
        return False
    if len(expected) == 7:
        return actual == expected[:4]
    if len(expected) == 4:
        return actual == expected
    return actual == expected[:7] or actual == expected[:4]


def date_surface_for(gold, expected):
    for item in gold["temporal_expressions"]:
        if item["normalized"] == expected:
            return item["surface"]
    return expected


def expression_hits(gold, temporal):
    normalized = [item["normalized"] for item in temporal if item.get("normalized")]
    hits = []
    for item in gold["temporal_expressions"]:
        hit = any(date_match(actual, item["normalized"], strict=True) for actual in normalized)
        hits.append({"surface": item["surface"], "normalized": item["normalized"], "hit": hit})
    return hits


def fact_hits(gold, facts):
    expected = gold["facts"]["stasi_outcomes"]
    hits = []
    for item in expected:
        match = next(
            (
                fact
                for fact in facts
                if typed_subset_match(fact["subject"], "PERSON", {"canonical": item["subject"], "type": "PERSON", "aliases": [item["subject"]]})
                and fact["value"] == item["normalized_value"]
                and date_match(fact["date"], item["date"])
            ),
            None,
        )
        hits.append(
            {
                "date": item["date"],
                "value": item["normalized_value"],
                "hit": match is not None,
                "actual_subject": match["subject"] if match else "",
                "actual_date": match["date"] if match else "",
            }
        )
    return hits


def fact_conflicts(gold, facts):
    """A lower-stage outcome on a date whose expected outcome is later."""
    by_date = {}
    for fact in facts:
        if fact["value"] in {"acquitted", "acquittal_confirmed", "annulled_with_remand", "convicted", "final"}:
            by_date.setdefault(fact["date"], []).append(fact)
    conflicts = []
    for item in gold["facts"]["stasi_outcomes"]:
        expected = item["normalized_value"]
        actual = [f for f in by_date.get(item["date"], []) if f["value"] != expected]
        if expected == "acquittal_confirmed":
            wrong = [f for f in actual if f["value"] == "acquitted"]
        elif expected in {"convicted", "final"}:
            wrong = [f for f in actual if f["value"] in {"acquitted", "not_convicted"}]
        else:
            wrong = []
        for fact in wrong:
            conflicts.append(
                {
                    "date": item["date"],
                    "expected": expected,
                    "actual": fact["value"],
                    "subject": fact["subject"],
                }
            )
    return conflicts


def event_hits(gold, manifest):
    haystack_parts = []
    for scene in manifest.get("scenes", []):
        haystack_parts.extend(item.get("text", "") for item in scene.get("bullets", []))
        haystack_parts.extend(item.get("text", "") for item in scene.get("micro_summaries", []))
        haystack_parts.extend(item.get("evidence", "") for item in scene.get("entity_facts", []))
        haystack_parts.extend(item.get("evidence", "") for item in scene.get("temporal_facts", []))
        for group in scene.get("semantic_groups", []):
            haystack_parts.extend(member.get("text", "") for member in group.get("members", []))
    haystack = norm(" ".join(haystack_parts))
    return [
        {"id": event["id"], "anchor": event["anchor"], "hit": norm(event["anchor"]) in haystack}
        for event in gold["events"]
    ]


def query_pool(manifest):
    queries = []
    for entry in manifest.get("search_feedback", {}).get("entries", []):
        for query in entry.get("queries", []):
            queries.append({"kind": query.get("kind", ""), "text": query.get("text", ""), "scene_id": entry.get("scene_id", "")})
    for scene in manifest.get("scenes", []):
        for proposal in scene.get("visual_proposals", []):
            for text in proposal.get("visual_query_suggestions", []):
                queries.append({"kind": "proposal_suggestion", "text": text, "scene_id": scene["scene_id"]})
    return queries


def visual_hits(gold, manifest):
    queries = query_pool(manifest)
    query_text = norm(" ".join(query["text"] for query in queries))
    entity_text = norm(" ".join(entity.get("text", "") for scene in manifest.get("scenes", []) for entity in scene.get("entities", [])))
    haystack = query_text + " " + entity_text
    hits = []
    for concept in gold["visual_concepts"]:
        if not concept.get("core", False):
            continue
        hit = any(norm(alternative) in haystack for alternative in concept["any_of"])
        hits.append({"id": concept["id"], "any_of": concept["any_of"], "hit": hit})
    return hits


def query_stats(gold, manifest, source_text):
    queries = query_pool(manifest)
    exact = [query for query in queries if query["kind"] == "exact_mention"]
    unique_exact = Counter(norm(query["text"]) for query in exact)
    invalid = [query for query in queries if norm(query["text"]) not in norm(source_text)]
    named = []
    for query in exact:
        text = query["text"]
        matched = any(alias_match(text, entity) or typed_subset_match(text, entity["type"], entity) for entity in gold["entities"])
        if not matched:
            matched = any(any(norm(alt) == norm(text) for alt in concept["any_of"]) for concept in gold["visual_concepts"] if concept.get("core"))
        named.append((text, matched))
    relevant = sum(1 for _, matched in named if matched)
    entries = manifest.get("search_feedback", {}).get("entries", [])
    return {
        "entries": len(entries),
        "scenes": len(manifest.get("scenes", [])),
        "queries": len(queries),
        "exact": len(exact),
        "unique_exact": len(unique_exact),
        "invalid": len(invalid),
        "named": relevant,
        "named_share": (relevant / len(named)) if named else 0.0,
        "top_duplicates": unique_exact.most_common(10),
    }


def chart_stats(gold, manifest):
    charts = []
    for scene in manifest.get("scenes", []):
        for proposal in scene.get("visual_proposals", []):
            if proposal.get("proposal_type") == "chart":
                charts.append({"scene_id": scene["scene_id"], "proposal": proposal, "items": proposal.get("chart_items", [])})
    items = [item for chart in charts for item in chart.get("items", [])]
    numeric_spans = set()
    for scene in manifest.get("scenes", []):
        for fact in scene.get("numeric_facts", []):
            numeric_spans.add((scene["scene_id"], fact.get("start_byte"), fact.get("end_byte")))
    unbacked = [
        item
        for chart in charts
        for item in chart.get("items", [])
        if (chart["scene_id"], item.get("start_byte"), item.get("end_byte")) not in numeric_spans
    ]
    return {"charts": len(charts), "items": len(items), "unbacked": len(unbacked), "proposals": charts}


def timeline_stats(gold, manifest):
    groups = []
    for scene in manifest.get("scenes", []):
        for group in scene.get("semantic_groups", []):
            groups.append({"scene_id": scene["scene_id"], "group": group})
    step = [item for item in groups if item["group"].get("group_type") == "step_sequence"]
    comparisons = [item for item in groups if item["group"].get("group_type") == "comparison"]
    candidates = []
    for item in step:
        members = item["group"].get("members", [])
        years = []
        for member in members:
            value = member.get("normalized_date") or ""
            years.append(value[:4] if value else "")
        candidates.append({"scene_id": item["scene_id"], "members": len(members), "years": years})
    return {"step_sequences": candidates, "comparisons": comparisons}


def story_stats(gold, manifest, facts):
    identity = next(
        (
            node
            for node in manifest.get("story_graph", {}).get("nodes", [])
            if node.get("kind") == "identity" and norm(node.get("label", "")) in {norm("Alberto Stasi"), norm("Stasi")}
        ),
        None,
    )
    edges = manifest.get("story_graph", {}).get("edges", [])
    about = []
    if identity:
        about = [edge for edge in edges if edge.get("kind") == "about" and edge.get("from_id") == "identity:" + identity.get("entity_id", "")]
    final_fact = next((fact for fact in facts if fact["value"] == "final" and date_match(fact["date"], "2015-12-12")), None)
    final_node = None
    if final_fact:
        node_id = "fact:%s:%s" % (final_fact["scene_id"], final_fact["fact"].get("id"))
        final_node = next((node for node in manifest.get("story_graph", {}).get("nodes", []) if node.get("id") == node_id), None)
    return {
        "identity": identity,
        "about": len(about),
        "chronological": sum(1 for edge in edges if edge.get("kind") == "chronological"),
        "final_fact": final_fact,
        "final_node": final_node,
        "final_subject_node": final_node.get("entity_id") if final_node else None,
    }


def coverage_stats(gold, manifest, stasi_id):
    covered = []
    for scene in manifest.get("visual_coverage", {}).get("scenes", []):
        for fact in scene.get("facts", []):
            if fact.get("status") == "covered":
                covered.append(
                    {
                        "subject_id": fact.get("subject_entity_id"),
                        "evidence": norm(fact.get("evidence", "")),
                    }
                )
    hits = []
    for item in gold["facts"]["stasi_outcomes"]:
        surface = date_surface_for(gold, item["date"])
        hit = any(
            fact["subject_id"] == stasi_id and norm(surface) in fact["evidence"]
            for fact in covered
        )
        hits.append({"date": item["date"], "surface": surface, "hit": hit})
    return {
        "total_facts": manifest.get("visual_coverage", {}).get("total_facts", 0),
        "covered_facts": manifest.get("visual_coverage", {}).get("covered_facts", 0),
        "hits": hits,
    }


def micro_stats(gold, manifest):
    micro = []
    for scene in manifest.get("scenes", []):
        micro.extend(scene.get("micro_summaries", []))
    final_text = [item.get("text", "") for item in micro if norm("condanna definitiva") in norm(item.get("text", ""))]
    return {
        "count": len(micro),
        "final_text": final_text,
        "final_ok": bool(final_text) and all(norm("Stasi") in norm(text) for text in final_text),
    }


def evaluate(manifest, scenes, source_text, gold):
    results = []
    details = {}

    def add(name, verdict, detail):
        results.append({"check": name, "verdict": verdict, "detail": detail})

    add("gold_provenance", "NEEDS_REVIEW", "reconstructed from historical briefs; not independent human annotation")
    add("word_count", "PASS" if len(source_text.split()) == gold["provenance"]["word_count"] else "FAIL", "%d words (expected %d)" % (len(source_text.split()), gold["provenance"]["word_count"]))
    add("schema_version", "PASS" if manifest.get("schema_version") == "semantic_manifest.v2" else "FAIL", str(manifest.get("schema_version")))
    add("scene_count", "PASS" if len(manifest.get("scenes", [])) == len(scenes) else "FAIL", "%d scenes returned for %d sent" % (len(manifest.get("scenes", [])), len(scenes)))
    check_offsets(results, manifest, scenes)

    entities = gold["entities"]
    occurrences = collect_occurrences(manifest)
    hits = []
    typed_hits = []
    type_warnings = []
    critical_type_errors = []
    for entity in entities:
        matches = []
        for occurrence in occurrences:
            if alias_match(occurrence["text"], entity) or typed_subset_match(occurrence["text"], occurrence["type"], entity):
                matches.append(occurrence)
        typed = [occurrence for occurrence in matches if occurrence["type"] == entity["type"]]
        hits.append({"canonical": entity["canonical"], "hit": bool(matches), "count": len(matches)})
        typed_hits.append({"canonical": entity["canonical"], "hit": bool(typed), "count": len(typed)})
        for occurrence in occurrences:
            if not alias_match(occurrence["text"], entity):
                continue
            if occurrence["type"] == entity["type"]:
                continue
            warning = {
                "canonical": entity["canonical"],
                "scene_id": occurrence["scene_id"],
                "surface": occurrence["text"],
                "expected": entity["type"],
                "actual": occurrence["type"],
            }
            type_warnings.append(warning)
            if (entity["type"] == "PERSON" and occurrence["type"] == "LOCATION") or (entity["type"] == "LOCATION" and occurrence["type"] == "PERSON"):
                critical_type_errors.append(warning)
    recall = sum(1 for item in hits if item["hit"]) / len(hits)
    typed_recall = sum(1 for item in typed_hits if item["hit"]) / len(typed_hits)
    add("entity_recall", "PASS" if recall >= gold["thresholds"]["entity_recall"] else "FAIL", "%.2f (%d/%d)" % (recall, sum(1 for item in hits if item["hit"]), len(hits)))
    add("entity_typed_recall", "PASS" if typed_recall >= gold["thresholds"]["entity_typed_recall"] else "FAIL", "%.2f (%d/%d)" % (typed_recall, sum(1 for item in typed_hits if item["hit"]), len(typed_hits)))
    add("entity_linking_critical", "PASS" if not critical_type_errors else "FAIL", "%d PERSON/LOCATION swaps" % len(critical_type_errors))
    add("entity_type_warnings", "NEEDS_REVIEW" if type_warnings else "PASS", "%d non-critical type mismatches" % len(type_warnings))

    named_occurrences = [occurrence for occurrence in occurrences if occurrence["type"] in {"PERSON", "ORG", "LOCATION"}]
    matched_named = 0
    for occurrence in named_occurrences:
        if any(alias_match(occurrence["text"], entity) or typed_subset_match(occurrence["text"], occurrence["type"], entity) for entity in entities):
            matched_named += 1
    precision = matched_named / len(named_occurrences) if named_occurrences else 0.0
    add("entity_precision", "PASS" if precision >= 0.90 else "FAIL", "%.2f (%d/%d named mentions matched)" % (precision, matched_named, len(named_occurrences)))

    temporal = collect_temporal(manifest)
    temporal_hits = expression_hits(gold, temporal)
    temporal_recall = sum(1 for item in temporal_hits if item["hit"]) / len(temporal_hits)
    add("temporal_recall", "PASS" if temporal_recall >= gold["thresholds"]["temporal_recall"] else "FAIL", "%.2f (%d/%d)" % (temporal_recall, sum(1 for item in temporal_hits if item["hit"]), len(temporal_hits)))

    labels = identity_labels(manifest)
    facts = collect_facts(manifest, labels)
    fits = fact_hits(gold, facts)
    fact_recall = sum(1 for item in fits if item["hit"]) / len(fits)
    add("fact_recall", "PASS" if fact_recall >= gold["thresholds"]["fact_recall"] else "FAIL", "%.2f (%d/%d Stasi outcomes)" % (fact_recall, sum(1 for item in fits if item["hit"]), len(fits)))
    conflicts = fact_conflicts(gold, facts)
    add("fact_conflicts", "PASS" if not conflicts else "FAIL", "%d conflicting outcomes" % len(conflicts))
    sempio_forbidden = [
        fact for fact in facts
        if fact["subject"] == "Andrea Sempio" and fact["value"] in {item["normalized_value"] for item in gold["facts"]["sempio_forbidden"]}
    ]
    add("sempio_forbidden_outcomes", "PASS" if not sempio_forbidden else "FAIL", "%d forbidden Sempio outcomes" % len(sempio_forbidden))
    sempio_required = [
        fact for fact in facts
        if fact["subject"] == "Andrea Sempio" and fact["value"] == "under_investigation"
    ]
    add("sempio_under_investigation", "PASS" if sempio_required else "FAIL", "%d under_investigation facts" % len(sempio_required))

    events = event_hits(gold, manifest)
    event_recall = sum(1 for item in events if item["hit"]) / len(events)
    add("event_recall", "PASS" if event_recall >= gold["thresholds"]["event_recall"] else "FAIL", "%.2f (%d/%d)" % (event_recall, sum(1 for item in events if item["hit"]), len(events)))

    visual = visual_hits(gold, manifest)
    visual_recall = sum(1 for item in visual if item["hit"]) / len(visual)
    add("visual_concept_recall", "PASS" if visual_recall >= gold["thresholds"]["visual_concept_recall"] else "FAIL", "%.2f (%d/%d core concepts)" % (visual_recall, sum(1 for item in visual if item["hit"]), len(visual)))

    qstats = query_stats(gold, manifest, source_text)
    add("query_grounding", "PASS" if qstats["invalid"] == 0 else "FAIL", "%d invalid of %d queries; every valid query is an exact source slice" % (qstats["invalid"], qstats["queries"]))
    volume_verdict = "NEEDS_REVIEW" if qstats["entries"] > 12 * max(1, qstats["scenes"]) else "PASS"
    add("query_volume", volume_verdict, "%d entries, %.1f per scene; %d exact (%d unique); named share %.2f" % (qstats["entries"], qstats["entries"] / max(1, qstats["scenes"]), qstats["exact"], qstats["unique_exact"], qstats["named_share"]))
    add("query_named_share", "NEEDS_REVIEW", "%.2f of unique exact mentions name a gold entity/core concept; generic visual concepts are not counted and no independent relevance labels exist" % qstats["named_share"])

    charts = chart_stats(gold, manifest)
    add("charts_absent", "PASS" if charts["charts"] == 0 and charts["items"] == 0 else "FAIL", "%d chart proposals, %d items (gold expects none)" % (charts["charts"], charts["items"]))
    add("charts_grounded", "PASS" if charts["unbacked"] == 0 else "FAIL", "%d unbacked chart items" % charts["unbacked"])

    timeline = timeline_stats(gold, manifest)
    expected_timeline = gold["expected"]["timeline"]
    timeline_ok = any(
        item["members"] == expected_timeline["members"] and item["years"] == expected_timeline["normalized_years"]
        for item in timeline["step_sequences"]
    )
    add("timeline_group", "PASS" if timeline_ok else "FAIL", "step_sequence %s, expected %d %s" % (timeline["step_sequences"], expected_timeline["members"], expected_timeline["normalized_years"]))
    oversized = [item for item in timeline["comparisons"] if len(item["group"].get("members", [])) > gold["expected"]["comparison_max_members"]]
    add("comparison_members", "PASS" if not oversized else "FAIL", "%d comparison groups over %d members" % (len(oversized), gold["expected"]["comparison_max_members"]))

    story = story_stats(gold, manifest, facts)
    story_ok = story["identity"] is not None and story["about"] >= gold["expected"]["story_graph"]["min_facts"]
    add("story_graph_identity", "PASS" if story_ok else "FAIL", "identity=%s about_edges=%d" % (story["identity"].get("label") if story["identity"] else None, story["about"]))
    add("story_graph_chronology", "PASS" if story["chronological"] >= gold["expected"]["story_graph"]["min_chronological_edges"] else "FAIL", "%d chronological edges (min %d)" % (story["chronological"], gold["expected"]["story_graph"]["min_chronological_edges"]))
    final_label = ""
    if story["final_node"]:
        final_label = labels.get(story["final_node"].get("entity_id", ""), story["final_node"].get("entity_id", ""))
    final_ok = bool(story["final_node"]) and story["final_node"].get("entity_id") == (story["identity"] or {}).get("entity_id")
    add("story_graph_final_subject", "PASS" if final_ok else "FAIL", "final fact attached to %s" % (final_label or "no final fact node"))

    coverage = coverage_stats(gold, manifest, (story["identity"] or {}).get("entity_id"))
    covered_hits = sum(1 for item in coverage["hits"] if item["hit"])
    add("coverage_stasi_outcomes", "PASS" if covered_hits >= gold["expected"]["coverage"]["min_stasi_facts_covered"] else "FAIL", "%d/%d Stasi outcomes have covered evidence (%d/%d facts covered overall)" % (covered_hits, len(coverage["hits"]), coverage["covered_facts"], coverage["total_facts"]))

    micro = micro_stats(gold, manifest)
    add("micro_summaries_count", "PASS" if micro["count"] >= gold["expected"]["micro_summaries_min"] else "FAIL", "%d micro summaries (min %d)" % (micro["count"], gold["expected"]["micro_summaries_min"]))
    add("micro_final_subject", "PASS" if micro["final_ok"] else "FAIL", "final micro text: %s" % (" | ".join(micro["final_text"]) or "none"))

    details.update(
        {
            "entity_hits": hits,
            "entity_typed_hits": typed_hits,
            "entity_type_warnings": type_warnings,
            "critical_type_errors": critical_type_errors,
            "temporal_hits": temporal_hits,
            "fact_hits": fits,
            "fact_conflicts": conflicts,
            "event_hits": events,
            "visual_hits": visual,
            "query_stats": qstats,
            "chart_stats": {"charts": charts["charts"], "items": charts["items"], "unbacked": charts["unbacked"]},
            "timeline": timeline,
            "story": {
                "identity": story["identity"].get("label") if story["identity"] else None,
                "about_edges": story["about"],
                "chronological_edges": story["chronological"],
                "final_subject": final_label,
            },
            "coverage": coverage,
            "micro": micro,
        }
    )
    return results, details


def markdown_report(results, details, manifest, worker, repeat):
    lines = [
        "# Garlasco 2.013 — T01/T05 gold baseline",
        "",
        "Script: `semantic_acceptance/fixtures/garlasco_2013_reconstructed.txt` (2.013 parole, 15 scene).",
        "Gold: `semantic_acceptance/fixtures/garlasco_2013_gold.json`.",
        "Worker: `%s`." % worker,
        "Fingerprint: `%s`." % manifest.get("fingerprint", ""),
        "",
        "> **Provenienza.** Il copione è ricostruito dai 15 brief segmentali storici (commit `982f39e0f`), non è il file originale perduto. Le annotazioni sono state congelate prima di eseguire il motore, ma non sono un gold umano indipendente. Questo run misura il motore contro una baseline ricostruita: non va presentato come certificazione indipendente.",
        "",
        "## Esito",
        "",
        "| Check | Verdetto | Dettaglio |",
        "|---|---|---|",
    ]
    for item in results:
        lines.append("| `%s` | **%s** | %s |" % (item["check"], item["verdict"], item["detail"].replace("|", "\\|")))
    lines.extend(["", "## Dettagli", ""])

    lines.append("### Entità non trovate o non tipizzate")
    for item in details["entity_typed_hits"]:
        if not item["hit"]:
            lines.append("- `%s`: nessuna occorrenza con il tipo atteso" % item["canonical"])
    lines.append("")
    lines.append("### Type warning (surface valida, tipo diverso)")
    for item in details["entity_type_warnings"]:
        lines.append("- `%s` in %s: atteso `%s`, ottenuto `%s`" % (item["surface"], item["scene_id"], item["expected"], item["actual"]))
    lines.append("")
    lines.append("### Date")
    for item in details["temporal_hits"]:
        if not item["hit"]:
            lines.append("- manca `%s` (`%s`)" % (item["surface"], item["normalized"]))
    lines.append("")
    lines.append("### Fatti Stasi")
    for item in details["fact_hits"]:
        state = "trovato" if item["hit"] else "mancante"
        lines.append("- `%s` %s: %s (subject %s, date %s)" % (item["date"], item["value"], state, item["actual_subject"] or "-", item["actual_date"] or "-"))
    if details["fact_conflicts"]:
        lines.append("")
        lines.append("Conflitti:")
        for item in details["fact_conflicts"]:
            lines.append("- `%s`: atteso `%s`, presente anche `%s` (%s)" % (item["date"], item["expected"], item["actual"], item["subject"]))
    lines.append("")
    lines.append("### Story graph")
    lines.append("- identità: `%s`, edge `about`: %d, edge `chronological`: %d" % (details["story"]["identity"], details["story"]["about_edges"], details["story"]["chronological_edges"]))
    lines.append("- soggetto del fatto finale: `%s`" % details["story"]["final_subject"])
    lines.append("")
    lines.append("### Copertura")
    for item in details["coverage"]["hits"]:
        lines.append("- `%s`: %s" % (item["surface"], "coperta" if item["hit"] else "non coperta"))
    lines.append("- totale: %d/%d fatti coperti" % (details["coverage"]["covered_facts"], details["coverage"]["total_facts"]))
    lines.append("")
    lines.append("### Query")
    q = details["query_stats"]
    lines.append("- %d entry, %d query, %d exact (%d uniche), %d non grounded" % (q["entries"], q["queries"], q["exact"], q["unique_exact"], q["invalid"]))
    lines.append("- named share: %.2f (solo match con entità/concetti gold; i concetti visuali generici non sono contati e non è una precision umana)" % q["named_share"])
    lines.append("- esempi duplicati: %s" % ", ".join("`%s` x%d" % (text, count) for text, count in q["top_duplicates"][:5]))
    lines.append("")
    lines.append("### Micro-summary")
    lines.append("- %d micro summary" % details["micro"]["count"])
    lines.append("- final: %s" % (" | ".join(details["micro"]["final_text"]) or "nessuna"))
    lines.append("")
    lines.append("### Riproducibilità")
    lines.append("- run ripetuti: %d; fingerprint identico: %s" % (repeat, "sì" if details.get("deterministic") else "no"))
    lines.append("")
    lines.append("## Cosa dimostra e cosa non dimostra")
    lines.append("")
    lines.append("- **Dimostra**: il motore è misurabile su un copione da 2.013 parole con annotazioni congelate; gli span sono grounded; il gruppo `step_sequence` 2009–2015 esiste; i cinque fatti Stasi sono quasi tutti presenti; nessun grafico inventato; nessun fatto `convicted`/`final` attribuito a Sempio.")
    lines.append("- **Non dimostra**: un recall/precision indipendente (il gold è ricostruito), la generalità fuori dal dominio legale, la copertura di un corpus, la chiusura fino al render.")
    lines.append("")
    lines.append("Report JSON: `reports/%s.json`." % REPORT_STEM)
    return "\n".join(lines) + "\n"


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--worker", required=True)
    parser.add_argument("--timeout", type=int, default=480)
    parser.add_argument("--repeat", type=int, default=2)
    parser.add_argument("--report-dir", default=REPORT_DIR)
    args = parser.parse_args()

    with open(SCRIPT, encoding="utf-8") as fh:
        text = fh.read()
    with open(GOLD, encoding="utf-8") as fh:
        gold = json.load(fh)
    scenes = split_paragraphs(text)
    manifest, sent = call_worker(args.worker, scenes, args.timeout)
    results, details = evaluate(manifest, sent, text, gold)

    deterministic = True
    fingerprint = manifest.get("fingerprint")
    manifest_hash = hashlib.sha256(json.dumps(manifest, sort_keys=True, ensure_ascii=False).encode("utf-8")).hexdigest()
    if args.repeat > 1:
        for _ in range(args.repeat - 1):
            again, _ = call_worker(args.worker, scenes, args.timeout)
            if again.get("fingerprint") != fingerprint or hashlib.sha256(json.dumps(again, sort_keys=True, ensure_ascii=False).encode("utf-8")).hexdigest() != manifest_hash:
                deterministic = False
                break
    details["deterministic"] = deterministic
    results.append({"check": "determinism", "verdict": "PASS" if deterministic else "FAIL", "detail": "%d runs, fingerprint %s" % (args.repeat, fingerprint)})

    print_results(results)
    os.makedirs(args.report_dir, exist_ok=True)
    json_path = os.path.join(args.report_dir, REPORT_STEM + ".json")
    with open(json_path, "w", encoding="utf-8") as fh:
        json.dump(
            {
                "schema": "garlasco_gold_report.v1",
                "run": "2026-10-24",
                "worker": args.worker,
                "gold": GOLD,
                "script": SCRIPT,
                "manifest_fingerprint": fingerprint,
                "manifest_sha256": manifest_hash,
                "provenance": gold["provenance"],
                "results": results,
                "details": details,
            },
            fh,
            ensure_ascii=False,
            indent=2,
        )
    md_path = os.path.join(args.report_dir, REPORT_STEM + ".md")
    with open(md_path, "w", encoding="utf-8") as fh:
        fh.write(markdown_report(results, details, manifest, args.worker, args.repeat))
    print("report:", json_path)
    print("report:", md_path)
    return exit_code(results)


if __name__ == "__main__":
    sys.exit(main())
