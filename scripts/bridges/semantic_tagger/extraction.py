"""Dynamic extraction helpers — YAKE keywords + spaCy entities.

YAKE + spaCy are loaded lazily in `scripts.bridges.semantic_tagger.__init__`
and discovered at call time via `get_yake()` / `get_nlp()`. If either lib
is missing, these helpers return empty lists rather than blowing up — the
tagger still produces a valid payload, just without those signals.
"""

from . import get_nlp, get_yake


def extract_keywords(prompt):
    """YAKE keyword extraction. Returns empty list if YAKE unavailable."""
    yake = get_yake()
    if not yake:
        return []
    kw_extractor = yake.KeywordExtractor(
        lan="en", n=2, dedupLim=0.9, top=10, features=None
    )
    return [kw[0] for kw in kw_extractor.extract_keywords(prompt)]


def extract_entities(prompt):
    """spaCy named-entity extraction. Returns empty list if spaCy unavailable."""
    nlp = get_nlp()
    if not nlp:
        return []
    doc = nlp(prompt)
    return [
        ent.text
        for ent in doc.ents
        if ent.label_ in ["FAC", "GPE", "LOC", "PERSON", "NORP", "ORG", "EVENT"]
    ]
