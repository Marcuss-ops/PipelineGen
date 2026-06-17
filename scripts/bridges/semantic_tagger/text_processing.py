"""Text processing utilities — phrase compaction, dedup, description building."""

import re

from .taxonomy import SYSTEM_WORDS, filter_system_words, normalize


def compact_phrase(text, max_words=5, max_chars=60):
    text = re.sub(r"[\[\]\(\)\{\}<>\"'`]+", " ", text or "")
    text = re.sub(r"[^\wÀ-ÿ\s-]+", " ", text, flags=re.UNICODE)
    words = []
    for raw in re.split(r"\s+", text.strip()):
        token = raw.strip("-_")
        norm = normalize(token)
        if not token or len(norm) < 3:
            continue
        if norm in SYSTEM_WORDS:
            continue
        words.append(token)
        if len(words) >= max_words:
            break
    if not words:
        return ""
    phrase = " ".join(words)
    if len(phrase) > max_chars:
        phrase = phrase[:max_chars].rstrip()
    return phrase.strip(" ,;:-")


def compact_list(values, max_items=12, max_words=4, max_chars=48):
    out = []
    seen = set()
    for value in values:
        if not value:
            continue
        phrase = compact_phrase(str(value), max_words=max_words, max_chars=max_chars)
        if not phrase:
            continue
        key = normalize(phrase)
        if key in seen:
            continue
        seen.add(key)
        out.append(phrase)
        if len(out) >= max_items:
            break
    return out


def compact_subjects(prompt, candidates):
    subjects = compact_list(candidates, max_items=3, max_words=6, max_chars=60)
    if subjects:
        return subjects

    # fall back: import on call to avoid hard dependency at module load
    from .extraction import extract_entities, extract_keywords

    keywords = extract_keywords(prompt) or []
    entities = extract_entities(prompt) or []
    subjects = compact_list(entities + keywords, max_items=3, max_words=6, max_chars=60)
    if subjects:
        return subjects

    fallback = compact_phrase(prompt, max_words=6, max_chars=60)
    return [fallback] if fallback else ["unknown"]


def unique_search_text(*groups):
    seen = set()
    tokens = []
    for group in groups:
        for item in group or []:
            if not item:
                continue
            for token in re.findall(r"[\wÀ-ÿ-]+", str(item).lower()):
                token = token.strip("-_")
                if len(token) < 3 or token in SYSTEM_WORDS or token in seen:
                    continue
                seen.add(token)
                tokens.append(token)
    return " ".join(sorted(tokens))


def build_description(prompt, subjects, categories, media_type, style="",
                      source_type="generated", retriever=""):
    media_labels = {
        "image": "image",
        "video": "video",
        "audio": "sound effect",
        "voiceover": "voiceover",
    }
    media_label = media_labels.get(media_type, "asset")
    sub_str = ", ".join(subjects) if subjects else "the subject"
    relevant_cats = [c for c in categories if c not in ["composition", "aesthetic"]]
    cat_str = f" related to {', '.join(relevant_cats[:3])}" if relevant_cats else ""
    style_str = f" in {style} style" if style else ""

    if source_type == "retrieved":
        prov = f" via {retriever}" if retriever else ""
        return f"A retrieved {media_label} of {sub_str}{cat_str} obtained{prov} for the query: '{prompt}'."
    return f"A generated {media_label} depicting {sub_str}{cat_str}{style_str}."


def build_search_text_expanded(
    prompt, subjects, tags, categories, mood,
    concept_tags, visual_objects, emotional_tone,
    style_list, semantic_description,
):
    """Builds a single flat text blob combining ALL semantic fields without spammy repetitions."""
    all_parts = (
        [prompt, semantic_description]
        + subjects + tags + categories + mood
        + concept_tags + visual_objects + emotional_tone
        + style_list
    )
    tokens = set()
    for part in all_parts:
        if not part:
            continue
        for token in normalize(part).split():
            token = re.sub(r"[^\w\s-]", "", token)
            if token not in SYSTEM_WORDS and len(token) > 2:
                tokens.add(token)

    phrases = set()
    for part in all_parts:
        p = normalize(part).strip()
        p = re.sub(r"[^\w\s-]", "", p)
        if p and len(p) > 3 and p not in SYSTEM_WORDS:
            if p != normalize(prompt).strip() and p != normalize(semantic_description).strip():
                phrases.add(p)

    return " ".join(sorted(tokens) + sorted(phrases - tokens))
