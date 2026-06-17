"""Semantic tagger for generated and retrieved media assets.

Enrichment strategy:
- Taxonomy matching (YAML-based, fast, deterministic)
- LLM enrichment via Ollama (one-time at ingest, NOT at search time)
  Generates: concept_tags, visual_objects, emotional_tone, search_text_expanded
- search_text_expanded is stored in DB and used for FTS5/BM25 + vector search
  with zero LLM calls at query time.

Package layout (split out from a 624-line single file):

  __init__.py        — sys.path setup for vlm_client + re-exports.
  taxonomy.py        — SYSTEM_WORDS, load_taxonomy, normalize, match_taxonomy.
  text_processing.py — clean_generated_prompt, filter, compact, build_desc.
  extraction.py      — YAKE keywords + spaCy entities.
  llm.py             — call_ollama, translate_metadata, prompt templates.
  vlm.py             — VLM visual analysis (memory tag).
  orchestrator.py    — assemble payload, build final response.
  __main__.py        — argparse + invoke orchestrator.

Entry point: `python3 -m scripts.bridges.semantic_tagger …` (Go caller in
internal/media/semantic/tagger.go has been updated accordingly).
"""

# The original semantic_tagger.py inserted google-accounting on sys.path so
# the sibling vlm_client module could be imported. With a package install
# the same trick still works, but the relative depth changed: when this
# __init__.py lives at `<root>/scripts/bridges/semantic_tagger/__init__.py`
# we need THREE `..` segments to reach the project root, then append the
# google-accounting leaf.
import os
import sys

_HERE = os.path.dirname(os.path.abspath(__file__))
_GOOGLE_ACCOUNTING_PATH = os.path.normpath(
    os.path.join(_HERE, os.pardir, os.pardir, os.pardir, "google-accounting")
)
if _GOOGLE_ACCOUNTING_PATH not in sys.path:
    sys.path.insert(0, _GOOGLE_ACCOUNTING_PATH)

# Optional VLM client (graceful fallback if not available)
try:
    from vlm_client import analyze_image, parse_vlm_json  # noqa: F401

    VLM_AVAILABLE = True
except ImportError:
    VLM_AVAILABLE = False
    print(
        "[semantic_tagger] VLM client not available: visual analysis disabled",
        file=sys.stderr,
    )

# Optional spacy / yake — both fall back gracefully to empty lists.
try:
    import spacy

    _NLP = spacy.load("en_core_web_sm")
except Exception:
    _NLP = None
    print(
        "[semantic_tagger] spaCy not loaded: entity extraction disabled",
        file=sys.stderr,
    )

try:
    import yake  # noqa: F401
except ImportError:
    yake = None
    print(
        "[semantic_tagger] YAKE not loaded: keyword extraction disabled",
        file=sys.stderr,
    )

# Lazy accessors so callers can grab the globals without importers having to
# re-import directly.
def get_nlp():
    return _NLP


def get_yake():
    return yake


# Re-exports for callers that did `from scripts.bridges.semantic_tagger import X`.
from .taxonomy import (  # noqa: E402
    SYSTEM_WORDS,
    load_taxonomy,
    normalize,
    match_taxonomy,
    clean_generated_prompt,
    filter_system_words,
)
from .text_processing import (  # noqa: E402
    compact_phrase,
    compact_list,
    compact_subjects,
    unique_search_text,
    build_description,
    build_search_text_expanded,
)
from .extraction import extract_keywords, extract_entities  # noqa: E402
from .llm import (  # noqa: E402
    call_ollama,
    call_ollama_compat,  # legacy alias
    translate_metadata,
    LLM_ENRICHMENT_PROMPT,
    TRANSLATION_PROMPT,
    LANGUAGE_NAMES,
)
from .vlm import call_vlm_visual, VLM_VISUAL_TAG_PROMPT  # noqa: E402

__all__ = [
    # taxonomy
    "SYSTEM_WORDS",
    "load_taxonomy",
    "normalize",
    "match_taxonomy",
    "clean_generated_prompt",
    "filter_system_words",
    "compact_phrase",
    "compact_list",
    "compact_subjects",
    "unique_search_text",
    "build_description",
    "build_search_text_expanded",
    # extraction + llm + vlm
    "extract_keywords",
    "extract_entities",
    "call_ollama",
    "call_ollama_compat",
    "translate_metadata",
    "LLM_ENRICHMENT_PROMPT",
    "TRANSLATION_PROMPT",
    "LANGUAGE_NAMES",
    "call_vlm_visual",
    "VLM_VISUAL_TAG_PROMPT",
    # module handles
    "get_nlp",
    "get_yake",
    "VLM_AVAILABLE",
]
