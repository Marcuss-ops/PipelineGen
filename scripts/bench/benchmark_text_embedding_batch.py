#!/usr/bin/env python3
"""Compare serial and batched E5 sidecar inference on the phrase smoke corpus.

Run from refactored/: python3 scripts/bench/benchmark_text_embedding_batch.py
Optional: --embedding-url http://127.0.0.1:8001 --trials 5

This measures endpoint latency including HTTP serialization/round-trips and
samples server RSS. The 14 authored benchmark texts are smoke inputs, not a
representative production workload or human-labeled ranking benchmark.
"""
from __future__ import annotations

import argparse
import ast
import hashlib
import json
import math
import statistics
import threading
import time
import urllib.request
from pathlib import Path
from typing import Any

ROOT = Path(__file__).resolve().parents[2]
DEFAULT_CORPUS = ROOT / "scripts/bench/compare_extractor.py"


def load_texts(path: Path) -> list[str]:
    module = ast.parse(path.read_text(encoding="utf-8"))
    cases = next(
        node.value for node in module.body
        if isinstance(node, ast.AnnAssign)
        and isinstance(node.target, ast.Name)
        and node.target.id == "CASES"
    )
    texts = [case["text"] for case in ast.literal_eval(cases)]
    if not texts:
        raise ValueError("benchmark corpus is empty")
    return texts


def post_json(url: str, body: dict[str, Any]) -> dict[str, Any]:
    request = urllib.request.Request(
        url,
        data=json.dumps(body, ensure_ascii=False).encode("utf-8"),
        headers={"Content-Type": "application/json"},
    )
    with urllib.request.urlopen(request, timeout=180) as response:
        return json.loads(response.read())


def process_rss_kib(pid: int) -> int:
    for line in Path(f"/proc/{pid}/status").read_text().splitlines():
        if line.startswith("VmRSS:"):
            return int(line.split()[1])
    return 0


def timed_with_rss(call, pid: int) -> tuple[Any, float, int, int]:
    samples = [process_rss_kib(pid)]
    stop = threading.Event()

    def sample() -> None:
        while not stop.wait(0.01):
            samples.append(process_rss_kib(pid))

    sampler = threading.Thread(target=sample, daemon=True)
    sampler.start()
    started = time.perf_counter()
    try:
        result = call()
    finally:
        elapsed = time.perf_counter() - started
        stop.set()
        sampler.join()
        samples.append(process_rss_kib(pid))
    return result, elapsed, max(samples), samples[0]


def digest(vectors: list[list[float]]) -> str:
    payload = json.dumps(vectors, separators=(",", ":")).encode("utf-8")
    return hashlib.sha256(payload).hexdigest()


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--embedding-url", default="http://127.0.0.1:8001")
    parser.add_argument("--corpus", type=Path, default=DEFAULT_CORPUS)
    parser.add_argument("--trials", type=int, default=5)
    parser.add_argument("--warmups", type=int, default=2)
    parser.add_argument("--type", choices=("query", "passage"), default="query")
    parser.add_argument("--pid", type=int, required=True, help="embedding sidecar PID for RSS sampling")
    args = parser.parse_args()
    if args.trials < 1 or args.warmups < 0:
        parser.error("--trials must be positive and --warmups nonnegative")

    texts = load_texts(args.corpus)
    if len(texts) > 32:
        parser.error("the current /embed_batch contract supports at most 32 texts per request")
    base = args.embedding_url.rstrip("/")
    for _ in range(args.warmups):
        post_json(base + "/embed", {"text": texts[0], "type": args.type})
        post_json(base + "/embed_batch", {"texts": texts[: min(2, len(texts))], "type": args.type})

    serial_times: list[float] = []
    batch_times: list[float] = []
    rss_deltas: list[dict[str, int]] = []
    max_abs_difference = 0.0
    repeat_hashes: list[str] = []
    dimensions: set[int] = set()
    for _ in range(args.trials):
        serial, serial_s, serial_peak, serial_start = timed_with_rss(
            lambda: [post_json(base + "/embed", {"text": text, "type": args.type}) for text in texts],
            args.pid,
        )
        batch, batch_s, batch_peak, batch_start = timed_with_rss(
            lambda: post_json(base + "/embed_batch", {"texts": texts, "type": args.type}),
            args.pid,
        )
        if batch.get("count") != len(texts) or len(batch.get("embeddings", [])) != len(texts):
            raise ValueError("batch endpoint returned an incorrect result count")
        for index, (single, vector) in enumerate(zip(serial, batch["embeddings"])):
            if single.get("dimensions") != batch.get("dimensions") or len(single["embedding"]) != len(vector):
                raise ValueError(f"dimension mismatch at corpus row {index}")
            if single.get("model") != batch.get("model") or single.get("model_version") != batch.get("model_version"):
                raise ValueError("serial and batch model provenance differs")
            differences = [abs(float(left) - float(right)) for left, right in zip(single["embedding"], vector)]
            if not all(math.isfinite(value) for value in single["embedding"] + vector):
                raise ValueError(f"non-finite embedding value at corpus row {index}")
            max_abs_difference = max(max_abs_difference, max(differences, default=0.0))
        dimensions.add(int(batch["dimensions"]))
        repeat_hashes.append(digest(batch["embeddings"]))
        serial_times.append(serial_s)
        batch_times.append(batch_s)
        rss_deltas.append({
            "serial_start_kib": serial_start,
            "serial_peak_kib": serial_peak,
            "batch_start_kib": batch_start,
            "batch_peak_kib": batch_peak,
        })

    serial_median = statistics.median(serial_times)
    batch_median = statistics.median(batch_times)
    report = {
        "benchmark": "e5-sidecar-serial-vs-batch.v1",
        "model": {"id": batch["model"], "revision": batch["model_version"], "dimensions": sorted(dimensions), "type": args.type},
        "corpus": {"path": str(args.corpus), "count": len(texts), "kind": "synthetic phrase-ranking smoke inputs"},
        "protocol": {"warmups_per_mode": args.warmups, "measured_trials": args.trials},
        "latency_seconds": {
            "serial": {"samples": serial_times, "median": serial_median, "max": max(serial_times)},
            "batch": {"samples": batch_times, "median": batch_median, "max": max(batch_times)},
            "median_speedup": serial_median / batch_median,
            "http_requests_per_corpus": {"serial": len(texts), "batch": (len(texts) + 31) // 32},
        },
        "correctness": {"max_abs_vector_difference": max_abs_difference, "batch_repeat_hashes": repeat_hashes,
                        "batch_repeats_identical": len(set(repeat_hashes)) == 1},
        "sidecar_rss_kib": rss_deltas,
        "limitations": ["RSS is sampled every 10ms and may miss brief peaks.",
                        "Latency includes local HTTP request/response cost; inputs are the 14 authored smoke cases, not a human-labeled corpus.",
                        "Serial and batch measurements share one already-loaded sidecar process and are sequential."]
    }
    print(json.dumps(report, ensure_ascii=False, indent=2))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
