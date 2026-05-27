#!/usr/bin/env python3
import sys
import json
import argparse
import yaml
import os
import re
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
    """Filters out technical terms from a list of words or phrases"""
    filtered = []
    for w in words:
        norm = normalize(w)
        # Check if the word itself is a system word or contains only system words
        if norm in SYSTEM_WORDS:
            continue
        # For multi-word phrases, only keep if not entirely composed of system words
        parts = norm.split()
        if all(p in SYSTEM_WORDS for p in parts):
            continue
        filtered.append(w)
    return filtered

def match_taxonomy(prompt, taxonomy):
    hits = {
        "subjects": [],
        "tags": [],
        "categories": [],
        "mood": [],
        "subject_slugs": []
    }
    
    prompt_norm = normalize(prompt)
    
    # Match entities
    for key, data in taxonomy.get("entities", {}).items():
        canonical = data.get("canonical", key)
        aliases = data.get("aliases", []) + [key, canonical.lower()]
        
        found = False
        for alias in aliases:
            # Use word boundaries for matching
            if re.search(r'\b' + re.escape(normalize(alias)) + r'\b', prompt_norm):
                found = True
                break
        
        if found:
            hits["subjects"].append(canonical)
            hits["subject_slugs"].append(key.replace(" ", "-"))
            hits["tags"].extend(data.get("tags", []))
            hits["categories"].extend(data.get("categories", []))
            hits["mood"].extend(data.get("mood", []))

    # Match actions
    for key, data in taxonomy.get("actions", {}).items():
        aliases = data.get("aliases", []) + [key]
        found = False
        for alias in aliases:
            if re.search(r'\b' + re.escape(normalize(alias)) + r'\b', prompt_norm):
                found = True
                break
        if found:
            hits["tags"].extend(data.get("tags", []))
            hits["categories"].extend(data.get("categories", []))

    # Match styles
    for key, data in taxonomy.get("styles", {}).items():
        if re.search(r'\b' + re.escape(normalize(key)) + r'\b', prompt_norm):
            hits["tags"].extend(data.get("tags", []))
            hits["categories"].extend(data.get("categories", []))
            hits["mood"].extend(data.get("mood", []))

    return hits

def extract_keywords(prompt):
    keywords = []
    if yake:
        kw_extractor = yake.KeywordExtractor(lan="en", n=2, dedupLim=0.9, top=10, features=None)
        kws = kw_extractor.extract_keywords(prompt)
        keywords = [kw[0] for kw in kws]
    return keywords

def extract_entities(prompt):
    entities = []
    if nlp:
        doc = nlp(prompt)
        for ent in doc.ents:
            if ent.label_ in ["FAC", "GPE", "LOC", "PERSON", "NORP", "ORG", "EVENT"]:
                entities.append(ent.text)
    return entities

def build_description(prompt, subjects, categories, media_type):
    media_label = "image" if media_type == "image" else "video"
    if not subjects:
        return f"A generated {media_label} based on the prompt: '{prompt}'."
    
    sub_str = ", ".join(subjects)
    cat_str = ""
    relevant_cats = [c for c in categories if c not in ["composition", "aesthetic"]]
    if relevant_cats:
        cat_str = f" related to {', '.join(relevant_cats[:3])}"
    
    return f"A generated {media_label} of {sub_str}{cat_str} based on the prompt: '{prompt}'."

def main():
    parser = argparse.ArgumentParser(description="Semantic tagger for generated assets")
    parser.add_argument("--prompt", required=True, help="Original generation prompt")
    parser.add_argument("--style", default="", help="Generation style")
    parser.add_argument("--media-type", default="image", help="image or video")
    parser.add_argument("--generator", default="unknown", help="google-flow, nvidia, etc.")
    parser.add_argument("--taxonomy", default="config/semantic_taxonomy.yaml", help="Path to taxonomy YAML")
    
    args = parser.parse_args()
    
    # 1. Clean Prompt
    clean_prompt = clean_generated_prompt(args.prompt)
    taxonomy = load_taxonomy(args.taxonomy)
    
    # 2. Process Taxonomy
    hits = match_taxonomy(clean_prompt, taxonomy)
    if args.style:
        style_hits = match_taxonomy(args.style, taxonomy)
        for k in hits:
            hits[k].extend(style_hits[k])

    # 3. Extract dynamic info
    yake_kws = extract_keywords(clean_prompt)
    spacy_ents = extract_entities(clean_prompt)
    
    # 4. Merge, Filter System Words, and Deduplicate
    all_subjects = hits["subjects"] + spacy_ents
    subjects = sorted(list(set(filter_system_words(all_subjects))))
    
    subject_slugs = sorted(list(set(hits["subject_slugs"])))
    
    all_tags = hits["tags"] + yake_kws
    if args.style:
        all_tags.append(args.style)
    tags = sorted(list(set(filter_system_words(all_tags))))
    
    categories = sorted(list(set(filter_system_words(hits["categories"]))))
    # Remove generic categories if better ones exist
    if len(categories) > 2 and "composition" in categories:
        categories.remove("composition")
        
    mood = sorted(list(set(filter_system_words(hits["mood"]))))
    
    # 5. Build search text (Clean content only)
    search_components = [clean_prompt] + subjects + tags + categories + mood
    search_text = " ".join(sorted(list(set([normalize(s) for s in search_components if s]))))
    # Final filter for search_text
    search_text = " ".join([w for w in search_text.split() if w not in SYSTEM_WORDS])
    
    # 6. Calculate confidence
    confidence = 0.5
    if subjects: confidence += 0.3
    if categories: confidence += 0.1
    if len(tags) > 5: confidence += 0.05
    if confidence > 0.95: confidence = 0.95
    
    # Adjust for media type in asset_type if needed
    asset_type = "image"
    if args.media_type == "video":
        asset_type = "video"
    
    result = {
        "asset_id": "", # To be filled by caller
        "asset_type": asset_type,
        "semantic_tier": "generated_light",
        "source": "generated",
        "media_type": args.media_type,
        "generator": args.generator,
        "prompt_original": clean_prompt,
        "semantic_description": build_description(clean_prompt, subjects, categories, args.media_type),
        "search_text": search_text,
        "subjects": subjects,
        "subject_slugs": subject_slugs,
        "tags": tags,
        "categories": categories,
        "mood": mood,
        "style": [args.style] if args.style else [],
        "confidence": round(confidence, 2),
        "embedding_status": "pending",
        "created_at": datetime.utcnow().isoformat() + "Z"
    }
    
    print(json.dumps(result, indent=2))

if __name__ == "__main__":
    main()
