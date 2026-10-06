#!/usr/bin/env python3
"""Run the synthetic transcript through the Rust phrase-impact interface.

From refactored/: python3 scripts/bench/elon_phrase_impact.py
This fixture and its manually authored labels are regression data, not human
quality certification. JSON reports default to the ignored .cache directory.
"""
from __future__ import annotations

import argparse
import json
import math
import subprocess
import time
import urllib.error
import urllib.request
from pathlib import Path
from typing import Any

ROOT = Path(__file__).resolve().parents[2]
FIXTURE = ROOT / "rust/pipelinegen-muscles/fixtures/elon_musk_synthetic_transcript_it.txt"
GROUND_TRUTH = ROOT / "rust/pipelinegen-muscles/fixtures/elon_musk_synthetic_ground_truth.json"
DEFAULT_BINARY = ROOT / "bin/phrase_impact"
DEFAULT_REPORT = ROOT / ".cache/phrase-impact/elon_musk_phrase_impact.json"


def run_worker(binary: Path, payload: dict[str, Any]) -> dict[str, Any]:
    process = subprocess.run(
        [str(binary), "phrase-impact"],
        input=json.dumps(payload, ensure_ascii=False) + "\n",
        text=True,
        capture_output=True,
        check=True,
        cwd=ROOT,
    )
    response = json.loads(process.stdout)
    if not response.get("ok"):
        raise RuntimeError(response.get("error", "Rust phrase-impact worker failed"))
    return response


def ranking_metrics(ranked: list[dict[str, Any]], relevant: set[str]) -> dict[str, float]:
    ideal = sum(1.0 / math.log2(index + 2) for index, _case_id in enumerate(sorted(relevant)))

    def dcg(rows: list[dict[str, Any]]) -> float:
        return sum(1.0 / math.log2(index + 2) for index, row in enumerate(rows) if row["case_id"] in relevant)
    output: dict[str, float] = {}
    for k in (5, 10):
        top = ranked[:k]
        hits = sum(row["case_id"] in relevant for row in top)
        output[f"precision_at_{k}"] = hits / len(top) if top else 0.0
        output[f"recall_heavy_at_{k}"] = hits / len(relevant) if relevant else 0.0
        output[f"ndcg_at_{k}"] = dcg(top) / ideal if ideal else 0.0
    output["heavy_recall_all"] = sum(row["case_id"] in relevant for row in ranked) / len(relevant) if relevant else 0.0
    return output


def is_extractive(summary: str, source_sentences: list[str]) -> bool:
    remaining = summary.strip()
    if not remaining:
        return False
    while remaining:
        match = next((sentence for sentence in sorted(source_sentences, key=len, reverse=True) if remaining.startswith(sentence)), None)
        if match is None:
            return False
        remaining = remaining[len(match):].lstrip()
    return True


def embed_passages(server_url: str, sentences: list[str]) -> tuple[list[list[float]], dict[str, Any]]:
    vectors: list[list[float]] = []
    metadata: dict[str, Any] | None = None
    for start in range(0, len(sentences), 32):
        batch = sentences[start : start + 32]
        request = urllib.request.Request(
            server_url.rstrip("/") + "/embed_batch",
            data=json.dumps({"texts": batch, "type": "passage"}, ensure_ascii=False).encode("utf-8"),
            headers={"Content-Type": "application/json"},
            method="POST",
        )
        try:
            with urllib.request.urlopen(request, timeout=120) as response:
                envelope = json.loads(response.read())
        except (urllib.error.URLError, TimeoutError, json.JSONDecodeError) as error:
            raise RuntimeError(f"multilingual embedding request failed for sentences {start}..{start + len(batch)}: {error}") from error
        if envelope.get("count") != len(batch) or len(envelope.get("embeddings", [])) != len(batch):
            raise AssertionError(f"embedding batch count mismatch at {start}: {envelope}")
        dimensions = envelope.get("dimensions")
        if not isinstance(dimensions, int) or dimensions <= 0 or any(len(row) != dimensions for row in envelope["embeddings"]):
            raise AssertionError(f"embedding dimension mismatch at {start}: {envelope}")
        batch_metadata = {key: envelope.get(key) for key in ("model", "model_version", "dimensions", "contract_hash")}
        if metadata is not None and batch_metadata != metadata:
            raise AssertionError("embedding model contract changed between batches")
        metadata = batch_metadata
        vectors.extend(envelope["embeddings"])
    return vectors, metadata or {}


def evaluate(binary: Path, embedding_server_url: str | None = None) -> dict[str, Any]:
    transcript = FIXTURE.read_text(encoding="utf-8")
    labels = json.loads(GROUND_TRUTH.read_text(encoding="utf-8"))
    sentence_gold = labels["sentences"]
    heavy_ids = set(labels["heavy_sentence_ids"])

    split = run_worker(binary, {"operation": "split_sentences", "transcript": transcript, "language": "it"})
    sentences = split["sentences"]
    byte_source = transcript.encode("utf-8")
    for index, sentence in enumerate(sentences):
        start, end = sentence["start_byte"], sentence["end_byte"]
        if not (0 <= start < end <= len(byte_source)):
            raise AssertionError(f"invalid sentence byte offsets at segment {index}: {start}..{end}")
        try:
            source_text = byte_source[start:end].decode("utf-8")
        except UnicodeDecodeError as error:
            raise AssertionError(f"non-boundary UTF-8 sentence offsets at segment {index}") from error
        if source_text != sentence["text"]:
            raise AssertionError(f"segment {index} does not round-trip to the source text")

    text_to_case = {row["text"]: row["id"] for row in sentence_gold}
    missing_gold = [row["id"] for row in sentence_gold if row["text"] not in {item["text"] for item in sentences}]
    if missing_gold:
        raise AssertionError(f"gold sentence text not preserved by the splitter: {missing_gold}")

    embeddings: list[list[float]] = []
    embedding_metadata: dict[str, Any] | None = None
    embedding_ms = 0.0
    if embedding_server_url:
        embedding_started = time.monotonic()
        embeddings, embedding_metadata = embed_passages(embedding_server_url, [item["text"] for item in sentences])
        embedding_ms = (time.monotonic() - embedding_started) * 1000.0
        if len(embeddings) != len(sentences):
            raise AssertionError(f"embedding count mismatch: expected {len(sentences)}, got {len(embeddings)}")
    analysis = run_worker(
        binary,
        {
            "transcript": transcript,
            "language": "it",
            "embeddings": embeddings,
            "lexical_only": not bool(embedding_server_url),
            "embedding_ms": embedding_ms,
            "options": {
                "summary_length": "short",
                "bullet_count": 10,
                "min_heavy": 15,
                "max_heavy": 15,
                "top_fraction": 0.125,
            },
        },
    )["result"]
    ranked = [
        {
            "rank": rank,
            "index": row["index"],
            "case_id": text_to_case.get(row["text"]),
            "importance": row["importance"],
            "text": row["text"],
        }
        for rank, row in enumerate(analysis["ranked"], 1)
    ]
    metrics = ranking_metrics(ranked, heavy_ids)

    summary = analysis["summary"]
    bullets = [row["text"] for row in analysis["bullet_points"]]
    negation = next(row["must_preserve_negation"] for row in sentence_gold if row.get("must_preserve_negation"))
    negation_sentence = next(row["text"] for row in sentence_gold if row.get("must_preserve_negation"))
    summary_preserves_negation = negation not in summary or negation.casefold() in summary.casefold()
    bullets_preserve_negation = all(negation not in bullet or negation.casefold() in bullet.casefold() for bullet in bullets)
    summary_is_extractive = is_extractive(summary, [sentence["text"] for sentence in sentences])
    report = {
        "benchmark": "synthetic-phrase-impact-evaluation.v1",
        "dataset_type": labels["dataset_type"],
        "generated_at_utc": time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime()),
        "fixture": FIXTURE.relative_to(ROOT).as_posix(),
        "ground_truth": GROUND_TRUTH.relative_to(ROOT).as_posix(),
        "word_count_whitespace": len(transcript.split()),
        "sentence_count": len(sentences),
        "sentence_offset_unit": "UTF-8 bytes",
        "sentence_offsets_round_trip": True,
        "gold_sentence_ids_found": len(sentence_gold),
        "heavy_sentence_count": len(heavy_ids),
        "ranking_metrics": metrics,
        "ranking_engine": "canonical multilingual E5 sidecar" if embedding_server_url else "Rust lexical-only fallback",
        "embedding_model": embedding_metadata,
        "ranking_targets": "not declared; report-only synthetic diagnostic",
        "ranking_targets_passed": None,
        "summary": summary,
        "summary_word_count": len(summary.split()),
        "summary_is_extractive": summary_is_extractive,
        "summary_preserves_licensing_negation": summary_preserves_negation,
        "negation_key_sentence_in_summary": negation_sentence in summary,
        "bullet_points": bullets,
        "bullets_preserve_licensing_negation": bullets_preserve_negation,
        "negation_key_sentence_in_bullets": negation_sentence in bullets,
        "embedding_ms": analysis.get("timings", {}).get("embedding_ms"),
        "total_ms": analysis.get("timings", {}).get("total_ms"),
        "ranking": [
            {"rank": row["rank"], "case_id": row["case_id"], "importance": row["importance"], "text": row["text"]}
            for row in ranked if row["case_id"] in heavy_ids
        ],
        "limitations": [
            "This approximately 2,000-word synthetic corpus and its labels are developer-authored regression data, not human evaluation.",
            "Labels are developer-authored and this single Italian fixture cannot certify multilingual ranking quality.",
            "Ranking metrics are diagnostic only and failures are not repaired with language-specific keyword rules.",
            "The extractor NER is not evaluated by this Rust phrase-impact CLI; use scripts/bench/compare_extractor.py for its separate manually labeled smoke comparison.",
        ],
    }
    return report


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--binary", type=Path, default=DEFAULT_BINARY, help="pipelinegen-muscles phrase-impact executable")
    parser.add_argument("--output", type=Path, default=DEFAULT_REPORT, help="JSON report path (default: ignored .cache)")
    parser.add_argument("--embedding-url", help="optional canonical multilingual-E5 sidecar base URL (e.g. http://127.0.0.1:8001)")
    args = parser.parse_args()
    binary = args.binary.resolve()
    if not binary.is_file():
        parser.error(f"phrase-impact binary not found: {binary}")
    report = evaluate(binary, args.embedding_url)
    args.output.parent.mkdir(parents=True, exist_ok=True)
    args.output.write_text(json.dumps(report, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")
    print(json.dumps({
        "report": str(args.output),
        "word_count": report["word_count_whitespace"],
        "sentences": report["sentence_count"],
        "ranking_metrics": report["ranking_metrics"],
        "negation_preserved_if_selected_in_summary": report["summary_preserves_licensing_negation"],
        "negation_key_sentence_in_summary": report["negation_key_sentence_in_summary"],
        "negation_preserved_if_selected_in_bullets": report["bullets_preserve_licensing_negation"],
        "negation_key_sentence_in_bullets": report["negation_key_sentence_in_bullets"],
        "ranking_engine": report["ranking_engine"],
        "embedding_model": report["embedding_model"],
        "embedding_ms": report["embedding_ms"],
        "total_ms": report["total_ms"],
    }, ensure_ascii=False, indent=2))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
