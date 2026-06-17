"""Taxonomy matching helpers — pure deterministic matching against a YAML taxonomy.

These helpers do not call any external service. They run synchronously in
microseconds per query and are safe for any ingest path.
"""

import os
import re

import yaml

# Technical/System terms to exclude from semantic fields
SYSTEM_WORDS = {
    "ai", "generated", "image", "video", "via", "prompt",
    "for", "flux", "flux-1-dev", "google-flow", "nvidia",
    "stabilityai", "sdxl", "turbo", "standard", "quality", "hd",
}


def load_taxonomy(path):
    if not os.path.exists(path):
        return {"entities": {}, "actions": {}, "styles": {}}
    with open(path, "r") as f:
        return yaml.safe_load(f)


def normalize(text):
    return text.lower().strip()


def clean_generated_prompt(text):
    """Strip technical prefixes like 'AI generated image via…'"""
    marker = "for prompt:"
    lower = text.lower()
    if marker in lower:
        return text[lower.index(marker) + len(marker):].strip()
    return text.strip()


def filter_system_words(words):
    filtered = []
    for w in words:
        norm = normalize(w)
        if norm in SYSTEM_WORDS:
            continue
        parts = norm.split()
        if all(p in SYSTEM_WORDS for p in parts):
            continue
        filtered.append(w)
    return filtered


def match_taxonomy(prompt, taxonomy):
    hits = {
        "subjects": [], "tags": [], "categories": [], "mood": [],
        "subject_slugs": [],
    }
    prompt_norm = normalize(prompt)
    for key, data in taxonomy.get("entities", {}).items():
        canonical = data.get("canonical", key)
        aliases = data.get("aliases", []) + [key, canonical.lower()]
        found = any(
            re.search(r"\b" + re.escape(normalize(a)) + r"\b", prompt_norm)
            for a in aliases
        )
        if found:
            hits["subjects"].append(canonical)
            hits["subject_slugs"].append(key.replace(" ", "-"))
            hits["tags"].extend(data.get("tags", []))
            hits["categories"].extend(data.get("categories", []))
            hits["mood"].extend(data.get("mood", []))
    for key, data in taxonomy.get("actions", {}).items():
        aliases = data.get("aliases", []) + [key]
        found = any(
            re.search(r"\b" + re.escape(normalize(a)) + r"\b", prompt_norm)
            for a in aliases
        )
        if found:
            hits["tags"].extend(data.get("tags", []))
            hits["categories"].extend(data.get("categories", []))
    for key, data in taxonomy.get("styles", {}).items():
        if re.search(r"\b" + re.escape(normalize(key)) + r"\b", prompt_norm):
            hits["tags"].extend(data.get("tags", []))
            hits["categories"].extend(data.get("categories", []))
            hits["mood"].extend(data.get("mood", []))
    for key, data in taxonomy.get("audio", {}).get("sounds", {}).items():
        aliases = data.get("aliases", []) + [key]
        found = any(
            re.search(r"\b" + re.escape(normalize(a)) + r"\b", prompt_norm)
            for a in aliases
        )
        if found:
            canonical = data.get("canonical", key.title())
            hits["subjects"].append(canonical)
            hits["subject_slugs"].append(key.replace(" ", "-"))
            hits["tags"].extend(data.get("tags", []))
            hits["categories"].extend(data.get("categories", []))
            hits["mood"].extend(data.get("mood", []))
    return hits
