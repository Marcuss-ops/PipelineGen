"""LLM enrichment via Ollama.

Both `call_ollama` (concept_tags / visual_objects / emotional_tone enrichment)
and `translate_metadata` are invoked at INGEST time only and their results
are stored in the DB. At query time we read `search_text` directly with no
recurring LLM calls.

The actual Ollama HTTP call is delegated to `scripts.core.ollama_client`
(shared client used across this repo) — we never call Ollama directly so
JSON-cleanup logic stays consistent across tagger / agent / sound-designer.
"""

import json
import sys

from scripts.core.ollama_client import OllamaError, generate_json as _ollama_generate_json

LLM_ENRICHMENT_PROMPT = """\
You are a media metadata specialist. Given a search query or prompt for a {media_type} asset, \
return ONLY a valid JSON object with exactly these 3 keys:

- "concept_tags": list of 5-12 conceptual keywords and synonyms that capture the \
abstract meaning and searchable concepts (not just literal words from the prompt)
- "visual_objects": list of 4-10 physical objects or visual elements likely present \
in the media asset given the prompt
- "emotional_tone": list of 3-6 psychological or emotional tones that describe the \
feeling or intent of the media asset

Prompt/Query: "{prompt}"
Style/Context: "{style}"

Respond with ONLY the JSON object, no explanation, no markdown, no code blocks."""

TRANSLATION_PROMPT = """\
You are a professional translator. Translate the following metadata fields to {target_language}.
Preserve the original meaning, style, and tone.

Input JSON:
{fields_json}

Return ONLY a JSON object with the same keys containing the translated values.
No explanations, no markdown, no code blocks."""

LANGUAGE_NAMES = {
    "en": "English", "es": "Spanish", "fr": "French", "de": "German",
    "it": "Italian", "pt": "Portuguese", "pl": "Polish", "nl": "Dutch",
    "ja": "Japanese", "ko": "Korean", "ru": "Russian", "tr": "Turkish",
    "id": "Indonesian", "zh": "Chinese", "ar": "Arabic", "hi": "Hindi",
}


def _ollama_call_generate_json(prompt, temperature=0.2, num_predict=400, timeout=30,
                                 ollama_url=None, ollama_model=None):
    """Call Ollama via shared client and return parsed JSON dict."""
    options = {"temperature": temperature, "num_predict": num_predict}
    kwargs = {}
    if ollama_model:
        kwargs["model"] = ollama_model
    if ollama_url:
        kwargs["host"] = ollama_url
    return _ollama_generate_json(
        prompt=prompt, options=options, timeout=timeout, cleanup_markdown=True, **kwargs
    )


def call_ollama(prompt, style, media_type, ollama_url, model):
    empty = {"concept_tags": [], "visual_objects": [], "emotional_tone": []}
    if not ollama_url or not model:
        return empty

    llm_prompt = LLM_ENRICHMENT_PROMPT.format(
        media_type=media_type,
        prompt=prompt[:800],
        style=style or "general",
    )

    try:
        parsed = _ollama_call_generate_json(llm_prompt, ollama_url=ollama_url, ollama_model=model)
        return {
            "concept_tags": [str(t) for t in parsed.get("concept_tags", []) if t],
            "visual_objects": [str(t) for t in parsed.get("visual_objects", []) if t],
            "emotional_tone": [str(t) for t in parsed.get("emotional_tone", []) if t],
        }
    except (OllamaError, json.JSONDecodeError, KeyError, Exception) as e:
        print(f"[semantic_tagger] Ollama enrichment failed (non-fatal): {e}", file=sys.stderr)
        return empty


# Legacy alias kept so the rest of the codebase (and the orchestrator)
# can use a stable name regardless of how `llm` was historically imported.
call_ollama_compat = call_ollama


def translate_metadata(ollama_url, model, fields, target_language):
    if not ollama_url or not model or not fields:
        return {}

    lang_name = LANGUAGE_NAMES.get(target_language.lower(), target_language)
    llm_prompt = TRANSLATION_PROMPT.format(
        target_language=lang_name,
        fields_json=json.dumps(fields, ensure_ascii=False, indent=2),
    )

    try:
        return _ollama_call_generate_json(
            llm_prompt,
            temperature=0.1,
            num_predict=800,
            timeout=60,
            ollama_url=ollama_url,
            ollama_model=model,
        )
    except (OllamaError, json.JSONDecodeError, KeyError, Exception) as e:
        print(f"[semantic_tagger] Translation to {target_language} failed (non-fatal): {e}", file=sys.stderr)
        return {}
