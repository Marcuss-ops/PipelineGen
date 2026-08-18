#!/usr/bin/env python3
"""generate-certification/report.py — certification report for one /api/script/generate run.

Assembles the certification report (the shape requested for the T01-T10 suite) from:

  1. the job full response (status, events, script text, entities),
  2. the aggregate research report persisted in SQLite `research_cache`
     (ranking info, evidence pack with per-candidate claims/sources),
  3. derived checks: unsupported numbers in the script vs the sourced text,
     cross-candidate claim contamination, name/alias coverage.

Usage:
  report.py --scenario <scenario.json> --response <response.json> \
            --script <script.txt> --db <sqlite.db> --out <report.json>

Exit code 0 => report written (result may be PASS, PASS_WITH_NOTE or FAIL);
exit 1 => report generation error.
"""

from __future__ import annotations

import argparse
import json
import re
import sqlite3
import sys
import unicodedata
from pathlib import Path

MONEY_RE = re.compile(
    r"\$\s?(\d[\d,.]*)\s?(billion|b|million|m|thousand|k)?",
    re.IGNORECASE,
)
BARE_AMOUNT_RE = re.compile(
    r"(?<![\w$])(\d[\d,.]*)\s?(billion|b|million|m)\b",
    re.IGNORECASE,
)

# Script-side markers used by the identity-collision test (T05).
BASKETBALL_MARKERS = [
    "bulls", "nba", "basketball", "championship", "wizards", "hornets",
    "six championships", "ncaa", "space jam", "air jordan", "nike", "baseball",
]
ACTING_MARKERS = [
    "actor", "hollywood", "creed", "black panther", "fantastic four",
    "fruitvale", "just mercy", "film", "movie", "screen", "producer", "award for acting",
]


def norm(s: str) -> str:
    """Lowercase, strip diacritics, drop non-alphanumerics (alias-insensitive match)."""
    s = unicodedata.normalize("NFKD", s)
    s = "".join(c for c in s if not unicodedata.combining(c))
    return re.sub(r"[^a-z0-9]+", "", s.lower())


def num_value(tok: str) -> float:
    try:
        return float(tok.replace(",", ""))
    except ValueError:
        return float("nan")


def money_tokens(text: str):
    """Yield (value, unit) money mentions from a text."""
    out = []
    for m in MONEY_RE.finditer(text):
        unit = (m.group(2) or "").lower()
        if unit in ("b", "billion"):
            unit = "billion"
        elif unit in ("m", "million"):
            unit = "million"
        elif unit in ("k", "thousand"):
            unit = "thousand"
        out.append((num_value(m.group(1)), unit))
    for m in BARE_AMOUNT_RE.finditer(text):
        unit = m.group(2).lower()
        if unit == "b":
            unit = "billion"
        elif unit == "m":
            unit = "million"
        out.append((num_value(m.group(1)), unit))
    return out


def find_job_status(full: dict) -> str:
    return str(
        full.get("status")
        or (full.get("job") or {}).get("status")
        or (full.get("result") or {}).get("status")
        or ""
    )


def find_script(full: dict) -> str:
    candidates = [
        full.get("result", {}).get("data", {}).get("result", {}).get("output", {}).get("text"),
        full.get("job", {}).get("result", {}).get("data", {}).get("result", {}).get("output", {}).get("text"),
        full.get("result", {}).get("data", {}).get("result", {}).get("text"),
    ]
    for c in candidates:
        if isinstance(c, str) and c.strip():
            return c
    return ""


def find_entities(full: dict) -> dict:
    """Locate the entities postprocessor payload across known response shapes."""
    for path in (
        ("result", "data", "result", "artifacts", "entities"),
        ("result", "data", "data", "artifacts", "entities"),
        ("result", "data", "result", "output", "entities"),
    ):
        node = full
        ok = True
        for key in path:
            if not isinstance(node, dict) or key not in node:
                ok = False
                break
            node = node[key]
        if ok and isinstance(node, dict):
            return node
    return {}


def load_research_record(db_path: str, topic: str) -> tuple[dict | None, str]:
    """Return (parsed research_report_json, source_text) from research_cache."""
    if not db_path or not Path(db_path).exists():
        return None, ""
    try:
        con = sqlite3.connect(f"file:{db_path}?mode=ro", uri=True, timeout=10)
        # research_cache.source_text may hold non-UTF-8 bytes (scraped pages);
        # decode lossily instead of letting the default str factory raise.
        con.text_factory = lambda b: b.decode("utf-8", errors="replace")
    except sqlite3.Error as exc:
        print(f"report.py: cannot open db {db_path}: {exc}", file=sys.stderr)
        return None, ""
    try:
        rows = con.execute(
            "SELECT research_report_json, source_text FROM research_cache "
            "WHERE topic = ? ORDER BY updated_at DESC LIMIT 5",
            (topic,),
        ).fetchall()
    except sqlite3.Error as exc:
        print(f"report.py: research_cache query failed: {exc}", file=sys.stderr)
        rows = []
    finally:
        con.close()
    for raw, source_text in rows:
        if not raw:
            continue
        try:
            report = json.loads(raw)
        except json.JSONDecodeError:
            continue
        if isinstance(report, dict):
            return report, source_text or ""
    return None, ""


def aggregate_report(report: dict | None) -> dict | None:
    if not report:
        return None
    if report.get("ranking"):
        return report
    if report.get("evidence") and report.get("mode") == "multi_candidate":
        return report
    return None


def count_verified(report: dict) -> tuple[int, int]:
    claims = report.get("claims") or []
    verified = sum(1 for c in claims if c.get("verified"))
    return verified, len(claims) - verified


def evidence_candidates(report: dict) -> list[dict]:
    if not report:
        return []
    if isinstance(report.get("evidence"), dict):
        return report["evidence"].get("candidates") or []
    return []


def claims_for(report: dict) -> list[dict]:
    return report.get("claims") or []


RANK_WORD = {
    "first": 1, "one": 1, "1st": 1, "1": 1,
    "second": 2, "two": 2, "2nd": 2, "2": 2,
    "third": 3, "three": 3, "3rd": 3, "3": 3,
    "fourth": 4, "four": 4, "4th": 4, "4": 4,
    "fifth": 5, "five": 5, "5th": 5, "5": 5,
    "sixth": 6, "six": 6, "6th": 6, "6": 6,
    "seventh": 7, "seven": 7, "7th": 7, "7": 7,
    "eighth": 8, "eight": 8, "8th": 8, "8": 8,
    "ninth": 9, "nine": 9, "9th": 9, "9": 9,
    "tenth": 10, "ten": 10, "10th": 10, "10": 10,
}

RANK_WORD_ALT = (
    r"first|second|third|fourth|fifth|sixth|seventh|eighth|ninth|tenth|"
    r"one|two|three|four|five|six|seven|eight|nine|ten|"
    r"1st|2nd|3rd|4th|5th|6th|7th|8th|9th|10th|1|2|3|4|5|6|7|8|9|10"
)
# "number nine" / "rank 5" / "at number one" (keyword first)
# and "the second position" / "fifth spot" (ordinal first).
RANK_RE = re.compile(
    r"(?:rank|position|spot|place|slot|number|no\.?)\s*(?:is|was|at|#|\s)?\s*"
    r"(" + RANK_WORD_ALT + r")|"
    r"(the\s+)?(" + RANK_WORD_ALT + r")\s+(?:position|spot|place|rank|slot)",
    re.IGNORECASE,
)


def script_rank_order(script: str, candidates: list[str]) -> list[tuple[int, str]]:
    """Extract (rank, candidate) pairs from explicit rank phrases in the script.

    E.g. "At number nine, we encounter Cristiano Ronaldo" → (9, "Cristiano Ronaldo").
    For each rank phrase we take the NEAREST candidate mentioned after it
    (within a short window), so a rank never steals the next sentence's subject.
    """
    found: dict[int, str] = {}
    low = script.lower()
    cand_low = [(norm(c), c) for c in candidates if norm(c)]
    for m in RANK_RE.finditer(low):
        word = (m.group(1) or m.group(3) or "").lower()
        rank = RANK_WORD.get(word)
        if rank is None or rank in found:
            continue
        tail = norm(low[m.end():m.end() + 120])
        best: tuple[int, str] | None = None
        for c_norm, c_raw in cand_low:
            idx = tail.find(c_norm)
            if idx >= 0 and (best is None or idx < best[0]):
                best = (idx, c_raw)
        if best is not None:
            found[rank] = best[1]
    return sorted((r, c) for r, c in found.items())


def contamination_claims(report: dict, candidates: list[str]) -> tuple[int, list[str]]:
    """Claims of candidate A whose subject migrated to candidate B.

    A claim is contaminated only when it mentions ANOTHER candidate's name
    without mentioning its own candidate's name — i.e. the evidence's primary
    subject is the wrong person. A passing disambiguation mention ("Michael
    B. Jordan, not to be confused with basketball's Michael Jordan") is not
    contamination: the claim's own name is present.
    """
    cand_norm = {norm(c): c for c in candidates}
    hits = []
    for cand in evidence_candidates(report):
        me = norm(cand.get("candidate_id") or cand.get("label") or "")
        for claim in cand.get("claims") or []:
            text = (claim.get("text") or "").lower()
            if not text:
                continue
            text_norm = norm(text)
            for other_norm, other_raw in cand_norm.items():
                if other_norm == me or not other_norm:
                    continue
                if other_norm in text_norm and (not me or me not in text_norm):
                    hits.append((cand.get("label"), other_raw, text[:160]))
    return len(hits), hits


def unsupported_numbers(script: str, source_text: str) -> int:
    """Count money tokens in the script with no counterpart in the sourced text."""
    if not script.strip():
        return 0
    src_flat = re.sub(r"[^0-9a-z]+", "", source_text.lower())
    misses = 0
    for value, unit in money_tokens(script):
        key = re.sub(r"[^0-9]", "", f"{value:g}")
        if not key:
            continue
        # Look for the bare digits and (best-effort) the unit inside the source.
        if key in src_flat:
            continue
        if unit and unit in source_text.lower() and key in src_flat:
            continue
        misses += 1
    return misses


def script_contamination(script: str, candidates: list[str]) -> tuple[int, list[str]]:
    """Sentence-level identity bleed for the two-Jordans test (T05)."""
    if len(candidates) != 2:
        return 0, []
    a, b = candidates
    n_a, n_b = norm(a), norm(b)
    violations = []
    for sentence in re.split(r"(?<=[.!?])\s+", script):
        low = sentence.lower()
        mentions_b = n_b and n_b in norm(low)
        mentions_a = n_a and n_a in norm(low)
        if mentions_b:
            for marker in BASKETBALL_MARKERS:
                if marker in low:
                    violations.append(f"basketball marker '{marker}' in sentence about {b}: {sentence[:140]}")
                    break
        elif mentions_a:
            for marker in ACTING_MARKERS:
                if marker in low:
                    violations.append(f"acting marker '{marker}' in sentence about {a}: {sentence[:140]}")
                    break
    return len(violations), violations


def names_in_script(script: str, names: list[str]) -> tuple[int, list[str]]:
    """Count names present in the script (accent/alias-insensitive)."""
    s = norm(script)
    missing = []
    for name in names:
        if norm(name) not in s:
            missing.append(name)
    return len(names) - len(missing), missing


def quality_event(full: dict, event_type: str) -> dict | None:
    for ev in full.get("events") or []:
        if ev.get("type") == event_type:
            return ev
    return None


def main() -> int:
    ap = argparse.ArgumentParser()
    ap.add_argument("--scenario", required=True)
    ap.add_argument("--response", required=True)
    ap.add_argument("--script", required=True)
    ap.add_argument("--db", default="")
    ap.add_argument("--out", required=True)
    args = ap.parse_args()

    scenario = json.loads(Path(args.scenario).read_text())
    full = json.loads(Path(args.response).read_text())
    script = Path(args.script).read_text(errors="replace") if Path(args.script).exists() else ""

    payload = scenario["payload"]
    item = payload["items"][0]
    source = item["source"]
    topic = source.get("topic", "")
    candidates = (source.get("research") or {}).get("candidates") or []
    requested_metric = (source.get("research") or {}).get("ranking_metric") or None
    freshness_days = (source.get("research") or {}).get("freshness_days", 0)
    assertions = scenario.get("assertions") or {}
    test_id = scenario.get("case_prefix", "t??").split("-")[0]

    status = find_job_status(full)
    job_id = full.get("id") or (full.get("job") or {}).get("id") or ""
    error = str(full.get("error") or "") or str((full.get("job") or {}).get("error") or "")

    gen_ev = quality_event(full, "script.generated")
    qual_ev = quality_event(full, "quality.checked")
    comp_ev = quality_event(full, "job.completed")

    # ── Research report from the SQLite cache ──────────────────────────────
    report, source_text = load_research_record(args.db, topic)
    agg = aggregate_report(report)
    ranking = (agg or {}).get("ranking")
    cands_ev = evidence_candidates(agg or report or {})
    verified, rejected = count_verified(agg or report or {})
    research_queries = (report or {}).get("queries") or []
    research_sources = (report or {}).get("sources") or []
    pages_fetched = (report or {}).get("pages_fetched", 0)
    pages_failed = (report or {}).get("pages_failed", 0)
    rejected_sources = (report or {}).get("rejected_sources", 0)

    # ── Derived checks ─────────────────────────────────────────────────────
    unsupported = unsupported_numbers(script, source_text)
    foreign_claims, foreign_list = contamination_claims(agg or report or {}, candidates or [])
    script_bleed, bleed_list = script_contamination(script, candidates or [])
    present, missing_names = names_in_script(script, candidates or []) if candidates else (0, [])
    money_hits = len(money_tokens(script))

    entities = find_entities(full)
    concepts = entities.get("concepts") or []
    important_phrases = entities.get("important_phrases") or []
    entity_note = None
    if entities:
        entity_note = "entities postprocessor emits KEYWORD concepts + important phrases only; typed PERSON/ORGANIZATION/MONEY/DATE/STATISTIC extraction is not exposed by the current pipeline"

    # ── Per-test assertions ────────────────────────────────────────────────
    notes: list[str] = []
    result = "PASS"

    def fail(reason: str) -> None:
        nonlocal result
        result = "FAIL"
        notes.append(reason)

    def warn(reason: str) -> None:
        nonlocal result
        if result == "PASS":
            result = "PASS_WITH_NOTE"
        notes.append(reason)

    # Common gate.
    if status not in ("SUCCEEDED", "SUCCEEDED_WITH_WARNINGS", "COMPLETED"):
        fail(f"job terminal status {status!r}" + (f" — {error}" if error else ""))
    else:
        words = len(script.split())
        min_w, max_w = assertions.get("min_words", 0), assertions.get("max_words", 0)
        if min_w and words < min_w:
            warn(f"word count {words} below target floor {min_w}")
        if max_w and words > max_w:
            warn(f"word count {words} above target ceiling {max_w}")

    if agg is None and (report or {}).get("mode") == "web_research":
        notes.append("single-topic research run (no candidate fanout): ranking section N/A by design")
    elif ranking is None and agg is not None:
        warn("candidate fanout ran but the persisted research report carries no ranking info")
    if requested_metric and ranking:
        if ranking.get("resolved_metric") != requested_metric:
            fail(
                f"requested ranking_metric {requested_metric!r} resolved to {ranking.get('resolved_metric')!r} "
                "(unsupported metric normalizes to generic)"
            )
    dropped = (agg or report or {}).get("dropped_candidates") or []
    if dropped:
        names = ", ".join(d.get("candidate_id", "?") for d in dropped)
        if ranking and ranking.get("uncertain"):
            warn(f"{len(dropped)} candidate(s) dropped at research gate (partial ranking, uncertain): {names}")
        else:
            warn(f"{len(dropped)} candidate(s) dropped at research gate: {names}")
    if freshness_days:
        notes.append(f"freshness_days={freshness_days} folded into research cache fingerprint (per-candidate page validation)")

    # T01 / T10 — annual-earnings athletes.
    if test_id in ("t01", "t10"):
        if candidates and present < len(candidates):
            fail(f"script missing {len(missing_names)} candidate(s): {', '.join(missing_names)}")
        if money_hits == 0:
            warn("script contains no money figures")
        if test_id == "t10" and (not entities or (len(concepts) == 0 and len(important_phrases) == 0)):
            warn("extract_entities enabled but no entities found in the job result")

    # Ground-truth ranking order (T01 / T10 golden checks).
    expected_order = assertions.get("expected_order") or []
    if test_id in ("t01", "t10") and expected_order:
        ranked = script_rank_order(script, expected_order)
        if not ranked:
            warn("no explicit rank phrases found in the script; ground-truth order NOT verified")
        else:
            got = [c for _, c in ranked]
            exp = expected_order[:len(got)]
            mismatches = [
                (r, e, g)
                for (r, _), e, g in zip(ranked, exp, got)
                if norm(e) != norm(g)
            ]
            if mismatches:
                detail = "; ".join(f"#{r}: expected {e}, got {g}" for r, e, g in mismatches)
                fail(f"ranking order diverges from ground truth: {detail}")
            elif len(got) < len(expected_order):
                notes.append(
                    f"ranking order verified for {len(got)}/{len(expected_order)} positions "
                    f"(script only states explicit ranks for the top {len(got)})"
                )
            else:
                notes.append(
                    f"ranking order matches ground truth for all {len(got)} explicit positions"
                )

    # T05 — identity collision.
    if test_id == "t05":
        if foreign_claims > 0:
            fail(f"{foreign_claims} cross-candidate claim contamination (claims mention another candidate)")
        if script_bleed > 0:
            fail(f"{script_bleed} sentence-level identity bleed (careers mixed in the script)")
        if present < len(candidates):
            fail(f"script missing {len(missing_names)} candidate(s): {', '.join(missing_names)}")

    # T06 — aliases/diacritics: every candidate must be a distinct identity and appear.
    if test_id == "t06":
        if len(cands_ev) != len(candidates):
            warn(f"evidence pack has {len(cands_ev)}/{len(candidates)} candidates")
        if present < len(candidates):
            fail(f"script missing {len(missing_names)} boxer(s): {', '.join(missing_names)}")

    # T07 — metric purity (net worth only).
    if test_id == "t07":
        if ranking and ranking.get("resolved_metric") != "estimated_net_worth":
            fail(f"resolved metric {ranking.get('resolved_metric')!r} != estimated_net_worth")
        if unsupported > 3:
            fail(f"{unsupported} money figures in the script are unsupported by the sourced research text")
        if ranking:
            notes.append(
                f"ranking strategy={ranking.get('strategy')} fallback_used={ranking.get('fallback_used')} "
                f"candidates_with_evidence={ranking.get('candidates_with_evidence')}"
            )

    # T08 — insufficient evidence must degrade, never invent.
    if test_id == "t08":
        if status in ("FAILED", "CANCELLED", "DEAD_LETTER"):
            notes.append("job failed closed at research/ranking phase (no invented values)")
            result = "PASS"
        elif ranking:
            if ranking.get("uncertain"):
                notes.append(f"ranking marked uncertain (candidates_with_evidence={ranking.get('candidates_with_evidence')})")
            elif ranking.get("candidates_with_evidence", 0) < len(candidates or []):
                warn(f"partial evidence: {ranking.get('candidates_with_evidence')}/{len(candidates)} candidates with evidence")
            else:
                notes.append(
                    f"full sourced evidence found ({ranking.get('candidates_with_evidence')}/{len(candidates or [])}); "
                    "ranking is claim-based, not invented"
                )
        if unsupported > 5:
            fail(f"{unsupported} money figures in the script are unsupported by the sourced research text")

    # T04 / T09 — factual non-ranking + temporal separation.
    if test_id in ("t04", "t09"):
        for kw in assertions.get("keywords", []):
            if norm(kw) not in norm(script):
                fail(f"script missing required keyword {kw!r}")
        if test_id == "t09" and "anora" not in norm(script):
            warn("script does not mention Anora (2025 Best Picture) — temporal comparison may be incomplete")

    # Semantic-index counts (informational).
    semantic = {
        "persons": None,
        "organizations": None,
        "money": None,
        "dates": None,
        "statistics": None,
        "important_phrases": len(important_phrases) if entities else None,
        "concepts": len(concepts) if entities else None,
        "extract_entities": bool(entities),
    }
    if entity_note:
        notes.append(entity_note)

    report_out = {
        "test_id": test_id,
        "title": scenario.get("name", ""),
        "result": result,
        "job": {
            "id": job_id,
            "status": status,
            "error": error or None,
            "word_count": len(script.split()),
            "model": (gen_ev.get("data") or {}).get("model") if gen_ev else None,
            "cache_status": (gen_ev.get("data") or {}).get("cache_status") if gen_ev else None,
            "duration_ms": (comp_ev.get("data") or {}).get("total_ms") if comp_ev else None,
            "quality_gate": (qual_ev.get("data") or {}).get("passed") if qual_ev else None,
            "unsupported_claims": (qual_ev.get("data") or {}).get("unsupported_claims") if qual_ev else None,
        },
        # ── Certification template ──────────────────────────────────────────
        "research_candidates": len(candidates) if candidates else (len(cands_ev) or 0),
        "research_passed": len(cands_ev) if cands_ev else (len(research_sources) if research_sources else 0),
        "verified_claims": verified,
        "rejected_claims": rejected,
        "provider_usage": {},
        "ranking": ranking or None,
        "dropped_candidates": (agg or report or {}).get("dropped_candidates") or [],
        "writer": {"calls": 1 if gen_ev else 0},
        "grounding": {
            "unsupported_numbers": unsupported,
            "foreign_candidate_claims": foreign_claims,
            "script_identity_bleed": script_bleed,
        },
        "semantic_index": semantic,
        "research": {
            "mode": (report or {}).get("mode"),
            "cache_hit": (report or {}).get("cache_hit"),
            "queries": len(research_queries),
            "sources": len(research_sources),
            "pages_fetched": pages_fetched,
            "pages_failed": pages_failed,
            "rejected_sources": rejected_sources,
            "requested_metric": requested_metric,
            "freshness_days": freshness_days,
        },
        "candidates": {
            "provided": len(candidates),
            "evidence": len(cands_ev),
            "present_in_script": present,
            "missing": missing_names,
        },
        "notes": notes,
        "_details": {
            "claim_contamination": foreign_list[:10],
            "script_contamination": bleed_list[:10],
        },
    }
    Path(args.out).parent.mkdir(parents=True, exist_ok=True)
    Path(args.out).write_text(json.dumps(report_out, ensure_ascii=False, indent=2))
    print(f"{test_id}: result={result} status={status} words={len(script.split())} "
          f"candidates={len(cands_ev)}/{len(candidates) if candidates else 0} "
          f"unsupported={unsupported} foreign_claims={foreign_claims} bleed={script_bleed}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
