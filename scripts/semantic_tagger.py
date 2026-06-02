#!/usr/bin/env python3
"""
Semantic tagger for generated media assets.

Enrichment strategy:
- Taxonomy matching (YAML-based, fast, deterministic)
- LLM enrichment via Ollama (one-time at ingest, NOT at search time)
  Generates: concept_tags, visual_objects, emotional_tone, search_text_expanded
- search_text_expanded is stored in DB and used for FTS5/BM25 + vector search
  with zero LLM calls at query time
"""
import sys
import json
import argparse
import yaml
import os
import re
import urllib.request
import urllib.error
from datetime import datetime

# Optional dependencies - will fallback gracefully
try:
    import spacy
    nlp = spacy.load("en_core_web_sm")
except Exception:
    nlp = None

try:
    import yake
except ImportError:
    yake = None

# Technical/System terms to exclude from semantic fields
SYSTEM_WORDS = {
    "ai", "generated", "image", "video", "via", "prompt",
    "for", "flux", "flux-1-dev", "google-flow", "nvidia",
    "stabilityai", "sdxl", "turbo", "standard", "quality", "hd"
}

# ---------------------------------------------------------------------------
# TAXONOMY HELPERS (unchanged from original)
# ---------------------------------------------------------------------------

def load_taxonomy(path):
    if not os.path.exists(path):
        return {"entities": {}, "actions": {}, "styles": {}}
    with open(path, 'r') as f:
        return yaml.safe_load(f)

def normalize(text):
    return text.lower().strip()

def clean_generated_prompt(text):
    """Strips technical prefixes like 'AI generated image via...'"""
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
    hits = {"subjects": [], "tags": [], "categories": [], "mood": [], "subject_slugs": []}
    prompt_norm = normalize(prompt)
    for key, data in taxonomy.get("entities", {}).items():
        canonical = data.get("canonical", key)
        aliases = data.get("aliases", []) + [key, canonical.lower()]
        found = any(re.search(r'\b' + re.escape(normalize(a)) + r'\b', prompt_norm) for a in aliases)
        if found:
            hits["subjects"].append(canonical)
            hits["subject_slugs"].append(key.replace(" ", "-"))
            hits["tags"].extend(data.get("tags", []))
            hits["categories"].extend(data.get("categories", []))
            hits["mood"].extend(data.get("mood", []))
    for key, data in taxonomy.get("actions", {}).items():
        aliases = data.get("aliases", []) + [key]
        found = any(re.search(r'\b' + re.escape(normalize(a)) + r'\b', prompt_norm) for a in aliases)
        if found:
            hits["tags"].extend(data.get("tags", []))
            hits["categories"].extend(data.get("categories", []))
    for key, data in taxonomy.get("styles", {}).items():
        if re.search(r'\b' + re.escape(normalize(key)) + r'\b', prompt_norm):
            hits["tags"].extend(data.get("tags", []))
            hits["categories"].extend(data.get("categories", []))
            hits["mood"].extend(data.get("mood", []))
    for key, data in taxonomy.get("audio", {}).get("sounds", {}).items():
        aliases = data.get("aliases", []) + [key]
        found = any(re.search(r'\b' + re.escape(normalize(a)) + r'\b', prompt_norm) for a in aliases)
        if found:
            canonical = data.get("canonical", key.title())
            hits["subjects"].append(canonical)
            hits["subject_slugs"].append(key.replace(" ", "-"))
            hits["tags"].extend(data.get("tags", []))
            hits["categories"].extend(data.get("categories", []))
            hits["mood"].extend(data.get("mood", []))
    return hits

def extract_keywords(prompt):
    if not yake:
        return []
    kw_extractor = yake.KeywordExtractor(lan="en", n=2, dedupLim=0.9, top=10, features=None)
    return [kw[0] for kw in kw_extractor.extract_keywords(prompt)]

def extract_entities(prompt):
    if not nlp:
        return []
    doc = nlp(prompt)
    return [ent.text for ent in doc.ents if ent.label_ in ["FAC", "GPE", "LOC", "PERSON", "NORP", "ORG", "EVENT"]]

def build_description(prompt, subjects, categories, media_type):
    media_labels = {"image": "image", "video": "video", "audio": "sound effect", "voiceover": "voiceover"}
    media_label = media_labels.get(media_type, "asset")
    if not subjects:
        return f"A generated {media_label} based on the prompt: '{prompt}'."
    sub_str = ", ".join(subjects)
    relevant_cats = [c for c in categories if c not in ["composition", "aesthetic"]]
    cat_str = f" related to {', '.join(relevant_cats[:3])}" if relevant_cats else ""
    return f"A generated {media_label} of {sub_str}{cat_str} based on the prompt: '{prompt}'."

# ---------------------------------------------------------------------------
# LLM ENRICHMENT (Ollama — called ONCE at ingest, result stored in DB)
# ---------------------------------------------------------------------------

LLM_ENRICHMENT_PROMPT = """\
You are a media metadata specialist. Given a generation prompt and style for an image, \
return ONLY a valid JSON object with exactly these 3 keys:

- "concept_tags": list of 5-12 conceptual keywords and synonyms that capture the \
abstract meaning and searchable concepts (not just literal words from the prompt)
- "visual_objects": list of 4-10 physical objects or visual elements likely present \
in the image given the prompt and style
- "emotional_tone": list of 3-6 psychological or emotional tones that describe the \
feeling or intent of the image

Prompt: "{prompt}"
Style: "{style}"

Respond with ONLY the JSON object, no explanation, no markdown, no code blocks."""

# Multilingual translation prompt — used to translate metadata fields
TRANSLATION_PROMPT = """\
You are a professional translator. Translate the following metadata fields to {target_language}.
Preserve the original meaning, style, and tone.

Input JSON:
{fields_json}

Return ONLY a JSON object with the same keys containing the translated values.
No explanations, no markdown, no code blocks."""

# ISO 639-1 language names for translation prompts
LANGUAGE_NAMES = {
    "en": "English", "es": "Spanish", "fr": "French", "de": "German",
    "it": "Italian", "pt": "Portuguese", "pl": "Polish", "nl": "Dutch",
    "ja": "Japanese", "ko": "Korean", "ru": "Russian", "tr": "Turkish",
    "id": "Indonesian", "zh": "Chinese", "ar": "Arabic", "hi": "Hindi",
}


def _ollama_api_call(ollama_url, model, prompt, temperature=0.2, num_predict=400, timeout=30):
    """Low-level Ollama API call. Returns parsed JSON or raises on failure."""
    payload = json.dumps({
        "model": model,
        "prompt": prompt,
        "stream": False,
        "options": {
            "temperature": temperature,
            "num_predict": num_predict,
        }
    }).encode("utf-8")

    req = urllib.request.Request(
        ollama_url.rstrip("/") + "/api/generate",
        data=payload,
        headers={"Content-Type": "application/json"},
        method="POST"
    )
    with urllib.request.urlopen(req, timeout=timeout) as resp:
        body = json.loads(resp.read().decode("utf-8"))

    raw_response = body.get("response", "").strip()
    # Strip markdown code fences
    raw_response = re.sub(r"^```(?:json)?\s*", "", raw_response)
    raw_response = re.sub(r"\s*```$", "", raw_response)
    return json.loads(raw_response)


def call_ollama(prompt, style, ollama_url, model):
    """
    Calls Ollama once at ingest time to generate enriched metadata fields.
    Returns dict with concept_tags, visual_objects, emotional_tone — or empty defaults.
    """
    empty = {"concept_tags": [], "visual_objects": [], "emotional_tone": []}
    if not ollama_url or not model:
        return empty

    llm_prompt = LLM_ENRICHMENT_PROMPT.format(
        prompt=prompt[:800],
        style=style or "general"
    )

    try:
        parsed = _ollama_api_call(ollama_url, model, llm_prompt)
        return {
            "concept_tags":   [str(t) for t in parsed.get("concept_tags", []) if t],
            "visual_objects":  [str(t) for t in parsed.get("visual_objects", []) if t],
            "emotional_tone": [str(t) for t in parsed.get("emotional_tone", []) if t],
        }
    except (urllib.error.URLError, json.JSONDecodeError, KeyError, Exception) as e:
        print(f"[semantic_tagger] Ollama enrichment failed (non-fatal): {e}", file=sys.stderr)
        return empty


def translate_metadata(ollama_url, model, fields, target_language):
    """
    Translates metadata fields to target_language using Ollama.
    fields: dict with keys like search_text, semantic_description, tags, subjects, mood
    Returns: dict with same keys containing translated values, or empty dict on failure.
    """
    if not ollama_url or not model or not fields:
        return {}

    lang_name = LANGUAGE_NAMES.get(target_language.lower(), target_language)
    llm_prompt = TRANSLATION_PROMPT.format(
        target_language=lang_name,
        fields_json=json.dumps(fields, ensure_ascii=False, indent=2)
    )

    try:
        return _ollama_api_call(ollama_url, model, llm_prompt,
                               temperature=0.1, num_predict=800, timeout=60)
    except (urllib.error.URLError, json.JSONDecodeError, KeyError, Exception) as e:
        print(f"[semantic_tagger] Translation to {target_language} failed (non-fatal): {e}", file=sys.stderr)
        return {}


def build_search_text_expanded(prompt, subjects, tags, categories, mood,
                                concept_tags, visual_objects, emotional_tone,
                                style, semantic_description):
    """
    Builds a single flat text blob combining ALL semantic fields.
    Stored once in DB — enables FTS5/BM25/Manticore + vector search
    with ZERO LLM calls at query time.
    """
    all_parts = (
        [prompt, semantic_description]
        + subjects + tags + categories + mood
        + concept_tags + visual_objects + emotional_tone
        + ([style] if style else [])
    )
    tokens = set()
    for part in all_parts:
        if not part:
            continue
        for token in normalize(part).split():
            if token not in SYSTEM_WORDS and len(token) > 2:
                tokens.add(token)
    # Also keep full phrases for bigram matching
    phrases = set()
    for part in all_parts:
        p = normalize(part).strip()
        if p and len(p) > 3 and p not in SYSTEM_WORDS:
            phrases.add(p)
    return " ".join(sorted(tokens) + sorted(phrases - tokens))


# ---------------------------------------------------------------------------
# MAIN
# ---------------------------------------------------------------------------

# Nota: il supporto multilingua non si basa su dizionari statici.
# Il modello intfloat/multilingual-e5-base mappa nativamente tutte le lingue
# nello stesso spazio semantico. Il cross-encoder bge-reranker-v2-m3 ri-ordina
# i candidati in qualsiasi lingua. Nessuna traduzione esplicita necessaria.
#
# PERO': per garantire riusabilita' multilingua su Drive, il tagger puo' generare
# traduzioni esplicite dei campi metadata chiave (search_text, tags, etc.) via Ollama.

def main():
    parser = argparse.ArgumentParser(description="Semantic tagger for generated assets")
    parser.add_argument("--prompt",      required=True,  help="Original generation prompt")
    parser.add_argument("--style",       default="",     help="Generation style")
    parser.add_argument("--media-type",  default="image",help="image, video, audio, or voiceover")
    parser.add_argument("--generator",   default="unknown")
    parser.add_argument("--taxonomy",    default="config/semantic_taxonomy.yaml")
    parser.add_argument("--ollama-url",  default="",     help="Ollama base URL (e.g. http://localhost:11434)")
    parser.add_argument("--ollama-model",default="",     help="Ollama model for enrichment (e.g. llama3)")
    parser.add_argument("--language",    default="en",   help="Source language code (ISO 639-1, e.g. en, it, es)")
    parser.add_argument("--translate-to",default="",     help="Comma-separated target language codes for translation (e.g. it,es,fr)")
    args = parser.parse_args()

    # 1. Clean prompt
    clean_prompt = clean_generated_prompt(args.prompt)
    taxonomy = load_taxonomy(args.taxonomy)

    # 2. Taxonomy matching
    hits = match_taxonomy(clean_prompt, taxonomy)
    if args.style:
        for k, v in match_taxonomy(args.style, taxonomy).items():
            hits[k].extend(v)

    # 3. Dynamic extraction (YAKE + spaCy)
    yake_kws   = extract_keywords(clean_prompt)
    spacy_ents = extract_entities(clean_prompt)

    # 4. Merge & deduplicate
    subjects     = sorted(set(filter_system_words(hits["subjects"] + spacy_ents)))
    subject_slugs= sorted(set(hits["subject_slugs"]))
    tags_list    = sorted(set(filter_system_words(hits["tags"] + yake_kws + ([args.style] if args.style else []))))
    categories   = sorted(set(filter_system_words(hits["categories"])))
    if len(categories) > 2 and "composition" in categories:
        categories.remove("composition")
    mood = sorted(set(filter_system_words(hits["mood"])))

    # 5. Base search_text
    search_components = [clean_prompt] + subjects + tags_list + categories + mood
    search_text = " ".join(w for w in
        " ".join(sorted(set(normalize(s) for s in search_components if s))).split()
        if w not in SYSTEM_WORDS)

    # 6. LLM enrichment (Ollama — one-time at ingest, result stored in DB)
    llm_result    = call_ollama(clean_prompt, args.style, args.ollama_url, args.ollama_model)
    concept_tags  = llm_result["concept_tags"]
    visual_objects= llm_result["visual_objects"]
    emotional_tone= llm_result["emotional_tone"]

    # 7. Build expanded search text (everything combined into one FTS blob)
    semantic_desc = build_description(clean_prompt, subjects, categories, args.media_type)
    search_text_expanded = build_search_text_expanded(
        clean_prompt, subjects, tags_list, categories, mood,
        concept_tags, visual_objects, emotional_tone,
        args.style, semantic_desc
    )

    # 8. Confidence
    confidence = 0.5
    if subjects:       confidence += 0.2
    if categories:     confidence += 0.1
    if concept_tags:   confidence += 0.1
    if visual_objects: confidence += 0.05
    if len(tags_list) > 5: confidence += 0.05
    confidence = min(confidence, 0.95)

    asset_type = {"image": "image", "video": "video", "audio": "sound_effect", "voiceover": "voiceover"}.get(args.media_type, "image")
    semantic_tier = "generated_rich" if (concept_tags or visual_objects) else "generated_light"

    # 9. Multi-language translations (Ollama — one-time at ingest)
    translations = {}
    translate_targets = [t.strip() for t in args.translate_to.split(",") if t.strip()] if args.translate_to else []
    if translate_targets and args.ollama_url and args.ollama_model:
        fields_to_translate = {
            "search_text": search_text,
            "semantic_description": semantic_desc,
            "tags": tags_list,
            "subjects": subjects,
            "mood": mood,
        }
        for target_lang in translate_targets:
            translated = translate_metadata(
                args.ollama_url, args.ollama_model,
                fields_to_translate, target_lang
            )
            if translated:
                translations[target_lang] = translated
                # Add translated tokens to search_text_expanded for cross-language search
                for value in translated.values():
                    if isinstance(value, str):
                        for token in normalize(value).split():
                            if token not in SYSTEM_WORDS and len(token) > 2:
                                tokens.add(token)
                    elif isinstance(value, list):
                        for item in value:
                            for token in normalize(str(item)).split():
                                if token not in SYSTEM_WORDS and len(token) > 2:
                                    tokens.add(token)
        # Rebuild search_text_expanded with multilingual tokens
        phrases = set()
        for part in (
            [clean_prompt, semantic_desc]
            + subjects + tags_list + categories + mood
            + concept_tags + visual_objects + emotional_tone
            + ([args.style] if args.style else [])
        ):
            p = normalize(str(part)).strip()
            if p and len(p) > 3 and p not in SYSTEM_WORDS:
                phrases.add(p)
        search_text_expanded = " ".join(sorted(tokens) + sorted(phrases - tokens))

        print(f"[semantic_tagger] Generated translations for: {', '.join(translations.keys())}", file=sys.stderr)

    result = {
        "asset_id":             "",
        "asset_type":           asset_type,
        "semantic_tier":        semantic_tier,
        "source":               "generated",
        "media_type":           args.media_type,
        "generator":            args.generator,
        "language":             args.language,
        "prompt_original":      clean_prompt,
        "semantic_description": semantic_desc,
        "search_text":          search_text,
        "concept_tags":         concept_tags,
        "visual_objects":       visual_objects,
        "emotional_tone":       emotional_tone,
        "search_text_expanded": search_text_expanded,
        "subjects":             subjects,
        "subject_slugs":        subject_slugs,
        "tags":                 tags_list,
        "categories":           categories,
        "mood":                 mood,
        "style":                [args.style] if args.style else [],
        "confidence":           round(confidence, 2),
        "embedding_status":     "pending",
        "translations":         translations,
        "created_at":           datetime.utcnow().isoformat() + "Z",
    }

    print(json.dumps(result, indent=2, ensure_ascii=False))

if __name__ == "__main__":
    main()
