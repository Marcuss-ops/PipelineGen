#!/usr/bin/env python3
"""Corpus acceptance for the Rust semantic worker.

Runs the worker over every downloaded transcript in the checkout and checks the
parts that can be certified without independent human annotation:

  * contract        schema version, one manifest scene per request scene;
  * grounding       every entity/bullet/phrase/quote/group/fact/proposal span
                    slices back to its own source text (UTF-8 bytes);
  * structure       fact subjects resolve to an identity, group members resolve
                    to their scene, story-graph nodes/edges resolve, coverage
                    totals agree with the per-fact statuses;
  * measurements    every 4-digit year and every percentage written in digits is
                    recovered as a temporal/numeric fact — an independent regex
                    pass, not the engine's own parser;
  * determinism     an optional re-run produces a byte-identical manifest.

Semantic entity/phrase relevance is NOT scored here: it needs independently
annotated gold, which this checkout does not contain. Scripts with few facts are
reported, never failed: sparse scripts are a quality observation, a broken span
is a defect.

Usage:
  python3 corpus_acceptance.py --worker BIN [--limit N] [--out PATH]
"""
import argparse
import glob
import json
import os
import re
import resource
import subprocess
import sys
import time

HERE = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, HERE)

from semantic_today_acceptance import call_worker, split_scenes

REPO_ROOT = os.path.dirname(HERE)
DEFAULT_ROOTS = [
    "data/tclip/transcripts",
    "data/tclip/poggi/transcripts",
    "ops/jobs/hiphop_36/cache/transcripts",
]
REPORT_DIR = os.path.join(HERE, "reports")
YEAR_RE = re.compile(r"(?<![\d.,])((?:19|20)\d{2})(?![\d.,])")
PERCENT_RE = re.compile(r"(?<![\d.,])(\d{1,3}(?:[.,]\d+)?)\s*%")


def merge_cues(pieces):
    """Join subtitle cues without duplicating their repeated tails.

    YouTube cues are frequently cumulative: a cue repeats the previous line and
    appends a few words. Appending them verbatim would feed the engine the same
    sentence three times and, at a cue boundary, a stitched phrase such as
    "Janet Records". The merge keeps the longest suffix of the accumulated text
    that is also a prefix of the next cue, so only the new words are appended.
    """
    merged = ""
    for piece in pieces:
        piece = " ".join(piece.split())
        if not piece:
            continue
        if not merged:
            merged = piece
            continue
        if merged.endswith(piece):
            continue
        window = min(len(merged), len(piece), 400)
        overlap = 0
        for size in range(window, 0, -1):
            tail = merged[-size:]
            if tail.strip() and piece.startswith(tail):
                overlap = size
                break
        merged = (merged + " " + piece[overlap:].strip()).strip()
    return merged


def transcript_text(path):
    """Return (video_id, language, text) for every transcript shape in the tree."""
    with open(path, encoding="utf-8") as fh:
        data = json.load(fh)
    video_id = ""
    language = "en"
    if isinstance(data, list):
        cues = data
    elif isinstance(data, dict):
        video_id = str(data.get("video_id") or "")
        language = str(data.get("language") or "en")
        cues = data.get("cues") or []
        text = data.get("text")
        if text and not cues:
            return video_id, language, text
    else:
        raise ValueError("unsupported transcript shape: %s" % type(data).__name__)
    parts = [
        str(cue.get("text") or "")
        for cue in cues
        if isinstance(cue, dict)
    ]
    merged = merge_cues(parts)
    # A provider `text` field is already merged; keep it when it carries at
    # least as much speech as the cue merge, otherwise trust the cues.
    text = data.get("text") if isinstance(data, dict) else None
    if text and len(text.split()) >= len(merged.split()):
        return video_id, language, text
    if not video_id:
        video_id = os.path.basename(path).split(".")[0]
    return video_id, language, merged


def discover(roots, limit=0):
    found = []
    for root in roots:
        pattern = root if os.path.isabs(root) else os.path.join(REPO_ROOT, root)
        for path in sorted(glob.glob(os.path.join(pattern, "*.json"))):
            found.append(path)
    entries = []
    seen = set()
    for path in found:
        key = (os.path.basename(os.path.dirname(path)), os.path.basename(path))
        if key in seen:
            continue
        seen.add(key)
        try:
            video_id, language, text = transcript_text(path)
        except (ValueError, json.JSONDecodeError) as exc:
            entries.append({"path": path, "error": str(exc)})
            continue
        if len(text.split()) < 40:
            continue
        entries.append(
            {
                "path": path,
                "id": video_id or os.path.basename(path).split(".")[0],
                "source": os.path.basename(os.path.dirname(path)),
                "language": language,
                "words": len(text.split()),
                "text": text,
            }
        )
    # The same video can be downloaded by more than one collector, sometimes
    # with a partial window. Keep the most complete transcript per (id, lang).
    best = {}
    for entry in entries:
        key = (entry.get("id") or entry["path"], entry.get("language"))
        current = best.get(key)
        if current is None or (entry.get("words") or 0) > (current.get("words") or 0):
            best[key] = entry
    entries = sorted(best.values(), key=lambda item: (item.get("source", ""), item.get("id", "")))
    if limit:
        entries = entries[:limit]
    return entries


def call_worker_limited(worker, scenes_text, language, timeout, mem_mb):
    """Call the worker with a hard address-space cap and a wall-clock timeout.

    A runaway worker must fail as a normal error instead of taking the host
    down with it; the cap is per process, so N parallel workers cost at most
    N x mem_mb.
    """
    scenes = [{"scene_id": "s%02d" % (i + 1), "text": t} for i, t in enumerate(scenes_text)]
    request = {"operation": "analyze_script", "language": language or "it", "scenes": scenes}

    def limit():
        resource.setrlimit(resource.RLIMIT_AS, (mem_mb * 1024 * 1024, mem_mb * 1024 * 1024))

    proc = subprocess.run(
        [worker],
        input=(json.dumps(request, ensure_ascii=False) + "\n").encode("utf-8"),
        capture_output=True,
        timeout=timeout,
        preexec_fn=limit,
    )
    lines = proc.stdout.decode("utf-8").strip().splitlines()
    if not lines:
        raise RuntimeError(
            "worker produced no output (exit %s): %s"
            % (proc.returncode, proc.stderr.decode("utf-8")[:400])
        )
    response = json.loads(lines[-1])
    if not response.get("ok", True) and "manifest" not in response:
        raise RuntimeError("worker error: " + str(response.get("error"))[:400])
    return response["manifest"]


def scene_bytes(scenes):
    return {scene["scene_id"]: scene["text"].encode("utf-8") for scene in scenes}


def grounded(raw, scene_id, start, end, text):
    chunk = raw.get(scene_id)
    if chunk is None or not text:
        return False
    if start is None or end is None or start > end or end > len(chunk):
        return False
    return chunk[start:end].decode("utf-8", "replace") == text


def audit(manifest, scenes):
    """Return (hard_failures, observations, stats)."""
    hard = []
    notes = []
    stats = {
        "entities": 0,
        "facts": 0,
        "dated_facts": 0,
        "groups": 0,
        "proposals": 0,
        "queries": 0,
        "coverage_total": 0,
        "coverage_covered": 0,
    }
    if manifest.get("schema_version") != "semantic_manifest.v2":
        hard.append("schema_version=%r" % manifest.get("schema_version"))
        return hard, notes, stats
    if len(manifest.get("scenes") or []) != len(scenes):
        hard.append(
            "scene_count=%d for %d sent"
            % (len(manifest.get("scenes") or []), len(scenes))
        )
        return hard, notes, stats

    raw = scene_bytes(scenes)
    identity_ids = set()
    fact_ids = {}
    for scene_manifest in manifest["scenes"]:
        scene_id = scene_manifest["scene_id"]
        for identity in scene_manifest.get("identities") or []:
            identity_ids.add(identity["id"])
        fact_ids[scene_id] = {fact["id"] for fact in scene_manifest.get("entity_facts") or []}
        for group_name in ("entities", "bullets", "short_phrases", "long_phrases", "quotes"):
            for item in scene_manifest.get(group_name) or []:
                if not grounded(raw, scene_id, item.get("start_byte"), item.get("end_byte"), item.get("text")):
                    hard.append("%s:%s ungrounded" % (group_name, item.get("id") or item.get("text")))
        stats["entities"] += len(scene_manifest.get("entities") or [])
        for fact in scene_manifest.get("entity_facts") or []:
            stats["facts"] += 1
            if fact.get("normalized_date"):
                stats["dated_facts"] += 1
            if fact.get("entity_id") and fact["entity_id"] not in identity_ids:
                hard.append("fact %s subject %s not an identity" % (fact.get("id"), fact.get("entity_id")))
            if not grounded(
                raw,
                scene_id,
                fact.get("evidence_start_byte"),
                fact.get("evidence_end_byte"),
                fact.get("evidence"),
            ):
                hard.append("fact %s ungrounded evidence" % fact.get("id"))
        for group in scene_manifest.get("semantic_groups") or []:
            stats["groups"] += 1
            for member in group.get("members") or []:
                member_scene = member.get("scene_id") or scene_id
                if not grounded(
                    raw,
                    member_scene,
                    member.get("start_byte"),
                    member.get("end_byte"),
                    member.get("text"),
                ):
                    hard.append("group %s member %r ungrounded" % (group.get("id"), member.get("text")))
        for proposal in scene_manifest.get("visual_proposals") or []:
            stats["proposals"] += 1
            for suggestion in proposal.get("visual_query_suggestions") or []:
                stats["queries"] += 1
                if suggestion and suggestion.encode("utf-8") not in raw[scene_id]:
                    hard.append("proposal %s query not a source slice" % proposal.get("id"))

    graph = manifest.get("story_graph") or {}
    nodes = graph.get("nodes") or []
    by_id = {node.get("id") for node in nodes}
    for node in nodes:
        if not grounded(raw, node.get("scene_id"), node.get("start_byte"), node.get("end_byte"), node.get("evidence")):
            hard.append("story node %s ungrounded" % node.get("id"))
    for edge in graph.get("edges") or []:
        if edge.get("from_id") not in by_id or edge.get("to_id") not in by_id:
            hard.append("story edge dangling %s" % edge.get("id"))

    coverage = manifest.get("visual_coverage") or {}
    total = 0
    covered = 0
    for entry in coverage.get("scenes") or []:
        known = fact_ids.get(entry.get("scene_id"), set())
        for item in entry.get("facts") or []:
            total += 1
            if item.get("fact_id") not in known:
                hard.append("coverage unknown fact %s" % item.get("fact_id"))
            if (item.get("status") == "covered") != bool(item.get("proposal_types")):
                hard.append("coverage status/proposal mismatch %s" % item.get("fact_id"))
            if item.get("status") == "covered":
                covered += 1
    if coverage.get("total_facts") != total or coverage.get("covered_facts") != covered:
        hard.append("coverage totals disagree with entries")
    stats["coverage_total"] = total
    stats["coverage_covered"] = covered

    feedback = manifest.get("search_feedback") or {}
    proposal_ids = {
        (scene["scene_id"], proposal["id"])
        for scene in manifest["scenes"]
        for proposal in scene.get("visual_proposals") or []
    }
    for entry in feedback.get("entries") or []:
        if (entry.get("scene_id"), entry.get("proposal_id")) not in proposal_ids:
            hard.append("search feedback dangling proposal %s" % entry.get("proposal_id"))
        if entry.get("status") != "pending":
            hard.append("search feedback status %r" % entry.get("status"))
        for query in entry.get("queries") or []:
            # A search rung is a verbatim slice of the scene; the schema carries
            # no offsets for it, so containment is the exact check.
            if not query.get("text") or query["text"].encode("utf-8") not in raw.get(entry.get("scene_id"), b""):
                hard.append("search rung ungrounded %r" % query.get("text"))
    if stats["facts"] == 0:
        notes.append("no grounded fact")
    if not nodes:
        notes.append("empty story graph")
    if stats["groups"] == 0:
        notes.append("no semantic group")
    return hard, notes, stats


def measurement_recall(manifest, text):
    """Independent regex pass: every digit year/percentage must be recovered."""
    surfaces = []
    normalized = []
    for scene in manifest["scenes"]:
        for fact in scene.get("temporal_facts") or []:
            surfaces.append((fact.get("surface") or "").lower())
            normalized.append(fact.get("normalized_date") or "")
        for fact in scene.get("numeric_facts") or []:
            surfaces.append((fact.get("surface") or "").lower())
            if fact.get("value") is not None:
                normalized.append(str(fact["value"]))
    blob = " ".join(surfaces)
    missing_years = [year for year in YEAR_RE.findall(text) if year not in normalized and year not in blob]
    missing_pct = []
    for raw_value in PERCENT_RE.findall(text):
        value = raw_value.replace(",", ".")
        if value not in surfaces and value not in normalized and raw_value not in blob:
            missing_pct.append(raw_value)
    return {
        "years": len(YEAR_RE.findall(text)),
        "years_missing": sorted(set(missing_years)),
        "percents": len(PERCENT_RE.findall(text)),
        "percents_missing": sorted(set(missing_pct)),
    }


def analyze_entry(index, entry, worker, scene_words, timeout, determinism, language, mem_mb):
    """Analyze one transcript and return its acceptance record."""
    if "error" in entry:
        return {"index": index, "path": entry["path"], "id": entry.get("id") or entry["path"],
                "hard": ["load: " + entry["error"]]}
    scenes_text = split_scenes(entry["text"], scene_words)
    record = {
        "index": index,
        "path": os.path.relpath(entry["path"], REPO_ROOT),
        "id": entry["id"],
        "source": entry["source"],
        "language": entry["language"],
        "words": entry["words"],
        "scene_count": len(scenes_text),
    }
    started_script = time.time()
    try:
        manifest = call_worker_limited(
            worker, scenes_text, entry.get("language") or language, timeout, mem_mb
        )
        scenes = [
            {"scene_id": "s%02d" % (i + 1), "text": text}
            for i, text in enumerate(scenes_text)
        ]
    except Exception as exc:  # noqa: BLE001 - the report must name the failure
        record["hard"] = ["worker: %s" % str(exc)[:400]]
        record["seconds"] = round(time.time() - started_script, 2)
        return record
    hard, notes, stats = audit(manifest, scenes)
    recall = measurement_recall(manifest, entry["text"])
    if recall["years_missing"]:
        notes.append("years missing: " + ", ".join(recall["years_missing"][:6]))
    if recall["percents_missing"]:
        notes.append("percent missing: " + ", ".join(recall["percents_missing"][:6]))
    if determinism:
        replay = call_worker_limited(
            worker, scenes_text, entry.get("language") or language, timeout, mem_mb
        )
        if json.dumps(replay, sort_keys=True) != json.dumps(manifest, sort_keys=True):
            hard.append("determinism: replay manifest differs")
    record.update(
        {
            "fingerprint": manifest.get("fingerprint"),
            "seconds": round(time.time() - started_script, 2),
            "stats": stats,
            "recall": recall,
            "notes": notes,
            "hard": hard,
        }
    )
    return record


def _worker_call(payload):
    index, entry, worker, scene_words, timeout, determinism, language, mem_mb = payload
    return analyze_entry(
        index, entry, worker, scene_words, timeout, determinism, language, mem_mb
    )


def save_partial(results, path):
    """Persist every completed script so a long run survives an interrupted session."""
    payload = {"partial": True, "scripts": [result for result in results if result]}
    tmp = path + ".tmp"
    with open(tmp, "w", encoding="utf-8") as fh:
        json.dump(payload, fh, ensure_ascii=False)
    os.replace(tmp, path)


def load_completed(path, entries, retry_failed=False):
    completed = {}
    if not os.path.exists(path):
        return completed
    try:
        with open(path, encoding="utf-8") as fh:
            data = json.load(fh)
    except (OSError, json.JSONDecodeError):
        return completed
    for record in data.get("scripts") or []:
        index = record.get("index")
        if isinstance(index, int) and 0 <= index < len(entries):
            if entries[index].get("id") == record.get("id"):
                if retry_failed and record.get("hard"):
                    continue
                completed[index] = record
    return completed


def run(args):
    entries = discover(args.roots, args.limit)
    results = [None] * len(entries)
    completed = (
        load_completed(args.out + ".json", entries, retry_failed=args.retry_failed)
        if args.resume
        else {}
    )
    for index, record in completed.items():
        results[index] = record
    started = time.time()
    pending = [index for index in range(len(entries)) if results[index] is None]
    payloads = [
        (index, entries[index], args.worker, args.scene_words, args.timeout,
         index < args.determinism, args.language, args.worker_mem_mb)
        for index in pending
    ]
    done = len(completed)
    if completed:
        print("resuming with %d completed scripts" % done, flush=True)
    pool_iter = None
    pool = None
    if args.jobs > 1 and len(payloads) > 1:
        import multiprocessing

        pool = multiprocessing.Pool(processes=min(args.jobs, len(payloads)))
        pool_iter = pool.imap_unordered(_worker_call, payloads)
    for position in range(len(payloads)):
        if pool_iter is not None:
            record = next(pool_iter)
        else:
            record = _worker_call(payloads[position])
        done += 1
        results[record["index"]] = record
        save_partial(results, args.out + ".json")
        stats = record.get("stats") or {}
        verdict = "FAIL" if record.get("hard") else ("REVIEW" if record.get("notes") else "PASS")
        print(
            "%3d/%d %-6s %-28s %6dw %2ds %4de %3df %2dg  %s"
            % (
                done,
                len(entries),
                verdict,
                str(record.get("id"))[:28],
                record.get("words") or 0,
                record.get("scene_count") or 0,
                stats.get("entities") or 0,
                stats.get("facts") or 0,
                stats.get("groups") or 0,
                "; ".join((record.get("hard") or [])[:2]),
            ),
            flush=True,
        )
    if pool is not None:
        pool.close()
        pool.join()
    results = [result for result in results if result is not None]

    totals = {
        "scripts": len(results),
        "failed": sum(1 for result in results if result.get("hard")),
        "review": sum(1 for result in results if not result.get("hard") and result.get("notes")),
        "words": sum(result.get("words") or 0 for result in results),
        "scenes": sum(result.get("scene_count") or 0 for result in results),
        "entities": sum((result.get("stats") or {}).get("entities") or 0 for result in results),
        "facts": sum((result.get("stats") or {}).get("facts") or 0 for result in results),
        "dated_facts": sum((result.get("stats") or {}).get("dated_facts") or 0 for result in results),
        "groups": sum((result.get("stats") or {}).get("groups") or 0 for result in results),
        "proposals": sum((result.get("stats") or {}).get("proposals") or 0 for result in results),
        "queries": sum((result.get("stats") or {}).get("queries") or 0 for result in results),
        "years_checked": sum((result.get("recall") or {}).get("years") or 0 for result in results),
        "years_missing": sum(len((result.get("recall") or {}).get("years_missing") or []) for result in results),
        "percents_checked": sum((result.get("recall") or {}).get("percents") or 0 for result in results),
        "percents_missing": sum(len((result.get("recall") or {}).get("percents_missing") or []) for result in results),
        "seconds": round(time.time() - started, 1),
    }
    return results, totals


def write_report(results, totals, path, args):
    os.makedirs(os.path.dirname(path), exist_ok=True)
    with open(path + ".json", "w", encoding="utf-8") as fh:
        json.dump({"totals": totals, "scripts": results}, fh, ensure_ascii=False, indent=2)
    lines = [
        "# Corpus acceptance — %d downloaded transcripts" % totals["scripts"],
        "",
        "Worker: `%s` · %d-word scene target · %d scripts, %d words, %d scenes."
        % (args.worker, args.scene_words, totals["scripts"], totals["words"], totals["scenes"]),
        "",
        "## Verdict",
        "",
        "| check | value |",
        "|---|---|",
        "| hard failures | **%d** |" % totals["failed"],
        "| scripts with review notes | %d |" % totals["review"],
        "| entity mentions | %d |" % totals["entities"],
        "| grounded facts | %d |" % totals["facts"],
        "| dated facts | %d |" % totals["dated_facts"],
        "| semantic groups | %d |" % totals["groups"],
        "| visual proposals | %d |" % totals["proposals"],
        "| visual queries | %d |" % totals["queries"],
        "| years recovered | %d/%d |" % (totals["years_checked"] - totals["years_missing"], totals["years_checked"]),
        "| percentages recovered | %d/%d |" % (totals["percents_checked"] - totals["percents_missing"], totals["percents_checked"]),
        "| wall clock | %ss |" % totals["seconds"],
        "",
        "A hard failure means a contract or grounding defect (ungrounded span, "
        "dangling id, inconsistent coverage). Review notes are observations "
        "(no fact, no group, a year the engine did not normalize); semantic "
        "relevance is not scored without independent annotation.",
        "",
        "## Scripts",
        "",
        "| script | lang | words | scenes | entities | facts | groups | years | % | verdict |",
        "|---|---|---:|---:|---:|---:|---:|---:|---:|---|",
    ]
    for result in results:
        if "stats" not in result:
            lines.append("| `%s` | - | - | - | - | - | - | - | - | FAIL |" % result.get("id") or result.get("path"))
            continue
        recall = result.get("recall") or {}
        verdict = "FAIL" if result.get("hard") else ("REVIEW" if result.get("notes") else "PASS")
        years = "%d/%d" % (
            recall.get("years", 0) - len(recall.get("years_missing") or []),
            recall.get("years", 0),
        )
        percent = "%d/%d" % (
            recall.get("percents", 0) - len(recall.get("percents_missing") or []),
            recall.get("percents", 0),
        )
        lines.append(
            "| `%s` | %s | %d | %d | %d | %d | %d | %s | %s | %s |"
            % (
                result.get("id"),
                result.get("language"),
                result.get("words") or 0,
                result.get("scene_count") or 0,
                result["stats"]["entities"],
                result["stats"]["facts"],
                result["stats"]["groups"],
                years,
                percent,
                verdict,
            )
        )
    failures = [result for result in results if result.get("hard")]
    if failures:
        lines += ["", "## Hard failures", ""]
        for result in failures:
            lines.append("- `%s`: %s" % (result.get("id") or result.get("path"), "; ".join(result["hard"][:8])))
    reviews = [result for result in results if not result.get("hard") and result.get("notes")]
    if reviews:
        lines += ["", "## Review notes", ""]
        for result in reviews:
            lines.append("- `%s`: %s" % (result.get("id"), "; ".join(result["notes"])))
    with open(path, "w", encoding="utf-8") as fh:
        fh.write("\n".join(lines) + "\n")


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--worker", required=True)
    parser.add_argument("--roots", nargs="*", default=DEFAULT_ROOTS)
    parser.add_argument("--scene-words", type=int, default=220)
    parser.add_argument("--limit", type=int, default=0)
    parser.add_argument("--determinism", type=int, default=3,
                        help="re-run the first N scripts and require a byte-identical manifest")
    parser.add_argument("--jobs", type=int, default=1,
                        help="analyze N transcripts in parallel (capped at 3)")
    parser.add_argument("--worker-mem-mb", type=int, default=2048,
                        help="hard address-space cap per worker process")
    parser.add_argument("--language", default="it",
                        help="fallback language when the transcript does not declare one")
    parser.add_argument("--timeout", type=int, default=900,
                        help="per-script wall clock; 70k-word scripts need ~10 minutes")
    parser.add_argument("--out", default=os.path.join(REPORT_DIR, "CORPUS_ACCEPTANCE_2026-10-24.md"))
    parser.add_argument("--resume", action="store_true",
                        help="skip scripts already recorded in <out>.json")
    parser.add_argument("--retry-failed", action="store_true",
                        help="with --resume, re-run records that ended in a hard failure")
    args = parser.parse_args()
    if args.jobs > 3:
        print("jobs capped at 3 to keep the host out of OOM", flush=True)
        args.jobs = 3
    results, totals = run(args)
    write_report(results, totals, args.out, args)
    print(json.dumps(totals, ensure_ascii=False))
    print("report:", args.out)
    sys.exit(1 if totals["failed"] else 0)


if __name__ == "__main__":
    main()
