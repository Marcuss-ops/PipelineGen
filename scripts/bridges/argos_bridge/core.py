"""Shared Argos Translate core: BCP-47 mapping + translation.

Single owner for the language-code normalisation and the translate call so
the CLI and the persistent server cannot drift. Argos Translate is
deterministic and CPU-only; models must be pre-installed via
scripts/tools/argos_install_models.py.
"""

# ISO 639-1 base code for every BCP-47 tag the multilingual registry can
# emit. Argos keys language models on the 2-letter base (regional variants
# collapse: pt-BR -> pt, en-US -> en).
_BCP47_TO_ARGOS = {
    "en": "en",
    "it": "it",
    "es": "es",
    "fr": "fr",
    "de": "de",
    "pt": "pt",
    "pl": "pl",
    "ru": "ru",
    "tr": "tr",
    "id": "id",
    "nl": "nl",
    "ja": "ja",
    "ko": "ko",
    "zh": "zh",
    "ar": "ar",
    "sv": "sv",
    "da": "da",
    "fi": "fi",
    "no": "no",
    "cs": "cs",
    "hu": "hu",
    "ro": "ro",
    "el": "el",
    "he": "he",
    "th": "th",
    "vi": "vi",
    "ms": "ms",
    "uk": "uk",
    "hr": "hr",
    "sr": "sr",
    "bg": "bg",
    "sk": "sk",
    "sl": "sl",
    "lt": "lt",
    "lv": "lv",
    "et": "et",
    "ca": "ca",
    "gl": "gl",
    "eu": "eu",
}


def bcp47_to_argos(code):
    """Normalise a BCP-47 tag to an Argos Translate language code."""
    if not code:
        return ""
    code = str(code).strip().lower().replace("_", "-")
    base = code.split("-")[0]
    return _BCP47_TO_ARGOS.get(base, base)


def translate_text(text, source, target):
    """Translate text via Argos Translate, returning the canonical envelope.

    Returns {"translated_text", "source", "target", "model", "via"} on
    success or {"error"} on failure (missing install, unsupported pair,
    empty output).
    """
    try:
        import argostranslate.translate as at
    except ImportError:
        return {
            "error": "argostranslate not installed. Run: pip3 install argostranslate "
            "and install models via scripts/tools/argos_install_models.py"
        }

    src = bcp47_to_argos(source)
    tgt = bcp47_to_argos(target)

    if not src or src == "und":
        return {"error": "invalid/undetermined source language: %r" % source}
    if not tgt or tgt == "und":
        return {"error": "invalid target language: %r" % target}

    if src == tgt:
        return {
            "translated_text": text,
            "source": src,
            "target": tgt,
            "model": "argos-%s-%s" % (src, tgt),
            "via": "identity",
        }

    try:
        result = at.translate(text, src, tgt)
    except Exception as exc:  # noqa: BLE001 — surface ANY runtime error as JSON
        return {"error": "argos translate(%s->%s) failed: %s" % (src, tgt, exc)}

    translated = str(result).strip()
    if not translated:
        return {"error": "argos returned empty for %s->%s" % (src, tgt)}

    # Argos pivots through English when no direct model exists; report the
    # path so the Go side can surface it for observability.
    via = "direct"
    if src != "en" and tgt != "en":
        via = "pivot"

    return {
        "translated_text": translated,
        "source": src,
        "target": tgt,
        "model": "argos-%s-%s" % (src, tgt),
        "via": via,
    }
