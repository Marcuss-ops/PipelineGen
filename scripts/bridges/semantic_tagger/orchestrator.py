"""Tagging orchestrator: assemble the final payload from all enrichment sources.

The argparse wrapper in __main__.py produces an `args` namespace from CLI
flags. `build_payload(args)` returns the final JSON-serialisable dict that
__main__ prints to stdout. Keeping the assembly here lets __main__ stay a
thin CLI wrapper.
"""

from datetime import datetime

from .extraction import extract_entities, extract_keywords
from .llm import call_ollama, translate_metadata
from .taxonomy import (
    clean_generated_prompt,
    load_taxonomy,
    match_taxonomy,
    normalize,
)
from .text_processing import (
    build_description,
    build_search_text_expanded,
    compact_subjects,
    compact_list,
    filter_system_words,
    unique_search_text,
)
from .vlm import VLM_AVAILABLE, call_vlm_visual


def build_payload(args) -> dict:
    """Run the full enrichment pipeline. Pure function — no global state,
    no prints, returns a dict ready for `json.dumps`. The CLI wrapper in
    __main__.py is the only thing that knows about argparse."""
    # 1. Clean prompt
    clean_prompt = clean_generated_prompt(args.prompt)
    taxonomy = load_taxonomy(args.taxonomy)

    # 2. Taxonomy matching
    hits = match_taxonomy(clean_prompt, taxonomy)
    if args.style:
        for k, v in match_taxonomy(args.style, taxonomy).items():
            hits[k].extend(v)

    # 3. Dynamic extraction (YAKE + spaCy)
    yake_kws = extract_keywords(clean_prompt)
    spacy_ents = extract_entities(clean_prompt)

    # 4. LLM enrichment (Ollama — one-time at ingest, result stored in DB)
    llm_result = call_ollama(
        clean_prompt, args.style, args.media_type, args.ollama_url, args.ollama_model
    )
    concept_tags = llm_result["concept_tags"]
    visual_objects = llm_result["visual_objects"]
    emotional_tone = llm_result["emotional_tone"]

    # 4b. VLM visual analysis (OpenRouter — if image URL provided)
    vlm_visual = None
    if args.vlm_image_url:
        vlm_visual = call_vlm_visual(args.vlm_image_url, args.vlm_endpoint)
        if vlm_visual.get("visual_objects"):
            visual_objects = vlm_visual["visual_objects"] + [
                v for v in visual_objects if v not in vlm_visual["visual_objects"]
            ]
        if vlm_visual.get("mood"):
            existing_moods = {m.lower() for m in emotional_tone}
            for m in vlm_visual["mood"]:
                if m.lower() not in existing_moods:
                    emotional_tone.append(m)

    # 5. Style & Subject separation
    subject_candidates = (
        hits["subjects"] + spacy_ents + concept_tags + visual_objects + yake_kws
    )
    subjects = compact_subjects(clean_prompt, subject_candidates)
    subject_slugs = sorted(
        set(
            s.lower().replace(" ", "-").replace("_", "-")
            for s in subjects if s and s != "unknown"
        )
    )
    if not subject_slugs and subjects:
        subject_slugs = [
            s.lower().replace(" ", "-").replace("_", "-")
            for s in subjects if s and s != "unknown"
        ]

    # Handle style correctly: if it matches the subject/query/slug, do not list it as a style.
    style_list = []
    if args.style and args.style.strip():
        st = args.style.strip()

        def normalize_comparable(text):
            return text.lower().replace("-", "").replace(" ", "").replace("_", "").strip()

        is_subject_match = any(
            normalize_comparable(sub) in normalize_comparable(st)
            or normalize_comparable(st) in normalize_comparable(sub)
            for sub in subjects
        )
        if args.source_type == "retrieved" and (
            is_subject_match
            or st.lower() == clean_prompt.lower()
            or st.lower().replace("-", " ") == clean_prompt.lower()
        ):
            pass
        else:
            style_list.append(st)

    # Clean tags_list (remove duplicate subject references or system terms)
    tags_raw = (
        hits["tags"] + yake_kws + style_list + concept_tags + visual_objects + emotional_tone
    )
    tags_list = []
    seen_tags = set()
    subject_norms = {normalize(sub) for sub in subjects}
    for tag in tags_raw:
        from .text_processing import compact_phrase
        tag_phrase = compact_phrase(str(tag), max_words=4, max_chars=48)
        if not tag_phrase:
            continue
        tag_norm = normalize(tag_phrase)
        if tag_norm in seen_tags:
            continue
        if tag_norm == normalize(clean_prompt) or tag_norm in subject_norms:
            continue
        if any(tag_norm in sub or sub in tag_norm for sub in subject_norms):
            continue
        seen_tags.add(tag_norm)
        tags_list.append(tag_phrase)
    tags_list = compact_list(
        filter_system_words(tags_list), max_items=12, max_words=4, max_chars=48
    )

    # For retrieved assets, clean up generic search/retrieval tag spam
    if args.source_type == "retrieved":
        tags_list = [
            t
            for t in tags_list
            if normalize(t)
            not in {
                "found", "wikipedia", "searxng", "duckduckgo",
                "download", "retrieved", "image", "video",
                "commons", "upload", "flickr",
            }
        ]
        tags_list = [
            t for t in tags_list
            if not any(normalize(t) in normalize(sub) for sub in subjects)
        ]
        tags_list = [
            t for t in tags_list
            if not any(normalize(sub) in normalize(t) for sub in subjects)
        ]

    categories = sorted(set(filter_system_words(hits["categories"])))
    if len(categories) > 2 and "composition" in categories:
        categories.remove("composition")
    mood = sorted(set(filter_system_words(hits["mood"])))

    # 5. Base search_text (pure elements, deduplicated, sorted, length > 3, no spam)
    clean_concept_tags = [
        t
        for t in (tags_list + categories + concept_tags + visual_objects + emotional_tone)
        if normalize(t) not in {
            "found", "wikipedia", "searxng", "duckduckgo",
            "download", "retrieved", "image", "video",
            "commons", "upload", "flickr",
        }
    ]
    search_text = unique_search_text(
        subjects,
        clean_concept_tags,
        [args.style.strip() if args.style and args.style.strip() else ""],
    )

    # 6. Build description & expanded search text
    semantic_desc = build_description(
        clean_prompt, subjects, categories,
        args.media_type, args.style.strip(),
        args.source_type, args.retriever,
    )

    # 8. Attribution Text
    author_val = args.author or "Unknown"
    license_val = args.license or "Unknown"
    attribution_text = (
        f"{', '.join(subjects) if subjects else clean_prompt}, "
        f"{author_val}, {license_val}"
    )

    # 9. Set semantic tier based on source type
    if args.source_type == "retrieved":
        semantic_tier = (
            "retrieved_rich" if (concept_tags or visual_objects) else "retrieved_light"
        )
    else:
        semantic_tier = (
            "generated_rich" if (concept_tags or visual_objects) else "generated_light"
        )
    asset_type = {
        "image": "image",
        "video": "video",
        "audio": "sound_effect",
        "voiceover": "voiceover",
    }.get(args.media_type, "image")

    # 10. Multi-language translations (Ollama — one-time at ingest)
    translations = {}
    translate_targets = (
        [t.strip() for t in args.translate_to.split(",") if t.strip()]
        if args.translate_to
        else []
    )
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
                fields_to_translate, target_lang,
            )
            if translated:
                translations[target_lang] = translated

    result = {
        "schema_version": "1.2",
        "asset_id": "",
        "asset_type": asset_type,
        "semantic_tier": semantic_tier,
        "source": args.source_type,
        "source_type": args.source_type,
        "media_type": args.media_type,
        "generator": args.generator if args.source_type == "generated" else "",
        "retriever": args.retriever if args.source_type == "retrieved" else "",
        "language": args.language or "en",
        "prompt_original": clean_prompt,
        "semantic_description": semantic_desc,
        "search_text": search_text,
        "concept_tags": concept_tags,
        "visual_objects": visual_objects,
        "emotional_tone": emotional_tone,
        "attribution_text": attribution_text,
        "subjects": subjects,
        "subject_slugs": subject_slugs,
        "tags": tags_list,
        "categories": categories,
        "mood": mood,
        "style": style_list,
        "embedding_status": "pending",
        "translations": translations,
        "created_at": datetime.utcnow().isoformat() + "Z",
    }

    # Add VLM visual analysis if available
    if vlm_visual:
        result["vlm_visual_analysis"] = {
            "scene_type": vlm_visual.get("scene_type", "other"),
            "visual_objects": vlm_visual.get("visual_objects", []),
            "mood": vlm_visual.get("mood", []),
            "text_on_screen": vlm_visual.get("text_on_screen", []),
            "dominant_colors": vlm_visual.get("dominant_colors", []),
            "composition": vlm_visual.get("composition", ""),
            "lighting": vlm_visual.get("lighting", ""),
            "raw_description": vlm_visual.get("raw_description", ""),
        }

    if args.source_type == "retrieved":
        result["retrieval"] = {
            "provider": args.retriever or "web",
            "page_url": args.page_url,
            "image_url": args.image_url,
            "license": args.license,
            "author": args.author,
        }

    return result
