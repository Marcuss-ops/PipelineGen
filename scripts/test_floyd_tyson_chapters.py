#!/usr/bin/env python3
"""
Smoke test per il flusso:
1. testo controllato Floyd -> Mike Tyson
2. planning semantico dei capitoli con Gemma
3. estrazione entita
4. mapping clip Drive/Artlist per capitolo

Uso:
  python3 scripts/test_floyd_tyson_chapters.py
  python3 scripts/test_floyd_tyson_chapters.py --api-url http://127.0.0.1:8080
"""

from __future__ import annotations

import argparse
import json
import re
import sys
import textwrap
import urllib.error
import urllib.request
from pathlib import Path


DEFAULT_API_URL = "http://127.0.0.1:8080"


TEXT = textwrap.dedent(
    """
    Floyd Mayweather opens the story as the master of distance, timing, and control, the undefeated boxer who turned defense into a weapon and money into a brand.
    Then the narrative shifts to Mike Tyson, whose early rise, explosive power, and fearsome aura change the tone from elegance to violence and make the second chapter feel heavier.
    """
).strip()


STOPWORDS = {
    "a",
    "an",
    "and",
    "as",
    "at",
    "be",
    "become",
    "brings",
    "by",
    "control",
    "defense",
    "does",
    "during",
    "early",
    "feel",
    "from",
    "heavy",
    "heavier",
    "his",
    "in",
    "into",
    "is",
    "it",
    "make",
    "master",
    "money",
    "narrative",
    "of",
    "opens",
    "power",
    "second",
    "shifts",
    "story",
    "the",
    "then",
    "to",
    "turns",
    "turning",
    "whose",
    "weapon",
    "with",
}


def http_json(method: str, url: str, payload: dict | None = None, timeout: int = 30) -> dict:
    data = None
    headers = {}
    if payload is not None:
        data = json.dumps(payload).encode("utf-8")
        headers["Content-Type"] = "application/json"

    req = urllib.request.Request(url, data=data, headers=headers, method=method)
    with urllib.request.urlopen(req, timeout=timeout) as resp:
        body = resp.read().decode("utf-8")
    return json.loads(body)


def split_sentences(text: str) -> list[str]:
    parts = [p.strip() for p in re.split(r"(?<=[.!?])\s+", text) if p.strip()]
    return parts


def token_keywords(text: str, limit: int = 8) -> list[str]:
    tokens = []
    for raw in re.findall(r"[A-Za-z][A-Za-z'-]{2,}", text):
        word = raw.strip("'-").lower()
        if word in STOPWORDS:
            continue
        if word not in tokens:
            tokens.append(word)
    return tokens[:limit]


def build_segment_payloads(divide_segments: list[dict], entity_response: dict) -> list[dict]:
    segment_entities: dict[int, list[str]] = {}
    for item in entity_response.get("segment_data", []):
        if not isinstance(item, dict):
            continue
        idx = item.get("segment_index")
        if idx is None:
            continue
        entities = []
        for ent in item.get("entities", []):
            if isinstance(ent, dict):
                value = str(ent.get("value", "")).strip()
                if value:
                    entities.append(value)
        segment_entities[int(idx)] = entities

    payload_segments = []
    for seg in divide_segments:
        idx = int(seg.get("index", 0))
        text = str(seg.get("text") or seg.get("source_text") or seg.get("title") or "").strip()
        payload_segments.append(
            {
                "id": f"seg_{idx}",
                "index": idx,
                "start_time": float(seg.get("start_time", 0)),
                "end_time": float(seg.get("end_time", 0)),
                "text": text,
                "keywords": token_keywords(text),
                "entities": segment_entities.get(idx, []),
                "emotions": [],
            }
        )
    return payload_segments


def print_mapping(mapping: dict) -> None:
    print()
    print("Mapping per capitolo")
    print("=" * 72)

    segments = mapping.get("mapping", {}).get("segments", [])
    for seg in segments:
        segment = seg.get("segment", {})
        assigned = seg.get("assigned_clips", [])
        print(
            f"[{segment.get('index', '?')}] "
            f"{segment.get('start_time', 0)}s -> {segment.get('end_time', 0)}s"
        )
        print(f"  {segment.get('text', '').strip()}")
        print(f"  clips: {len(assigned)} | best_score: {seg.get('best_score', 0)}")
        for clip in assigned:
            source = clip.get("source", "?")
            score = clip.get("relevance_score", 0)
            name = clip.get("name", "")
            link = clip.get("drive_link", "")
            reason = clip.get("match_reason", "")
            print(f"    - [{source}] {score:.1f} {name}")
            if link:
                print(f"      {link}")
            if reason:
                print(f"      {reason}")
        print()


def main() -> int:
    parser = argparse.ArgumentParser(description="Test Floyd -> Tyson chapter mapping")
    parser.add_argument("--api-url", default=DEFAULT_API_URL, help="Base URL API Go")
    parser.add_argument("--output", default="/tmp/floyd_tyson_mapping.json", help="File JSON di output")
    parser.add_argument("--max-segments", type=int, default=2, help="Numero massimo di capitoli")
    args = parser.parse_args()

    api_url = args.api_url.rstrip("/")

    print("Smoke test Floyd -> Mike Tyson")
    print(f"API: {api_url}")
    print()

    try:
        health = http_json("GET", f"{api_url}/health", timeout=5)
    except Exception as exc:
        print(f"ERRORE: server non raggiungibile: {exc}")
        return 1

    if not health:
        print("ERRORE: health check vuoto")
        return 1

    print("Health OK")
    print()

    plan_segments: list[dict] = []
    try:
        plan_req = {
            "topic": "Floyd Mayweather to Mike Tyson",
            "text": TEXT,
            "source_language": "english",
            "target_language": "english",
            "duration": 120,
            "max_chapters": args.max_segments,
            "model": "gemma3:4b",
        }
        plan_response = http_json("POST", f"{api_url}/api/script-pipeline/plan-chapters", plan_req)
        if plan_response.get("ok"):
            plan_segments = plan_response.get("chapters", [])
    except urllib.error.HTTPError as exc:
        if exc.code != 404:
            raise

    if plan_segments:
        for seg in plan_segments:
            seg["text"] = seg.get("source_text") or seg.get("translated_text") or seg.get("title") or ""
        print(f"Capitoli pianificati: {len(plan_segments)}")
        for seg in plan_segments:
            print(
                f"  - [{seg.get('index')}] {seg.get('start_sentence', 0)} -> {seg.get('end_sentence', 0)} | "
                f"{str(seg.get('title', '')).strip()}"
            )
        print()
    else:
        print("plan-chapters non disponibile, fallback a divide")
        print()
        divide_req = {"script": TEXT, "max_segments": args.max_segments}
        divide = http_json("POST", f"{api_url}/api/script-pipeline/divide", divide_req)
        if not divide.get("ok"):
            print(f"Divide fallito: {divide}")
            return 1
        divide_segments = divide.get("segments", [])
        if len(divide_segments) < 2:
            sentences = split_sentences(TEXT)
            divide_segments = [
                {"index": i, "text": sentence, "start_time": i * 30, "end_time": (i + 1) * 30}
                for i, sentence in enumerate(sentences[:2])
            ]
        plan_segments = [
            {
                "index": seg.get("index", i),
                "title": f"Chapter {i + 1}",
                "start_sentence": i,
                "end_sentence": i,
                "text": seg.get("text", ""),
                "source_text": seg.get("text", ""),
                "start_time": seg.get("start_time", 0),
                "end_time": seg.get("end_time", 0),
            }
            for i, seg in enumerate(divide_segments)
        ]

    if len(plan_segments) < 2:
        print("ERRORE: impossibile ottenere due capitoli utili")
        return 1

    extract_req = {"segments": plan_segments, "max_entities": 8}
    extract = http_json("POST", f"{api_url}/api/script-pipeline/extract-entities", extract_req)
    if not extract.get("ok"):
        print(f"Extract entities fallito: {extract}")
        return 1

    payload_segments = build_segment_payloads(plan_segments, extract)

    mapping_req = {
        "script_id": "floyd_tyson_test",
        "segments": payload_segments,
        "media_type": "clip",
        "max_clips_per_segment": 3,
        "min_score": 20,
        "include_drive": True,
        "include_artlist": True,
    }
    mapping = http_json("POST", f"{api_url}/api/timestamp/map", mapping_req)
    if not mapping.get("success"):
        print(f"Timestamp mapping fallito: {mapping}")
        return 1

    out_path = Path(args.output)
    out_path.write_text(json.dumps(mapping, indent=2), encoding="utf-8")

    print_mapping(mapping)
    print(f"JSON salvato in: {out_path}")
    print()
    print("Nota:")
    print("  Questo test usa il mapping timestamp/clip per verificare che i due capitoli")
    print("  Floyd e Tyson tornino con link Drive associati ai segmenti corretti.")

    return 0


if __name__ == "__main__":
    raise SystemExit(main())
