#!/usr/bin/env python3
"""Offline semantic phrase-impact benchmark for four archived scripts.

From refactored/: python3 -m venv .venv-phrase-impact; then run
.venv-phrase-impact/bin/python -m pip install fastembed==0.8.1 and
.venv-phrase-impact/bin/python scripts/bench/phrase_impact.py. Model/cache and
JSON report stay under ignored .cache/. See CLI flags for repeatable settings.
"""
from __future__ import annotations

import argparse
import importlib.metadata
import json
import os
import platform
import re
import statistics
import subprocess
import time
import unicodedata
from pathlib import Path
from typing import Any

ROOT = Path(__file__).resolve().parents[2]
RENDERING = ROOT.parent / "RenderingGen"
CACHE = ROOT / ".cache" / "phrase-impact"
MODEL_NAME = "sentence-transformers/paraphrase-multilingual-MiniLM-L12-v2"
MODEL_VERSION = "fastembed==0.8.1"

CORPUS = [
    {"id": "milton-2026-09-25", "kind": "markdown", "path": RENDERING / "crime_case_scripts_20260925" / "milton_leite_ptbr.md", "timed": False},
    {"id": "isabelle-2026-09-25", "kind": "markdown", "path": RENDERING / "crime_case_scripts_20260925" / "isabelle_caracristi_ptbr.md", "timed": False},
    {"id": "milton-2026-09-26", "kind": "result_json", "path": RENDERING / "crime_case_scripts_20260926" / "milton_leite_ptbr_20260926_runtime_result.json", "timed": True},
    {"id": "isabelle-2026-09-26", "kind": "result_json", "path": RENDERING / "crime_case_scripts_20260926" / "isabelle_caracristi_ptbr_20260926_runtime_result.json", "timed": True},
]


def split_sentences(text: str) -> list[str]:
    """Match scene.splitProseSentences: punctuation followed by whitespace/end.

    Like the canonical helper this intentionally treats a decimal period as a
    boundary; preserving parity is more important than adding new segmentation
    heuristics to this benchmark-only adapter.
    """
    text = " ".join(text.split())
    if not text:
        return []
    out: list[str] = []
    start = 0
    for match in re.finditer(r"[.!?](?=\s|$)", text):
        part = text[start : match.end()].strip()
        if part:
            out.append(part)
        start = match.end()
    tail = text[start:].strip()
    if tail:
        out.append(tail)
    return out


def read_markdown(path: Path) -> tuple[list[dict[str, Any]], dict[str, Any]]:
    content = path.read_text(encoding="utf-8")
    match = re.search(r"(?ms)^## Roteiro\s*\n(.*?)(?=^## Frases sobrepostas\s*$)", content)
    if not match:
        raise ValueError(f"Cannot find Roteiro section: {path.name}")
    sections = re.split(r"(?m)^### Cena\s+(\d+)\s*$", match.group(1))
    scenes: list[tuple[int, str]] = []
    for i in range(1, len(sections), 2):
        scenes.append((int(sections[i]), sections[i + 1].strip()))
    sentences: list[dict[str, Any]] = []
    for scene_no, prose in scenes:
        for local, text in enumerate(split_sentences(prose)):
            sentences.append({"text": text, "scene": scene_no - 1, "local_index": local, "start_us": None, "end_us": None})
    return sentences, {"scene_count": len(scenes), "timing_available": False, "word_count": len(re.findall(r"\b\w+\b", match.group(1), re.UNICODE))}


def normalize_token(value: str) -> str:
    value = unicodedata.normalize("NFKD", value.casefold())
    value = "".join(ch for ch in value if not unicodedata.combining(ch))
    return "".join(ch for ch in value if ch.isalnum())


def text_tokens(text: str) -> list[str]:
    return [normalize_token(x) for x in re.findall(r"[\w]+(?:['’\-][\w]+)*", text, re.UNICODE) if normalize_token(x)]


def align_sentence_times(sentences: list[dict[str, Any]], words: list[dict[str, Any]], scene_start_us: int) -> None:
    cursor = 0
    timing_tokens: list[str] = []
    token_spans: list[tuple[int, int]] = []
    for word in words:
        pieces = text_tokens(str(word.get("text", "")))
        for piece in pieces:
            timing_tokens.append(piece)
            token_spans.append((int(word["start_us"]), int(word["end_us"])))
    for sentence in sentences:
        wanted = text_tokens(sentence["text"])
        if not wanted:
            raise ValueError("sentence has no lexical tokens")
        found = None
        for pos in range(cursor, len(timing_tokens) - len(wanted) + 1):
            if timing_tokens[pos : pos + len(wanted)] == wanted:
                found = pos
                break
        if found is None:
            excerpt = sentence["text"][:80]
            raise ValueError(f"Canonical word timings cannot exactly align sentence: {excerpt!r}")
        last = found + len(wanted) - 1
        sentence["start_us"] = scene_start_us + token_spans[found][0]
        sentence["end_us"] = scene_start_us + token_spans[last][1]
        cursor = last + 1


def read_result_json(path: Path) -> tuple[list[dict[str, Any]], dict[str, Any]]:
    data = json.loads(path.read_text(encoding="utf-8"))
    result = data["result"]["result"]
    scenes = result.get("scenes", [])
    canonical_segments = result.get("canonical_timeline", {}).get("segments", [])
    if len(scenes) != len(canonical_segments):
        raise ValueError(f"Scene/canonical timeline cardinality mismatch in {path.name}")
    speech_by_scene = {row.get("scene_id"): row for row in result.get("scene_speech_timings", [])}
    sentences: list[dict[str, Any]] = []
    mixed_scenes: list[int] = []
    scene_word_counts: list[int] = []
    total_words = 0
    for scene, segment in zip(scenes, canonical_segments):
        scene_index = int(scene["index"])
        text = scene.get("text", {}).get("pt", "")
        if not text:
            raise ValueError(f"Missing PT text in {path.name} scene {scene_index}")
        scene_sentences = split_sentences(text)
        language_words = re.findall(r"[A-Za-zÀ-ÖØ-öø-ÿ]+", text)
        english_markers = {"what", "really", "happened", "behind", "closed", "doors", "the", "investigation"}
        portuguese_markers = {"que", "como", "aconteceu", "investigação", "caso", "continua"}
        lowered = {w.casefold() for w in language_words}
        if len(lowered & english_markers) >= 2 and len(lowered & portuguese_markers) >= 1:
            mixed_scenes.append(scene_index)
        rows = [{"text": sentence, "scene": scene_index, "local_index": i, "start_us": None, "end_us": None} for i, sentence in enumerate(scene_sentences)]
        timing = speech_by_scene.get(scene.get("id"))
        if not timing or not timing.get("words"):
            raise ValueError(f"Missing canonical speech words: {path.name} scene {scene_index}")
        align_sentence_times(rows, timing["words"], int(segment["timeline_start_us"]))
        sentences.extend(rows)
        total_words += len(timing["words"])
        scene_word_counts.append(len(timing["words"]))
    return sentences, {"scene_count": len(scenes), "timing_available": True, "canonical_word_count": total_words, "scene_word_counts": scene_word_counts, "mixed_language_scenes": mixed_scenes, "timeline_duration_us": result.get("canonical_timeline", {}).get("duration_us")}


def percentile(values: list[float], q: float) -> float:
    if not values:
        return 0.0
    ordered = sorted(values)
    position = (q / 100) * (len(ordered) - 1)
    lower = int(position)
    upper = min(lower + 1, len(ordered) - 1)
    fraction = position - lower
    return ordered[lower] * (1 - fraction) + ordered[upper] * fraction


def summarize(values: list[float]) -> dict[str, float]:
    return {"min_ms": min(values), "median_ms": statistics.median(values), "p95_ms": percentile(values, 95), "max_ms": max(values)}


def run_adapter(binary: Path, request: dict[str, Any]) -> dict[str, Any]:
    proc = subprocess.run([str(binary)], input=json.dumps(request, ensure_ascii=False) + "\n", text=True, capture_output=True, check=True, cwd=ROOT)
    response = json.loads(proc.stdout)
    if response.get("error"):
        raise RuntimeError(response["error"])
    return response


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--repeat", type=int, default=20, help="warm embedding repetitions for measured latency (default: 20)")
    parser.add_argument("--warmup", type=int, default=3, help="unreported warmup embedding passes (default: 3)")
    parser.add_argument("--top", type=int, default=0, help="ranked sentences emitted per script; 0 emits the full ranking")
    parser.add_argument("--per-script-repeat", type=int, default=3, help="warm inference repetitions per script (default: 3)")
    parser.add_argument("--max-peaks", type=int, default=5, help="max temporal highlights per timed script")
    parser.add_argument("--min-gap-seconds", type=float, default=8.0, help="temporal highlight spacing")
    parser.add_argument("--threshold-percentile", type=float, default=80.0, help="score percentile for peak threshold")
    args = parser.parse_args()
    if args.repeat < 1 or args.warmup < 0 or args.top < 0 or args.max_peaks < 1 or args.per_script_repeat < 1:
        parser.error("repeat/per-script-repeat/max-peaks must be positive; top/warmup must be nonnegative")
    if args.min_gap_seconds < 0 or not 0 <= args.threshold_percentile <= 100:
        parser.error("invalid peak selection options")

    CACHE.mkdir(parents=True, exist_ok=True)
    model_cache = CACHE / "model-cache"
    model_cache.mkdir(parents=True, exist_ok=True)
    output_path = CACHE / "latest.json"
    binary = CACHE / "phrase_impact"

    corpus: list[dict[str, Any]] = []
    for spec in CORPUS:
        sentences, meta = read_markdown(spec["path"]) if spec["kind"] == "markdown" else read_result_json(spec["path"])
        for sentence in sentences:
            sentence["text"] = " ".join(sentence["text"].split())
        corpus.append({**spec, "sentences": sentences, "meta": meta})

    all_texts = [sentence["text"] for doc in corpus for sentence in doc["sentences"]]
    sentence_count = len(all_texts)
    token_count = sum(len(text_tokens(text)) for text in all_texts)
    if len(corpus) != 4 or sentence_count == 0:
        raise ValueError(f"Expected all four nonempty archival scripts; got {len(corpus)} scripts / {sentence_count} sentences")
    for doc in corpus:
        expected_timed = bool(doc["meta"].get("timing_available"))
        for sentence in doc["sentences"]:
            has_start = sentence["start_us"] is not None
            has_end = sentence["end_us"] is not None
            if has_start != has_end or has_start != expected_timed:
                raise ValueError(f"Timestamp completeness mismatch for {doc['id']}")

    build_start = time.perf_counter()
    build = subprocess.run(["go", "build", "-o", str(binary), "./scripts/bench"], cwd=ROOT, capture_output=True, text=True)
    if build.returncode:
        raise RuntimeError(f"Go scorer build failed: {build.stderr.strip()}")
    adapter_build_ms = (time.perf_counter() - build_start) * 1000

    # Import after the local scorer is built; these imports/model initialization
    # and first-use compile costs are reported separately below.
    dependency_import_start = time.perf_counter()
    import numpy as np
    import onnxruntime as ort
    import fastembed
    from fastembed import TextEmbedding
    dependency_import_ms = (time.perf_counter() - dependency_import_start) * 1000

    model_cache_populated_before_init = any(model_cache.iterdir())
    model_download_or_cache_start = time.perf_counter()
    model = TextEmbedding(MODEL_NAME, cache_dir=str(model_cache), threads=min(4, os.cpu_count() or 1), providers=["CPUExecutionProvider"])
    model_initialization_ms = (time.perf_counter() - model_download_or_cache_start) * 1000

    # Produce exactly one cold run, then warmup and measured passes. Include all
    # sentences for throughput; scoring remains separate per source document.
    cold_start = time.perf_counter()
    cold_vectors = list(model.embed(all_texts, batch_size=64, parallel=None))
    cold_embedding_ms = (time.perf_counter() - cold_start) * 1000
    if len(cold_vectors) != sentence_count:
        raise RuntimeError(f"model returned {len(cold_vectors)} embeddings for {sentence_count} sentences")
    dimension = int(np.asarray(cold_vectors[0]).shape[0]) if cold_vectors else model.embedding_size
    if dimension != 384:
        raise RuntimeError(f"unexpected embedding dimension: {dimension}")
    for _ in range(args.warmup):
        list(model.embed(all_texts, batch_size=64, parallel=None))
    warm_embedding_ms: list[float] = []
    warm_per_sentence_ms: list[float] = []
    latest_vectors = cold_vectors
    for _ in range(args.repeat):
        started = time.perf_counter()
        latest_vectors = list(model.embed(all_texts, batch_size=64, parallel=None))
        elapsed = (time.perf_counter() - started) * 1000
        warm_embedding_ms.append(elapsed)
        warm_per_sentence_ms.append(elapsed / sentence_count)

    vectors = [np.asarray(vector, dtype=np.float32).tolist() for vector in latest_vectors]
    per_script_embedding_timings: dict[str, list[float]] = {}
    cursor = 0
    records: list[dict[str, Any]] = []
    scoring_by_doc: list[dict[str, Any]] = []
    lexical_overlap: dict[str, Any] = {}
    for doc in corpus:
        doc_sentences = doc["sentences"]
        count = len(doc_sentences)
        doc_vectors = vectors[cursor : cursor + count]
        cursor += count
        script_embedding_ms: list[float] = []
        script_end_to_end_ms: list[float] = []
        for _ in range(args.per_script_repeat):
            e2e_start = time.perf_counter()
            per_script_vectors = list(model.embed([s["text"] for s in doc_sentences], batch_size=64, parallel=None))
            embed_elapsed = (time.perf_counter() - e2e_start) * 1000
            script_embedding_ms.append(embed_elapsed)
            if len(per_script_vectors) != count:
                raise RuntimeError(f"model returned {len(per_script_vectors)} embeddings for {doc['id']}")
            e2e_inputs = [{"text": sentence["text"], "start_us": sentence["start_us"] or 0, "end_us": sentence["end_us"] or 0, "embedding": np.asarray(embedding, dtype=np.float32).tolist()} for sentence, embedding in zip(doc_sentences, per_script_vectors)]
            e2e_score = run_adapter(binary, {"action": "score", "sentences": e2e_inputs})
            if len(e2e_score.get("scores", [])) != count:
                raise RuntimeError(f"scorer returned incomplete results for {doc['id']}")
            script_end_to_end_ms.append((time.perf_counter() - e2e_start) * 1000)
        per_script_embedding_timings[doc["id"]] = {"embedding_ms": summarize(script_embedding_ms), "embedding_plus_scorer_end_to_end_ms": summarize(script_end_to_end_ms)}
        semantic_inputs = []
        for sentence, embedding in zip(doc_sentences, doc_vectors):
            semantic_inputs.append({"text": sentence["text"], "start_us": sentence["start_us"] or 0, "end_us": sentence["end_us"] or 0, "embedding": embedding})
        score_start = time.perf_counter()
        scored = run_adapter(binary, {"action": "score", "sentences": semantic_inputs})
        go_wall_ms = (time.perf_counter() - score_start) * 1000
        impacts = scored.get("scores", [])
        # Production scoring input requires canonical timings as an all-or-none
        # set. Older Markdown inputs are untimed; semantic rank is still valid.
        ranked = sorted(impacts, key=lambda row: (-row["impact"], row["index"]))
        baseline_start = time.perf_counter()
        baseline = run_adapter(binary, {"action": "baseline", "text": " ".join(x["text"] for x in doc_sentences), "language": "pt", "limit": 5, "lexicon_root": str(ROOT / "config" / "lexicons")}).get("phrases", [])
        baseline_ms = (time.perf_counter() - baseline_start) * 1000
        top_indexes = {item["index"] for item in ranked[:5]}
        top_sentences = [doc_sentences[i]["text"] for i in top_indexes]
        overlap = sum(any(phrase.casefold() in sentence.casefold() for sentence in top_sentences) for phrase in baseline)
        entry = {
            "script_id": doc["id"],
            "source": doc["path"].relative_to(ROOT.parent).as_posix(),
            "language_note": ("mixed English / Portuguese: retained per requested corpus; scene 3 begins in English" if doc["meta"].get("mixed_language_scenes") else "") if "mixed_language_scenes" in doc["meta"] else "",
            "metadata": doc["meta"],
            "sentence_count": count,
            "token_count": sum(len(text_tokens(s["text"])) for s in doc_sentences),
            "timed_sentence_count": sum(s["start_us"] is not None for s in doc_sentences),
            "scoring_wall_ms_including_go_process": go_wall_ms,
            "scorer_stages": scored.get("timings", {}),
            "baseline_wall_ms_including_go_process": baseline_ms,
            "semantic_vs_lexical_top5_sentence_overlap_count": overlap,
            "lexical_baseline_phrases": baseline,
            "ranked_sentences": [],
            "peaks": [],
        }
        ranking_limit = args.top if args.top > 0 else len(ranked)
        for row in ranked[:ranking_limit]:
            source_sentence = doc_sentences[row["index"]]
            entry["ranked_sentences"].append({"rank": len(entry["ranked_sentences"]) + 1, "sentence_index": row["index"], "scene_index": source_sentence["scene"], "start_us": source_sentence["start_us"], "end_us": source_sentence["end_us"], "centrality": row["centrality"], "novelty": row["novelty"], "impact": row["impact"], "text": source_sentence["text"]})
        if doc["meta"].get("timing_available"):
            peaks = run_adapter(binary, {"action": "peaks", "scores": impacts, "options": {"min_distance_us": round(args.min_gap_seconds * 1_000_000), "threshold_percentile": args.threshold_percentile, "max_peaks": args.max_peaks}}).get("peaks", [])
            for row in peaks:
                sentence = doc_sentences[row["index"]]
                entry["peaks"].append({"sentence_index": row["index"], "scene_index": sentence["scene"], "start_us": row["start_us"], "end_us": row["end_us"], "impact": row["impact"], "text": sentence["text"]})
        records.append(entry)
        scoring_by_doc.append({"script_id": doc["id"], "wall_ms": go_wall_ms, "scorer_stages": scored.get("timings", {}), "baseline_wall_ms": baseline_ms})
        lexical_overlap[doc["id"]] = overlap

    ort_providers = ort.get_available_providers()
    if "CPUExecutionProvider" not in ort_providers:
        raise RuntimeError(f"ONNX CPU provider unavailable: {ort_providers}")
    report = {
        "benchmark": "semantic-phrase-impact-offline-v1",
        "generated_at_utc": time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime()),
        "runtime": {"python": platform.python_version(), "platform": platform.platform(), "machine": platform.machine(), "cpu_count": os.cpu_count(), "onnxruntime": ort.__version__, "fastembed": importlib.metadata.version("fastembed"), "onnx_available_providers": ort_providers, "selected_providers": ["CPUExecutionProvider"]},
        "model": {"id": MODEL_NAME, "fastembed_version": MODEL_VERSION, "dimension": dimension, "license": "Apache-2.0", "max_input_tokens": 512, "multilingual": True, "batch_size": 64, "parallel": None, "cache_path": str(model_cache.relative_to(ROOT)), "inference_device": "CPUExecutionProvider"},
        "corpus_summary": {"script_count": len(corpus), "sentence_count": sentence_count, "token_count": token_count, "timed_script_count": sum(bool(x["meta"].get("timing_available")) for x in corpus), "timed_sentence_count": sum(s["start_us"] is not None for doc in corpus for s in doc["sentences"]), "no_timestamp_script_count": sum(not x["meta"].get("timing_available") for x in corpus), "embedding_shape": [sentence_count, dimension]},
        "options": {"warmup_repetitions": args.warmup, "measured_repetitions": args.repeat, "per_script_measured_repetitions": args.per_script_repeat, "ranked_sentences_per_script": args.top or "all", "peak_threshold_percentile": args.threshold_percentile, "peak_min_gap_seconds": args.min_gap_seconds, "peak_max_count": args.max_peaks},
        "timings": {"adapter_go_build_ms": adapter_build_ms, "python_ml_dependency_import_ms": dependency_import_ms, "model_cache_populated_before_initialization": model_cache_populated_before_init, "model_download_included_in_this_run": not model_cache_populated_before_init, "model_initialization_ms_including_model_download_or_cached_model_load": model_initialization_ms, "first_cold_embedding_ms_excluding_model_construction": cold_embedding_ms, "warm_embedding_total_ms": summarize(warm_embedding_ms), "warm_embedding_ms_per_sentence": summarize(warm_per_sentence_ms), "warm_sentences_per_second_median": sentence_count / (statistics.median(warm_embedding_ms) / 1000), "warm_embedding_and_semantic_scoring_by_script": per_script_embedding_timings, "scoring_and_baseline_by_script": scoring_by_doc},
        "comparison": {"scope": "Existing deterministic lexical selector vs semantic top-five sentence overlap; lexical phrases map to their containing sentences by literal exact surface. This is a descriptive comparison, not a human quality judgment.", "top5_overlap_counts": lexical_overlap},
        "scripts": records,
        "limitations": ["25 Sep Markdown scripts have no canonical TTS word timing; no timestamps or peaks were fabricated for them.", "26 Sep Isabelle scene 3 is mixed English/Portuguese; included unfiltered as explicitly requested and flagged.", "Four historical scripts form a local prototype/performance corpus, not a labeled quality benchmark.", "Cold embedding timing excludes model construction; model construction includes model download on the first run and is separately reported. Warm inference measures repeated all-corpus batches.", "Semantic ranks are relative to the source document; p10-p90 scaling means values are not calibrated probabilities."],
    }
    output_path.write_text(json.dumps(report, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")
    print(json.dumps({"output": str(output_path.relative_to(ROOT)), "scripts": len(records), "sentences": sentence_count, "dimension": dimension, "cold_embedding_ms": cold_embedding_ms, "warm_embedding_median_ms": statistics.median(warm_embedding_ms), "model_initialization_ms": model_initialization_ms, "timed_scripts": report["corpus_summary"]["timed_script_count"], "isabelle_mixed_scenes": next((x["metadata"].get("mixed_language_scenes") for x in records if x["script_id"] == "isabelle-2026-09-26"), [])}, ensure_ascii=False))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
