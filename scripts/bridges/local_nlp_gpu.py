#!/usr/bin/env python3
"""Local GPU NER bridge for scene annotations.

Protocol: one JSON EntityExtractionRequest on stdin, one EntityResult on
stdout. It uses spaCy with CuPy when available and never calls an LLM.
"""

from __future__ import annotations

import argparse
import json
import os
import re
import sys


def load_pipeline(language: str):
    import spacy

    if not spacy.require_gpu():
        raise RuntimeError("spaCy GPU is unavailable; install a matching cupy-cuda package")
    defaults = {
        "en": "en_core_web_sm",
        "it": "it_core_news_sm",
        "es": "es_core_news_sm",
        "fr": "fr_core_news_sm",
        "de": "de_core_news_sm",
    }
    model = os.environ.get(
        f"PIPELINEGEN_NLP_SPACY_MODEL_{language.upper()}",
        os.environ.get("PIPELINEGEN_NLP_SPACY_MODEL", defaults.get(language, f"{language}_core_news_sm")),
    )
    return spacy.load(model)


def sentence(text: str) -> str:
    candidates = [part.strip() for part in re.findall(r"[^.!?]+(?:[.!?]+|$)", text)]
    return max(candidates, key=lambda part: (len(re.findall(r"\w+", part)), len(part)), default="")


def extract(request: dict) -> dict:
    text = str(request.get("text", "")).strip()
    language = str(request.get("language", "en")).strip() or "en"
    nlp = load_pipeline(language)
    doc = nlp(text)
    persons, places, concepts = [], [], []
    seen = set()
    for ent in doc.ents:
        label = ent.label_.upper()
        if label in {"PERSON"}:
            target = persons
            normalized = "PERSON"
        elif label in {"ORG", "GPE", "LOC", "FAC"}:
            target = places
            normalized = "GPE" if label in {"GPE", "LOC", "FAC"} else "ORG"
        else:
            target = concepts
            normalized = label
        key = (normalized, ent.text.casefold())
        if key in seen:
            continue
        seen.add(key)
        target.append({"value": ent.text, "type": normalized, "score": 0.85})

    words = []
    stop = {"della", "dello", "questo", "questa", "anche", "con", "per", "the", "and", "of"}
    for token in doc:
        word = token.text.casefold()
        if token.is_alpha and len(word) >= 4 and not token.is_stop and word not in stop:
            if word not in words and not any(word in ent["value"].casefold().split() for ent in persons + places):
                words.append(word)
    limit = int(request.get("entity_count", 5) or 5)
    limit = max(1, min(limit, 50))
    return {
        "persons": persons[:limit],
        "places": places[:limit],
        "concepts": concepts[:limit],
        "important_phrases": [sentence(text)] if text else [],
        "important_words": words[:limit],
        "artlist_phrases": [],
    }


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--check-gpu", action="store_true")
    args = parser.parse_args()
    try:
        if args.check_gpu:
            load_pipeline(os.environ.get("PIPELINEGEN_NLP_GPU_CHECK_LANGUAGE", "en"))
            return 0
        request = json.load(sys.stdin)
        print(json.dumps(extract(request), ensure_ascii=False))
        return 0
    except Exception as exc:  # fail closed; Go decides whether auto may fall back
        print(str(exc), file=sys.stderr)
        return 2


if __name__ == "__main__":
    raise SystemExit(main())
