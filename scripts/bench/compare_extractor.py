#!/usr/bin/env python3
"""Compare local extractors on a synthetic smoke subset from the Elon benchmark.

From refactored/: python3 scripts/bench/compare_extractor.py

This is a smoke comparison, NOT an independently annotated NER certificate.
It uses the real VisualNER executable, a cached spaCy model, the running E5
embedding endpoint, and the production Go semantic phrase scorer. Default NER
settings are 20 full-corpus warmups and 200 measured passes per backend.
The JSON report is written under ignored .cache/phrase-impact/.
"""
from __future__ import annotations

import argparse
import json
from collections import Counter
import math
import os
import resource
import subprocess
import tempfile
import time
import urllib.request
from pathlib import Path
from typing import Any

ROOT = Path(__file__).resolve().parents[2]
DEFAULT_MODEL = ROOT / ".cache/ner-models/python/xx_ent_wiki_sm/xx_ent_wiki_sm-3.8.0"
DEFAULT_VISUALNER = ROOT / "rust/target/debug/visualner"
OUTPUT = ROOT / ".cache/phrase-impact/elon_musk_extractor_comparison.json"

# Verbatim benchmark sentences copied from the user's pasted synthetic corpus.
# Entity spans are manually selected for smoke diagnostics; they are not
# double-annotated gold. Heavy labels use all nine IDs specified by the user.
CASES: list[dict[str, Any]] = [
    {
        "id": "S006", "language": "it", "heavy": True,
        "text": "La prima frase davvero importante arrivò quando Elon Musk dichiarò, nel contesto fittizio di questo benchmark, che Tesla avrebbe puntato ad aumentare del 17.5% la capacità produttiva del sito di Austin entro il 2027 senza costruire una seconda linea completa.",
        "gold": [("Elon Musk", "PERSON"), ("Tesla", "ORG"), ("17.5%", "PERCENT"), ("Austin", "GPE"), ("2027", "DATE")],
    },
    {
        "id": "S011", "language": "it", "heavy": True,
        "text": "Il secondo punto chiave fu l'annuncio sintetico secondo cui Tesla Energy stava valutando, sempre in questo scenario inventato, un investimento da €750 milioni in un impianto di accumulo vicino a Berlino con una decisione finale prevista per il 2 aprile 2026.",
        "gold": [("Tesla Energy", "ORG"), ("€750 milioni", "MONEY"), ("Berlino", "GPE"), ("2 aprile 2026", "DATE")],
    },
    {
        "id": "S017", "language": "it", "heavy": True,
        "text": "Il terzo momento ad alta importanza arrivò quando Musk disse che un aggiornamento software fittizio per Model Y avrebbe ridotto dell'8% il consumo energetico medio nei test interni, con una distribuzione prevista a partire da maggio 2026.",
        "gold": [("Musk", "PERSON"), ("Model Y", "PRODUCT"), ("8%", "PERCENT"), ("maggio 2026", "DATE")],
    },
    {
        "id": "S023", "language": "it", "heavy": True,
        "text": "La quarta frase pesante stabilì che SpaceX avrebbe programmato, nello scenario sintetico, una finestra di prova di Starship il 21 giugno 2026 alle 9:15 a.m., con un massimo di tre tentativi tecnici distribuiti nella stessa settimana.",
        "gold": [("SpaceX", "ORG"), ("Starship", "PRODUCT"), ("21 giugno 2026", "DATE"), ("9:15 a.m.", "TIME")],
    },
    {
        "id": "S029", "language": "it", "heavy": True,
        "text": "Il quinto punto chiave affermò che xAI avrebbe allocato, nel benchmark inventato, $4.8 billion a un programma triennale di infrastruttura computazionale tra Austin e Memphis, con la prima fase operativa prevista nel Q4 2026.",
        "gold": [("xAI", "ORG"), ("$4.8 billion", "MONEY"), ("Austin", "GPE"), ("Memphis", "GPE"), ("Q4 2026", "DATE")],
    },
    {
        "id": "S035", "language": "it", "heavy": True,
        "text": "Il sesto momento importante fu una frase che indicava, sempre nel test sintetico, che Neuralink avrebbe aperto un centro di ricerca da $120 million a São Paulo, Brazil, nel 2027, impiegando circa 320 persone.",
        "gold": [("Neuralink", "ORG"), ("$120 million", "MONEY"), ("São Paulo", "GPE"), ("Brazil", "GPE"), ("2027", "DATE"), ("320", "NUMBER")],
    },
    {
        "id": "S041", "language": "it", "heavy": True,
        "text": "Il settimo punto ad alta importanza dichiarò che Tesla non avrebbe annunciato licenziamenti durante l'evento sintetico e che, al contrario, il piano operativo ipotizzava 1,500 nuove assunzioni tecniche tra Texas e Germania entro diciotto mesi.",
        "gold": [("Tesla", "ORG"), ("1,500", "NUMBER"), ("Texas", "GPE"), ("Germania", "GPE")],
    },
    {
        "id": "S047", "language": "it", "heavy": True,
        "text": "L'ottavo punto chiave indicò che Tesla avrebbe ridotto del 22% il tempo medio di approvvigionamento per alcuni componenti elettronici entro dicembre 2026, secondo un piano pilota limitato a tre fornitori e due stabilimenti.",
        "gold": [("Tesla", "ORG"), ("22%", "PERCENT"), ("dicembre 2026", "DATE")],
    },
    {
        "id": "S052", "language": "it", "heavy": True,
        "text": "La nona e ultima frase pesante riassunse il quadro operativo fittizio: entro il 2027 i progetti descritti avrebbero coinvolto Austin, Berlino e São Paulo, oltre 1,800 nuove posizioni e investimenti pianificati superiori a $5.6 billion.",
        "gold": [("2027", "DATE"), ("Austin", "GPE"), ("Berlino", "GPE"), ("São Paulo", "GPE"), ("1,800", "NUMBER"), ("$5.6 billion", "MONEY")],
    },
    {
        "id": "S015", "language": "it", "heavy": False,
        "text": "Elon Musk discussed Tesla durante una breve risposta in inglese, frase inserita apposta per verificare che il sistema identifichi Tesla come ORG e non produca l'entità errata discussed Tesla come VISUAL_CONCEPT.",
        "gold": [("Elon Musk", "PERSON"), ("Tesla", "ORG", 0), ("Tesla", "ORG", 1), ("Tesla", "ORG", 2)],
    },
    {
        "id": "S057", "language": "it", "heavy": False,
        "text": "Durante quella riunione, Maya Chen osserva che una frase con un numero elevato o con un nome famoso non deve ricevere automaticamente il punteggio massimo: per esempio, la stringa Elon Musk possiede 99 penne blu è intenzionalmente irrilevante anche se contiene PERSON e CARDINAL.",
        "gold": [("Maya Chen", "PERSON"), ("Elon Musk", "PERSON"), ("99", "NUMBER")],
    },
    {
        "id": "S058", "language": "it", "heavy": False,
        "text": "Per lo stesso motivo, una frase come Paris is a city non dovrebbe superare l'annuncio sull'investimento da €750 milioni soltanto perché contiene un luogo riconoscibile; il ranking deve combinare centralità, novità e contesto invece di premiare in modo meccanico le entità.",
        "gold": [("Paris", "GPE"), ("€750 milioni", "MONEY")],
    },
    {
        "id": "WHISPER", "language": "it", "heavy": False,
        "text": "Per introdurre rumore simile a Whisper, una registrazione secondaria riportava la frase uh Elon Musk basically discussed Tesla and then you know talked about Austin before the audio dropped for two seconds.",
        "gold": [("Elon Musk", "PERSON"), ("Tesla", "ORG"), ("Austin", "GPE")],
    },
    {
        "id": "COMPANY_SUFFIXES", "language": "it", "heavy": False,
        "text": "Una slide riportava Apple Inc., NVIDIA Corp. e Tesla Inc. nello stesso elenco, caso utile per testare suffissi societari senza inglobare parole successive che non appartengono al nome dell'organizzazione.",
        "gold": [("Apple Inc", "ORG"), ("NVIDIA Corp", "ORG"), ("Tesla Inc", "ORG")],
    },
]

HEAVY_CASE_IDS = {"S006", "S011", "S017", "S023", "S029", "S035", "S041", "S047", "S052"}


def validate_cases() -> None:
    ids = [case["id"] for case in CASES]
    if len(ids) != len(set(ids)):
        raise ValueError("benchmark smoke case IDs must be unique")
    if {case["id"] for case in CASES if case["heavy"]} != HEAVY_CASE_IDS:
        raise ValueError("ranking gold must match all nine user-specified heavy sentence IDs")
    if len(CASES) != 14:
        raise ValueError(f"expected 14 smoke cases covering heavy, trap, offset, Whisper and company-name cases; got {len(CASES)}")
    for case in CASES:
        used_spans: set[tuple[int, int]] = set()
        for item in case["gold"]:
            surface = item[0]
            occurrence = item[2] if len(item) == 3 else 0
            entity_span = span(case["text"], surface, occurrence)
            if entity_span in used_spans:
                raise ValueError(f"duplicate gold span for {case['id']}: {surface!r}")
            used_spans.add(entity_span)

LABELS = {
    "PER": "PERSON", "PERSON": "PERSON",
    "ORG": "ORG", "ORGANIZATION": "ORG", "BRAND": "ORG",
    "LOC": "GPE", "LOCATION": "GPE", "GPE": "GPE",
    "DATE": "DATE", "TIME": "TIME", "MONEY": "MONEY",
    "PERCENT": "PERCENT", "PERCENTAGE": "PERCENT",
    "CARDINAL": "NUMBER", "ORDINAL": "NUMBER", "NUMBER": "NUMBER", "QUANTITY": "NUMBER",
    "PRODUCT": "PRODUCT", "EVENT": "EVENT", "WORK": "WORK_OF_ART",
    "WORK_OF_ART": "WORK_OF_ART", "FAC": "GPE", "NORP": "CONCEPT",
    "LAW": "CONCEPT", "LANGUAGE": "CONCEPT", "MISC": "CONCEPT", "CONCEPT": "CONCEPT",
    "VISUAL_CONCEPT": "CONCEPT",
}


def span(text: str, surface: str, occurrence: int = 0) -> tuple[int, int]:
    if occurrence < 0:
        raise ValueError("gold span occurrence must be nonnegative")
    start = -1
    cursor = 0
    for _ in range(occurrence + 1):
        start = text.find(surface, cursor)
        if start < 0:
            raise ValueError(f"gold surface occurrence {occurrence} missing: {surface!r} in {text!r}")
        cursor = start + len(surface)
    return len(text[:start].encode("utf-8")), len(text[:start + len(surface)].encode("utf-8"))


def percentile(values: list[float], q: float) -> float:
    if not values:
        return 0.0
    ordered = sorted(values)
    return ordered[round((len(ordered) - 1) * q)]


def latency_summary(values: list[float]) -> dict[str, float]:
    return {
        "mean_ms": sum(values) / len(values) if values else 0.0,
        "p50_ms": percentile(values, 0.50),
        "p95_ms": percentile(values, 0.95),
        "p99_ms": percentile(values, 0.99),
    }


def gold_rows(case: dict[str, Any]) -> list[dict[str, Any]]:
    rows = []
    for item in case["gold"]:
        surface, label = item[:2]
        occurrence = item[2] if len(item) == 3 else 0
        start, end = span(case["text"], surface, occurrence)
        rows.append({"text": surface, "label": label, "start": start, "end": end})
    return rows


def rust_entities(proc: subprocess.Popen[str], case: dict[str, Any]) -> list[dict[str, Any]]:
    assert proc.stdin is not None and proc.stdout is not None
    request = {"source_text": case["text"], "language": case["language"], "entity_count": 100}
    proc.stdin.write(json.dumps(request, ensure_ascii=False) + "\n")
    proc.stdin.flush()
    line = proc.stdout.readline()
    if not line:
        raise RuntimeError(f"VisualNER exited early for {case['id']}: {proc.poll()}")
    response = json.loads(line)
    return [
        {"text": entity["text"], "label": LABELS.get(entity["type"].upper(), entity["type"].upper()),
         "start": entity["start"], "end": entity["end"]}
        for entity in response.get("entities", [])
    ]


def entity_metrics(cases: list[dict[str, Any]], predictions: dict[str, list[dict[str, Any]]]) -> dict[str, Any]:
    counts: dict[str, list[int]] = {}
    exact = [0, 0, 0]
    boundary = [0, 0, 0]
    invalid_offsets = 0
    invalid_labels = 0
    invalid_outputs = 0
    predicted_total = 0
    per_case: dict[str, Any] = {}
    valid_labels = set(LABELS.values())
    for case in cases:
        gold = gold_rows(case)
        predicted = predictions[case["id"]]
        predicted_total += len(predicted)
        gold_exact = Counter((e["start"], e["end"], e["label"]) for e in gold)
        valid_pred_exact: Counter[tuple[int, int, str]] = Counter()
        valid_gold_boundary = Counter((e["start"], e["end"]) for e in gold)
        valid_pred_boundary: Counter[tuple[int, int]] = Counter()
        valid_label_gold: dict[str, Counter[tuple[int, int, str]]] = {}
        valid_label_pred: dict[str, Counter[tuple[int, int, str]]] = {}
        case_invalid_offsets = 0
        case_invalid_labels = 0
        for entity in predicted:
            raw_label = entity["label"].upper()
            label = LABELS.get(raw_label, raw_label)
            offset_valid = _matches_utf8_span(case["text"], entity)
            label_valid = label in valid_labels
            if not offset_valid:
                invalid_offsets += 1
                case_invalid_offsets += 1
            else:
                valid_pred_boundary[(entity["start"], entity["end"])] += 1
            if not label_valid:
                invalid_labels += 1
                case_invalid_labels += 1
            if not offset_valid or not label_valid:
                invalid_outputs += 1
                continue
            key = (entity["start"], entity["end"], label)
            valid_pred_exact[key] += 1
            valid_label_pred.setdefault(label, Counter())[key] += 1
            entity["label"] = label
        for key, count in gold_exact.items():
            valid_label_gold.setdefault(key[2], Counter())[key] += count

        tp = sum(min(count, valid_pred_exact[key]) for key, count in gold_exact.items())
        fp = len(predicted) - tp  # malformed predictions remain false positives
        fn = sum(gold_exact.values()) - tp
        exact = [exact[0] + tp, exact[1] + fp, exact[2] + fn]
        boundary_tp = sum(min(count, valid_pred_boundary[key]) for key, count in valid_gold_boundary.items())
        boundary_fp = len(predicted) - boundary_tp
        boundary_fn = sum(valid_gold_boundary.values()) - boundary_tp
        boundary = [boundary[0] + boundary_tp, boundary[1] + boundary_fp, boundary[2] + boundary_fn]
        for label in set(valid_label_gold) | set(valid_label_pred):
            gold_label = valid_label_gold.get(label, Counter())
            pred_label = valid_label_pred.get(label, Counter())
            row = counts.setdefault(label, [0, 0, 0])
            label_tp = sum(min(count, pred_label[key]) for key, count in gold_label.items())
            row[0] += label_tp
            row[1] += sum(pred_label.values()) - label_tp
            row[2] += sum(gold_label.values()) - label_tp
        if case_invalid_offsets or case_invalid_labels:
            row = counts.setdefault("INVALID_OUTPUT", [0, 0, 0])
            row[1] += case_invalid_offsets + case_invalid_labels
        per_case[case["id"]] = {"gold": gold, "predicted": predicted, "true_positive": tp,
                                 "false_positive": fp, "false_negative": fn,
                                 "invalid_offsets": case_invalid_offsets,
                                 "invalid_labels": case_invalid_labels}

    def score(value: list[int]) -> dict[str, float | int]:
        tp, fp, fn = value
        precision = tp / (tp + fp) if tp + fp else 0.0
        recall = tp / (tp + fn) if tp + fn else 0.0
        return {"true_positive": tp, "false_positive": fp, "false_negative": fn,
                "precision": precision, "recall": recall,
                "f1": 2 * precision * recall / (precision + recall) if precision + recall else 0.0}

    return {"exact_span_and_type": score(exact), "boundary_only": score(boundary),
            "per_label": {label: score(value) for label, value in sorted(counts.items())},
            "invalid_offsets": invalid_offsets, "invalid_labels": invalid_labels,
            "invalid_outputs": invalid_outputs, "predicted_entities": predicted_total,
            "per_case": per_case}


def _matches_utf8_span(text: str, entity: dict[str, Any]) -> bool:
    raw = text.encode("utf-8")
    start, end = entity["start"], entity["end"]
    if not (0 <= start < end <= len(raw)):
        return False
    try:
        return raw[start:end].decode("utf-8") == entity["text"]
    except UnicodeDecodeError:
        return False


def process_rss_mb(pid: int) -> float:
    try:
        for line in Path(f"/proc/{pid}/status").read_text().splitlines():
            if line.startswith("VmRSS:"):
                return float(line.split()[1]) / 1024
    except (OSError, ValueError):
        pass
    return 0.0


def run_ner(cases: list[dict[str, Any]], binary: Path, model_path: Path, warmup: int, iterations: int) -> dict[str, Any]:
    import spacy

    started = time.perf_counter()
    nlp = spacy.load(str(model_path))
    spacy_load_ms = (time.perf_counter() - started) * 1000
    rust_started = time.perf_counter()
    rust = subprocess.Popen([str(binary)], stdin=subprocess.PIPE, stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True, bufsize=1)
    rust_startup_ms = (time.perf_counter() - rust_started) * 1000

    last_predictions: dict[str, dict[str, list[dict[str, Any]]]] = {"rust": {}, "spacy": {}}
    latencies: dict[str, list[float]] = {"rust": [], "spacy": []}
    rust_peak_rss = process_rss_mb(rust.pid)
    try:
        # Warm each backend on every sample, matching the corpus-pass protocol.
        for _ in range(warmup):
            for case in cases:
                rust_entities(rust, case)
                nlp(case["text"])
                rust_peak_rss = max(rust_peak_rss, process_rss_mb(rust.pid))
        for _ in range(iterations):
            for case in cases:
                started = time.perf_counter()
                r_entities = rust_entities(rust, case)
                latencies["rust"].append((time.perf_counter() - started) * 1000)
                rust_peak_rss = max(rust_peak_rss, process_rss_mb(rust.pid))
                started = time.perf_counter()
                doc = nlp(case["text"])
                latencies["spacy"].append((time.perf_counter() - started) * 1000)
                converted = []
                for entity in doc.ents[:100]:
                    start = len(case["text"][:entity.start_char].encode("utf-8"))
                    end = len(case["text"][:entity.end_char].encode("utf-8"))
                    converted.append({"text": entity.text, "label": LABELS.get(entity.label_.upper(), entity.label_.upper()), "start": start, "end": end})
                last_predictions["rust"][case["id"]] = r_entities
                last_predictions["spacy"][case["id"]] = converted
    finally:
        if rust.stdin:
            rust.stdin.close()
        rust.wait(timeout=10)
    if rust.returncode:
        error = rust.stderr.read() if rust.stderr else ""
        raise RuntimeError(f"VisualNER exited {rust.returncode}: {error}")
    process_peak = resource.getrusage(resource.RUSAGE_SELF).ru_maxrss / 1024
    return {
        "corpus_cases": len(cases), "warmup_passes": warmup, "measured_passes": iterations,
        "warmup_calls_per_backend": len(cases) * warmup,
        "measured_calls_per_backend": len(cases) * iterations,
        "total_calls_per_backend": len(cases) * (warmup + iterations),
        "calls_per_backend": len(cases) * iterations,
        "model": {"spacy_model_path": str(model_path), "spacy_version": spacy.__version__,
                  "spacy_model_load_ms": spacy_load_ms, "visualner_binary": str(binary),
                  "visualner_process_start_ms": rust_startup_ms},
        "latency_per_call": {backend: latency_summary(values) for backend, values in latencies.items()},
        "peak_rss_mb": {"benchmark_python_process_including_spacy": process_peak,
                        "visualner_child_sampled": rust_peak_rss},
        "quality": {backend: entity_metrics(cases, values) for backend, values in last_predictions.items()},            "limitations": ["The smoke gold is a small manually curated subset of the exact synthetic source sentences, not human double-annotated data.",

                        "spaCy offsets are converted from Unicode codepoints to UTF-8 bytes before comparison.",
                        "Latency includes per-request JSON pipe overhead for VisualNER; spaCy is timed in-process.",
                        "RSS is sampled from /proc; Python process RSS includes its loaded spaCy model."]
    }


def post_json(url: str, body: dict[str, Any], timeout: float = 60) -> tuple[dict[str, Any], float]:
    request = urllib.request.Request(url, data=json.dumps(body, ensure_ascii=False).encode("utf-8"), headers={"Content-Type": "application/json"})
    started = time.perf_counter()
    with urllib.request.urlopen(request, timeout=timeout) as response:
        data = json.loads(response.read())
    return data, (time.perf_counter() - started) * 1000


def ndcg(ranked_relevances: list[int], all_relevances: list[int], k: int) -> float:
    """NDCG@K against the ideal ordering of the entire evaluated corpus."""
    dcg = sum((2**rel - 1) / math.log2(index + 2) for index, rel in enumerate(ranked_relevances[:k]))
    ideal = sorted(all_relevances, reverse=True)[:k]
    best = sum((2**rel - 1) / math.log2(index + 2) for index, rel in enumerate(ideal))
    return dcg / best if best else 0.0



def run_phrase_ranking(cases: list[dict[str, Any]], embedding_url: str, warmup: int, iterations: int) -> dict[str, Any]:
    vectors: list[list[float]] = []
    embedding_latencies: list[float] = []
    model_name = ""
    dimensions = 0
    for case in cases:
        response, elapsed = post_json(embedding_url, {"text": case["text"], "type": "passage"})
        vector = response["embedding"]
        if not vector or not all(math.isfinite(float(value)) for value in vector):
            raise ValueError(f"invalid embedding for {case['id']}")
        vectors.append([float(value) for value in vector])
        dimensions = int(response.get("dimensions", len(vector)))
        model_name = response.get("model", model_name)
        embedding_latencies.append(elapsed)
    if any(len(row) != dimensions for row in vectors):
        raise ValueError("embedding dimensions are inconsistent")

    with tempfile.TemporaryDirectory(prefix="velox-phrase-bench-") as temporary:
        binary = Path(temporary) / "phrase-scorer"
        build = subprocess.run(["go", "build", "-o", str(binary), "./scripts/bench"], cwd=ROOT, text=True, capture_output=True)
        if build.returncode:
            raise RuntimeError(f"build production semantic scorer failed: {build.stderr.strip()}")
        sentence_rows = [{"text": case["text"], "embedding": vector} for case, vector in zip(cases, vectors)]
        request = json.dumps({"action": "score", "sentences": sentence_rows}, ensure_ascii=False) + "\n"
        scores: list[dict[str, Any]] = []
        total_latencies: list[float] = []
        stage_samples: dict[str, list[float]] = {}
        # The existing Go CLI flushes its buffered output at EOF. Invoke it
        # once per sample instead of expecting an unsupported streaming reply.
        for _ in range(warmup):
            warm = subprocess.run([str(binary)], input=request, text=True, capture_output=True, check=True, cwd=ROOT)
            if not warm.stdout.strip():
                raise RuntimeError("production phrase scorer returned no warmup output")
        for _ in range(iterations):
            started = time.perf_counter()
            measured = subprocess.run([str(binary)], input=request, text=True, capture_output=True, check=True, cwd=ROOT)
            total_latencies.append((time.perf_counter() - started) * 1000)
            result = json.loads(measured.stdout)
            if result.get("error"):
                raise RuntimeError(result["error"])
            scores = result["scores"]
            for stage, value in result.get("timings", {}).items():
                stage_samples.setdefault(stage, []).append(float(value))

    ranked = sorted(scores, key=lambda row: (-row["impact"], row["index"]))
    relevance = [2 if case["heavy"] else 0 for case in cases]
    metrics = {}
    heavy_total = sum(value > 0 for value in relevance)
    for k in (5, 10):
        top = ranked[:k]
        hits = sum(relevance[row["index"]] > 0 for row in top)
        metrics[f"precision_at_{k}"] = hits / len(top) if top else 0.0
        metrics[f"recall_heavy_at_{k}"] = hits / heavy_total if heavy_total else 0.0
        metrics[f"ndcg_at_{k}"] = ndcg([relevance[row["index"]] for row in top], relevance, k)
    return {
        "embedding_model": model_name, "embedding_dimensions": dimensions,
        "embedding_endpoint": embedding_url, "embedding_latency_per_sentence": latency_summary(embedding_latencies),
        "scorer_warmup_runs": warmup, "scorer_measured_runs": iterations,
        "scorer_latency_per_full_corpus": latency_summary(total_latencies),
        "scorer_stage_latency": {stage: latency_summary(values) for stage, values in stage_samples.items()},
        "heavy_labels": heavy_total, "metrics": metrics,
        "ranked": [{"rank": rank, "id": row["index"], "case_id": cases[row["index"]]["id"],
                    "heavy": cases[row["index"]]["heavy"], "impact": row["impact"], "text": row["text"]}
                   for rank, row in enumerate(ranked, 1)],
        "limitations": ["Rank labels are illustrative labels assigned by the benchmark author, not independent human judgments.",
                        "This evaluates the production Go semantic scorer only; extractive summary/bullets are a separate Rust prototype and are not part of this report.",
                        "Embedding latency includes HTTP round-trip to the running E5 sidecar and was sampled once per sentence."]
    }


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--visualner", type=Path, default=DEFAULT_VISUALNER)
    parser.add_argument("--spacy-model", type=Path, default=Path(os.environ.get("PIPELINEGEN_SPACY_MODEL", DEFAULT_MODEL)))
    parser.add_argument("--embedding-url", default="http://127.0.0.1:8001/embed")
    parser.add_argument("--warmup", type=int, default=20)
    parser.add_argument("--iterations", type=int, default=200)
    parser.add_argument("--skip-ranking", action="store_true", help="run NER comparison only")
    parser.add_argument("--output", type=Path, default=OUTPUT)
    args = parser.parse_args()
    if args.warmup < 0 or args.iterations < 1:
        parser.error("--warmup must be nonnegative and --iterations must be positive")
    if not args.visualner.is_file():
        parser.error(f"VisualNER executable not found: {args.visualner}")
    if not args.spacy_model.is_dir():
        parser.error(f"spaCy model directory not found: {args.spacy_model}")

    validate_cases()
    report: dict[str, Any] = {
        "benchmark": "elon-musk-synthetic-smoke-comparison.v1",
        "generated_at_utc": time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime()),
        "dataset_type": "synthetic_developer_authored_exact_text_smoke_not_human_evaluation",
        "ground_truth_scope": "Exact source sentences are copied from the user-provided synthetic corpus; entity labels are a manually curated smoke subset, not independent or double-annotated gold.",
        "cases": [{"id": case["id"], "language": case["language"], "heavy": case["heavy"], "text": case["text"]} for case in CASES],
        "protocol": {"warmup_passes": args.warmup, "measured_passes": args.iterations, "entity_count": 100},
        "ner": run_ner(CASES, args.visualner.resolve(), args.spacy_model.resolve(), args.warmup, args.iterations),
    }
    if not args.skip_ranking:
        report["phrase_ranking"] = run_phrase_ranking(CASES, args.embedding_url, args.warmup, args.iterations)
    else:
        report["phrase_ranking"] = {"status": "not_run_by_request"}
    args.output.parent.mkdir(parents=True, exist_ok=True)
    args.output.write_text(json.dumps(report, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")
    print(json.dumps({"report": str(args.output), "case_count": len(CASES),
                      "ner": {name: value["exact_span_and_type"] for name, value in report["ner"]["quality"].items()},
                      "ranking": report["phrase_ranking"].get("metrics")}, ensure_ascii=False, indent=2))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
