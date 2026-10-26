#!/usr/bin/env python3
"""Acceptance harness for the Rust semantic-engine worker (semantic_manifest.v2).

Subcommands:
  selftest                      check the harness itself on a built-in sample
  run   --worker BIN            full battery on the Garlasco fixture
  custom --text-file F --worker BIN [--scene-words N] [--gold G]
  charts --worker BIN           T05: digit/spelled comparisons and dates

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
    """Split text into scenes of roughly scene_words words, on sentence bounds.

    An unpunctuated ASR passage can be thousands of words long. It is cut on
    word boundaries at the scene budget instead of being sent as one sentence:
    production segments by time, so a single multi-thousand-word "sentence" is
    an artifact of the transcript, not a scene the planner ever sees.
    """
    sentences = []
    for sentence in re.split(r"(?<=[.!?])\s+", text.strip()):
        words = sentence.split()
        if len(words) <= scene_words * 2:
            sentences.append(sentence)
            continue
        for start in range(0, len(words), scene_words):
            sentences.append(" ".join(words[start:start + scene_words]))
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


def _scene_bytes(scenes):
    return {s["scene_id"]: s["text"].encode("utf-8") for s in scenes}


def check_story_graph(results, manifest, scenes):
    """Every node and edge must resolve to a grounded source span."""
    graph = manifest.get("story_graph") or {}
    nodes = graph.get("nodes") or []
    edges = graph.get("edges") or []
    facts = sum(len(s.get("entity_facts", [])) for s in manifest["scenes"])
    if not facts:
        check(results, "story_graph", "NOT_RUN", "no grounded fact to link in this script")
        return
    if not nodes or not edges:
        check(results, "story_graph", "FAIL", "manifest carries no story graph")
        return
    by_id = {node.get("id"): node for node in nodes}
    raw = _scene_bytes(scenes)
    bad = 0
    for node in nodes:
        text = raw.get(node.get("scene_id"))
        if text is None:
            bad += 1
            continue
        sliced = text[node.get("start_byte", 0):node.get("end_byte", 0)].decode("utf-8", "replace")
        if sliced != node.get("evidence"):
            bad += 1
        for scene_id in node.get("scene_ids") or []:
            if scene_id not in raw:
                bad += 1
    for edge in edges:
        if edge.get("from_id") not in by_id or edge.get("to_id") not in by_id:
            bad += 1
    cross = sum(1 for edge in edges if edge.get("cross_scene"))
    if cross == 0 and len(scenes) < 2:
        check(results, "story_graph", "NOT_RUN",
              "%d nodes, %d edges, one scene cannot cross" % (len(nodes), len(edges)))
        return
    if cross == 0:
        check(results, "story_graph", "NEEDS_REVIEW",
              "%d nodes, %d edges, none crossing a scene" % (len(nodes), len(edges)))
        return
    check(results, "story_graph", "PASS" if bad == 0 else "FAIL",
          "%d nodes, %d edges (%d cross-scene), %d unresolved" % (len(nodes), len(edges), cross, bad))


def check_visual_coverage(results, manifest, scenes):
    """Coverage must reference real facts and agree with its own totals."""
    coverage = manifest.get("visual_coverage") or {}
    entries = coverage.get("scenes") or []
    if len(entries) != len(scenes):
        check(results, "visual_coverage", "FAIL",
              "%d coverage entries for %d scenes" % (len(entries), len(scenes)))
        return
    fact_ids = {
        s["scene_id"]: {f["id"] for f in s.get("entity_facts", [])}
        for s in manifest["scenes"]
    }
    total = 0
    covered = 0
    bad = 0
    for entry in entries:
        known = fact_ids.get(entry.get("scene_id"), set())
        for item in entry.get("facts") or []:
            total += 1
            status = item.get("status")
            types = item.get("proposal_types") or []
            if item.get("fact_id") not in known:
                bad += 1
            if (status == "covered") != bool(types):
                bad += 1
            if status not in ("covered", "uncovered"):
                bad += 1
            if status == "covered":
                covered += 1
    if coverage.get("total_facts") != total or coverage.get("covered_facts") != covered:
        bad += 1
    if total == 0:
        check(results, "visual_coverage", "NEEDS_REVIEW", "no fact to cover")
        return
    check(results, "visual_coverage", "PASS" if bad == 0 else "FAIL",
          "%d facts, %d covered, %d uncovered, %d bad" % (total, covered, total - covered, bad))


def check_search_feedback(results, manifest, scenes):
    """Every search rung is a source slice of the scene it belongs to."""
    entries = (manifest.get("search_feedback") or {}).get("entries") or []
    if not entries:
        check(results, "search_feedback", "NOT_RUN",
              "no image-backed proposal in this script")
        return
    raw = _scene_bytes(scenes)
    proposal_ids = {
        s["scene_id"]: {p["id"] for p in s.get("visual_proposals", [])}
        for s in manifest["scenes"]
    }
    bad = 0
    widest = 0
    for entry in entries:
        scene_id = entry.get("scene_id")
        queries = entry.get("queries") or []
        widest = max(widest, len(queries))
        if entry.get("proposal_id") not in proposal_ids.get(scene_id, set()):
            bad += 1
        if not queries:
            bad += 1
        if entry.get("status") != "pending":
            bad += 1
        for query in queries:
            text = raw.get(scene_id, b"")
            if not query_is_grounded(text, query):
                bad += 1
    check(results, "search_feedback", "PASS" if bad == 0 else "FAIL",
          "%d entries, widest ladder %d rungs, %d bad" % (len(entries), widest, bad))


def query_is_grounded(raw, query):
    """Validate a literal source query or a composed query's exact spans."""
    text = query.get("text") or ""
    if not raw or not text:
        return False
    origin = query.get("origin", "literal")
    if origin == "literal":
        return text.encode("utf-8") in raw
    if origin not in ("derived_from_source_context", "derived_from_semantic_template"):
        return False
    evidence = query.get("evidence") or ""
    start, end = query.get("evidence_start_byte"), query.get("evidence_end_byte")
    if start is None or end is None or start > end or end > len(raw):
        return False
    if raw[start:end].decode("utf-8", "replace") != evidence:
        return False
    components = query.get("components") or []
    if len(components) < 2:
        return False
    for component in components:
        left, right = component.get("start_byte"), component.get("end_byte")
        if left is None or right is None or left < start or right > end or left > right:
            return False
        if raw[left:right].decode("utf-8", "replace") != component.get("text"):
            return False
    ordered = sorted(components, key=lambda item: item["start_byte"])
    source_terms = " ".join(item["text"].strip() for item in ordered)
    if origin == "derived_from_source_context":
        return source_terms == text
    if not query.get("template_id") or not query.get("illustrative") or not query.get("derived_terms"):
        return False
    expected = sorted((source_terms + " " + " ".join(query["derived_terms"])).casefold().split())
    return expected == sorted(text.casefold().split())


def check_chronology(results, manifest, scenes, gold):
    """The emitted dated facts must match the documented sequence, in order.

    This is the difference between `source_grounded` and `externally_verified`:
    the engine is not asked to know history, only to reproduce the script's own
    timeline. The gold states what the corrected script says.
    """
    expected = [(step["date"], step["outcome"]) for step in gold.get("sequence") or []]
    if not expected:
        check(results, "chronology_gold", "NOT_RUN", "gold carries no sequence")
        return
    dated = []
    bad = 0
    raw = _scene_bytes(scenes)
    for scene_manifest, scene in zip(manifest["scenes"], scenes):
        for fact in scene_manifest.get("entity_facts", []):
            date = fact.get("normalized_date")
            outcome = fact.get("normalized_value")
            if not date or not outcome:
                continue
            start = fact["evidence_start_byte"]
            end = fact["evidence_end_byte"]
            if raw[scene["scene_id"]][start:end].decode("utf-8", "replace") != fact["evidence"]:
                bad += 1
            dated.append((date, outcome))
    dated.sort()
    actual = [(date[:4], outcome) for date, outcome in dated]
    verdict = "PASS" if actual == expected and bad == 0 else "FAIL"
    detail = "expected %s, got %s" % (expected, actual)
    if bad:
        detail += ", %d ungrounded evidence spans" % bad
    check(results, "chronology_gold", verdict, detail)


def check_temporal_recall(results, manifest, gold):
    """Every temporal expression in the script must be recovered, even when no
    entity fact is attached to it: five dated facts do not prove the 2007 and
    2025 references were seen."""
    expected = gold.get("temporal_expressions") or []
    if not expected:
        check(results, "temporal_recall", "NOT_RUN", "gold carries no temporal expressions")
        return
    counts = {}
    unresolved = []
    for scene_manifest in manifest["scenes"]:
        for fact in scene_manifest.get("temporal_facts", []):
            normalized = fact.get("normalized_date")
            if normalized:
                counts[normalized] = counts.get(normalized, 0) + 1
            else:
                unresolved.append(fact.get("surface") or "")
    missing = []
    for entry in expected:
        normalized = entry["normalized"]
        # A finer expression (2025-05-24) also proves the coarser year, never
        # the opposite: a bare "2025" does not prove the day.
        found = counts.get(normalized, 0)
        if len(normalized) == 4:
            found += sum(
                count for value, count in counts.items()
                if len(value) > 4 and value.startswith(normalized)
            )
        if found < entry.get("min_count", 1):
            missing.append("%s (%d/%d)" % (normalized, found, entry.get("min_count", 1)))
    allowed = set(gold.get("unresolved_allowed") or [])
    bad_unresolved = [surface for surface in unresolved if surface not in allowed]
    verdict = "PASS" if not missing and not bad_unresolved else "FAIL"
    detail = "%d expressions checked, %d temporal facts" % (len(expected), len(counts))
    if missing:
        detail += ", missing: " + ", ".join(missing)
    if bad_unresolved:
        detail += ", unexpected unresolved: " + ", ".join(sorted(set(bad_unresolved)))
    check(results, "temporal_recall", verdict, detail)


NAMED_TYPES = {
    "PERSON",
    "ORGANIZATION",
    "ORG",
    "LOCATION",
    "FACILITY",
    "BRAND",
    "PRODUCT",
    "WORK",
}
MEASUREMENT_TYPES = {"NUMBER", "PERCENT", "MONEY", "DATE"}


def is_heading_surface(text):
    """Editorial headings ("NOME CHE TORNA", "RITROVAMENTO") are not names."""
    words = [word for word in text.split() if any(ch.isalpha() for ch in word)]
    if not words:
        return False
    letters = ["".join(ch for ch in word if ch.isalpha()) for word in words]
    if not all(letter.isupper() for letter in letters if letter):
        return False
    return len(words) >= 2 or len(letters[0]) >= 6


def scene_entity_types(manifest):
    """(scene_id, lowercase surface) -> type for every grounded entity."""
    types = {}
    for scene in manifest["scenes"]:
        for entity in scene.get("entities", []):
            types[(scene["scene_id"], entity["text"].lower())] = entity["type"]
    return types


def check_entity_linking(results, manifest, scenes):
    """T01: a name fragment is never a place, and a heading is never an entity."""
    persons = [e for s in manifest["scenes"] for e in s.get("entities", []) if e["type"] == "PERSON"]
    name_tokens = {
        token.lower()
        for person in persons
        for token in person["text"].split()
        if len(token) > 1
    }
    fragments = [
        entity["text"]
        for scene in manifest["scenes"]
        for entity in scene.get("entities", [])
        if entity["type"] == "LOCATION"
        and " " not in entity["text"]
        and entity["text"].lower() in name_tokens
    ]
    headings = [
        entity["text"]
        for scene in manifest["scenes"]
        for entity in scene.get("entities", [])
        if is_heading_surface(entity["text"])
    ]
    problems = []
    if fragments:
        problems.append("name fragments typed LOCATION: " + ", ".join(sorted(set(fragments))[:5]))
    if headings:
        problems.append("headings typed as entities: " + ", ".join(sorted(set(headings))[:5]))
    verdict = "PASS" if not problems else "FAIL"
    detail = "%d person names, %d locations" % (
        len(persons),
        sum(1 for s in manifest["scenes"] for e in s.get("entities", []) if e["type"] == "LOCATION"),
    )
    if problems:
        detail += ", " + "; ".join(problems)
    check(results, "entity_linking", verdict, detail)


def check_comparison_precision(results, manifest):
    """T02: a comparison is exactly two named subjects, never a noun list."""
    types = scene_entity_types(manifest)
    groups = [
        (scene["scene_id"], group)
        for scene in manifest["scenes"]
        for group in scene.get("semantic_groups", [])
        if group["group_type"] == "comparison"
    ]
    if not groups:
        check(results, "comparison_precision", "NOT_RUN", "no comparison group in this script")
        return
    bad = []
    for scene_id, group in groups:
        members = group.get("members") or []
        if len(members) != 2:
            bad.append("%s: %d members" % (group["id"], len(members)))
            continue
        for member in members:
            kind = types.get((scene_id, member["text"].lower()))
            if member.get("scene_id"):
                kind = types.get((member["scene_id"], member["text"].lower())) or kind
            if kind not in NAMED_TYPES:
                bad.append("%s: %r is %s, not a named subject" % (group["id"], member["text"], kind))
    verdict = "PASS" if not bad else "FAIL"
    detail = "%d comparisons" % len(groups)
    if bad:
        detail += ", " + "; ".join(bad[:4])
    check(results, "comparison_precision", verdict, detail)


def check_timeline_completeness(results, manifest):
    """T03: one complete timeline, distinct events, and every dated fact covered."""
    coverage = {
        entry["scene_id"]: entry
        for entry in (manifest.get("visual_coverage") or {}).get("scenes", [])
    }
    sequenced = []
    bad = []
    for scene in manifest["scenes"]:
        groups = scene.get("semantic_groups", [])
        sequences = [g for g in groups if g["group_type"] == "step_sequence"]
        chronologies = [g for g in groups if g["group_type"] == "chronological"]
        for group in sequences + chronologies:
            dates = [m.get("normalized_date") for m in group.get("members") or [] if m.get("normalized_date")]
            if len(dates) != len(set(dates)):
                bad.append("%s: duplicate dates %s" % (group["id"], dates))
            # Two outcomes can share one source sentence ("nel 2009 assolto,
            # nel 2011 confermato"); two *identical members* cannot.
            texts = [m.get("text") for m in group.get("members") or []]
            if len(texts) != len(set(texts)):
                bad.append("%s: duplicate events" % group["id"])
        if not sequences:
            continue
        sequenced.append(scene["scene_id"])
        timelines = [
            p
            for p in scene.get("visual_proposals", [])
            if p["proposal_type"] == "timeline"
        ]
        if len(timelines) != 1:
            bad.append("%s: %d timelines for one sequence" % (scene["scene_id"], len(timelines)))
        facts = [
            item
            for item in coverage.get(scene["scene_id"], {}).get("facts", [])
            if item.get("status") == "uncovered"
        ]
        if facts:
            bad.append(
                "%s: %d uncovered facts (%s)"
                % (scene["scene_id"], len(facts), facts[0]["fact_id"])
            )
    if not sequenced:
        check(results, "timeline_completeness", "NOT_RUN", "no dated sequence in this script")
        return
    verdict = "PASS" if not bad else "FAIL"
    detail = "%d sequenced scenes" % len(sequenced)
    if bad:
        detail += ", " + "; ".join(bad[:4])
    check(results, "timeline_completeness", verdict, detail)


def check_keyword_quality(results, manifest):
    """T04: every exact-mention query is a grounded visual entity, not noise."""
    entries = (manifest.get("search_feedback") or {}).get("entries") or []
    if not entries:
        check(results, "keyword_quality", "NOT_RUN", "no search entry in this script")
        return
    types = scene_entity_types(manifest)
    visual = {key for key, kind in types.items() if kind not in MEASUREMENT_TYPES}
    bad = []
    checked = 0
    for entry in entries:
        seen = set()
        for query in entry.get("queries") or []:
            text = query.get("text") or ""
            if not text or text.lower() in seen:
                bad.append("duplicate or empty query in %s" % entry["proposal_id"])
                continue
            seen.add(text.lower())
            if query.get("kind") != "exact_mention":
                continue
            checked += 1
            key = (entry.get("scene_id"), text.lower())
            if key not in visual:
                kind = types.get(key)
                bad.append(
                    "%r is %s, not a searchable visual entity"
                    % (text, kind if kind else "not an entity")
                )
    verdict = "PASS" if not bad else "FAIL"
    detail = "%d exact mentions of %d entries" % (checked, len(entries))
    if bad:
        detail += ", " + "; ".join(sorted(set(bad))[:4])
    check(results, "keyword_quality", verdict, detail)


def check_consolidation(results, manifest, scenes):
    """Semantic Consolidation v1: inventory, selectivity and coverage invariant."""
    consolidation = manifest.get("visual_consolidation") or {}
    if not consolidation:
        check(results, "consolidation", "FAIL", "visual_consolidation missing")
        return
    texts = {scene["scene_id"]: scene["text"] for scene in scenes}
    candidates = consolidation.get("production_candidates") or []
    facts = consolidation.get("facts") or []
    inventory = consolidation.get("inventory") or {}
    metrics = consolidation.get("metrics") or {}
    raw_facts = sum(len(scene.get("entity_facts") or []) for scene in manifest["scenes"])
    raw_proposals = sum(len(scene.get("visual_proposals") or []) for scene in manifest["scenes"])
    raw_queries = sum(len(entry.get("queries") or []) for entry in (manifest.get("search_feedback") or {}).get("entries", []))
    bad = []
    if consolidation.get("schema") != "visual_consolidation.v1":
        bad.append("schema=%r" % consolidation.get("schema"))
    if inventory.get("facts") != raw_facts or inventory.get("proposals") != raw_proposals or inventory.get("queries") != raw_queries:
        bad.append("inventory does not match raw collections")
    if inventory.get("canonical_facts") != len(facts):
        bad.append("canonical fact inventory mismatch")
    if metrics.get("production_candidates") != len(candidates):
        bad.append("candidate metric mismatch")
    if metrics.get("canonical_facts") != len(facts):
        bad.append("fact metric mismatch")
    covered = sum(1 for fact in facts if fact.get("representation"))
    if metrics.get("covered_facts") != covered or metrics.get("uncovered_facts") != len(facts) - covered:
        bad.append("coverage metric mismatch")
    if metrics.get("covered_facts", 0) + metrics.get("uncovered_facts", 0) != len(facts):
        bad.append("coverage totals disagree")
    canonical_ids = {fact.get("id") for fact in facts}
    candidate_ids = {candidate.get("id") for candidate in candidates}
    seen_dedup = set()
    production_queries = 0
    for candidate in candidates:
        scene_text = texts.get(candidate.get("scene_id"))
        if scene_text is None:
            bad.append("candidate %s unknown scene" % candidate.get("id"))
            continue
        if candidate.get("dedup_key") in seen_dedup:
            bad.append("duplicate candidate %s" % candidate.get("id"))
        seen_dedup.add(candidate.get("dedup_key"))
        raw = scene_text.encode("utf-8")
        start, end = candidate.get("start_byte"), candidate.get("end_byte")
        if start is None or end is None or start > end or end > len(raw) or raw[start:end].decode("utf-8", "replace") != candidate.get("evidence"):
            bad.append("candidate %s ungrounded evidence" % candidate.get("id"))
        for fact_id in candidate.get("covered_fact_ids") or []:
            if fact_id not in canonical_ids:
                bad.append("candidate %s unknown fact %s" % (candidate.get("id"), fact_id))
        production_queries += len(candidate.get("queries") or [])
        for query in candidate.get("queries") or []:
            if not query_is_grounded(raw, query):
                bad.append("candidate %s ungrounded query" % candidate.get("id"))
    for fact in facts:
        representation = fact.get("representation")
        if not representation:
            if not (fact.get("uncovered_reason") or "").strip():
                bad.append("fact %s uncovered without reason" % fact.get("id"))
            continue
        candidate_id = representation.get("candidate_id")
        if candidate_id not in candidate_ids:
            bad.append("fact %s unknown representation" % fact.get("id"))
    if metrics.get("production_queries") != production_queries:
        bad.append("query metric mismatch")
    verdict = "PASS" if not bad else "FAIL"
    detail = "%d candidates from %d proposals; %d/%d canonical facts; %d queries from %d" % (
        len(candidates), raw_proposals, covered, len(facts), production_queries, raw_queries)
    if bad:
        detail += "; " + "; ".join(bad[:4])
    check(results, "consolidation", verdict, detail)
    selective = (
        len(candidates) <= raw_proposals
        and metrics.get("estimated_search_jobs", 0) <= metrics.get("raw_search_entries", 0)
        and metrics.get("production_queries", 0) <= raw_queries + metrics.get("production_search_candidates", 0)
    )
    check(
        results,
        "consolidation_selectivity",
        "PASS" if selective else "FAIL",
        "%d candidates, %d search jobs, %d queries (raw: %d proposals, %d entries, %d queries)"
        % (len(candidates), metrics.get("estimated_search_jobs", 0), metrics.get("production_queries", 0),
           raw_proposals, metrics.get("raw_search_entries", 0), raw_queries),
    )


def evaluate(worker, text, scene_words, timeout, gold=None):
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
    check_story_graph(results, manifest, scenes)
    check_visual_coverage(results, manifest, scenes)
    check_search_feedback(results, manifest, scenes)
    check_entity_linking(results, manifest, scenes)
    check_comparison_precision(results, manifest)
    check_timeline_completeness(results, manifest)
    check_keyword_quality(results, manifest)
    check_consolidation(results, manifest, scenes)
    if gold:
        check_chronology(results, manifest, scenes, gold)
        check_temporal_recall(results, manifest, gold)
    return results, manifest


CHART_FIXTURES = {
    "digits": os.path.join(HERE, "fixtures", "charts_digits.txt"),
    "spelled": os.path.join(HERE, "fixtures", "charts_spelled.txt"),
    "dates": os.path.join(HERE, "fixtures", "charts_dates.txt"),
}


def chart_items_of(scene):
    return [item for vp in scene.get("visual_proposals", []) for item in vp.get("chart_items") or []]


def charts_case(worker, path, timeout):
    with open(path, encoding="utf-8") as fh:
        text = fh.read()
    manifest, scenes = call_worker(worker, [text], timeout)
    return manifest["scenes"][0], scenes[0]


def check_chart_fixtures(results, worker, timeout):
    """T05: a stated comparison becomes a chart, a date never becomes a bar."""
    expected = {"digits": "42%", "spelled": "quarantadue"}
    for name, path in CHART_FIXTURES.items():
        scene, sent = charts_case(worker, path, timeout)
        items = chart_items_of(scene)
        if name == "dates":
            dates = scene.get("temporal_facts") or []
            verdict = "PASS" if not items and dates else "FAIL"
            check(
                results,
                "charts_dates_never_bars",
                verdict,
                "%d temporal facts, %d chart items" % (len(dates), len(items)),
            )
            continue
        percents = [item for item in items if item["kind"] == "PERCENT"]
        values = sorted(item["value"] for item in percents)
        marker = expected[name]
        spelled = any(ch.isalpha() for item in percents for ch in item["text"])
        ok = values == [18.0, 42.0] and ((name == "spelled") == spelled)
        check(
            results,
            "charts_%s" % name,
            "PASS" if ok else "FAIL",
            "values %s from %s" % (values, [item["text"] for item in percents]),
        )
        numeric = {(f["start_byte"], f["end_byte"]) for f in scene.get("numeric_facts") or []}
        unbacked = [
            item["text"]
            for item in items
            if (item["start_byte"], item["end_byte"]) not in numeric
        ]
        check(
            results,
            "charts_%s_grounded" % name,
            "PASS" if not unbacked else "FAIL",
            "%d items, %d unbacked (%s)" % (len(items), len(unbacked), marker),
        )


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
    gold = None
    if args.gold:
        with open(args.gold, encoding="utf-8") as fh:
            gold = json.load(fh)
    results, _ = evaluate(args.worker, text, args.scene_words, args.timeout, gold)
    print_results(results)
    print("report:", write_report("custom.json", results))
    return exit_code(results)


def cmd_charts(args):
    results = []
    check_chart_fixtures(results, args.worker, args.timeout)
    print_results(results)
    print("report:", write_report("charts.json", results))
    return exit_code(results)


def main():
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    sub = parser.add_subparsers(dest="cmd", required=True)
    for name in ("selftest", "run", "custom", "charts"):
        p = sub.add_parser(name)
        p.add_argument("--worker", required=True)
        p.add_argument("--timeout", type=int, default=480)
        if name == "custom":
            p.add_argument("--text-file", required=True)
            p.add_argument("--scene-words", type=int, default=220)
            p.add_argument("--gold", help="chronology/temporal gold JSON for this script")
    args = parser.parse_args()
    handler = {
        "selftest": cmd_selftest,
        "run": cmd_run,
        "custom": cmd_custom,
        "charts": cmd_charts,
    }[args.cmd]
    sys.exit(handler(args))


if __name__ == "__main__":
    main()
