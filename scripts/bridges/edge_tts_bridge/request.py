"""Request parsing and language normalization for the Edge TTS sidecar.

This module owns the validated JSON-RPC body schema for POST /synthesize
and the canonical language-code normalizer. Keeping it separate from
the server and voice resolver makes the schema testable and avoids
duplicating request logic between the CLI and HTTP entry points.
"""

from dataclasses import dataclass
from typing import Any, Dict


class SynthesizeRequestError(Exception):
    """Raised by SynthesizeRequest.from_dict on shape errors.

    The orchestrator (handle_synthesize) catches this and forwards
    `message` verbatim as the JSON-RPC `error` field. No `code`
    attribute on the response shape — the pre-fix 400 surface was
    `{"ok": false, "error": "..."}` only and we keep it byte-equivalent.
    """

    def __init__(self, message: str):
        super().__init__(message)
        self.message = message


# Canonical boundary values accepted on the wire. The canonical
# PipelineGen timing contract is word-level; sentence is kept for
# generic callers that want the edge-tts default semantics explicitly.
_BOUNDARY_MODES = ("word", "sentence")

# Wire value → edge-tts Communicate boundary literal.
_EDGE_BOUNDARY_MODES = {
    "word": "WordBoundary",
    "sentence": "SentenceBoundary",
}


def edge_boundary_mode(boundary: str) -> str:
    """Map the canonical wire boundary value to the edge-tts literal.

    Unknown values fail closed to WordBoundary (the canonical mode for
    PipelineGen timing capture) rather than silently downgrading to the
    edge-tts default SentenceBoundary.
    """
    return _EDGE_BOUNDARY_MODES.get(boundary, "WordBoundary")


@dataclass
class SynthesizeRequest:
    """Validated JSON-RPC body for POST /synthesize."""

    text: str
    out: str
    lang: str = "en"
    voice: str = ""
    # boundary defaults to "word": edge-tts would otherwise silently
    # use SentenceBoundary. PipelineGen explicitly requests word-level
    # boundaries so timing capture never depends on a library default.
    boundary: str = "word"
    # allow_voice_fallback controls whether the voice resolver may
    # auto-select a voice when no explicit voice is provided.
    # Default False (production fail-closed): a missing voice is a
    # 400 error. Set to True only in debug/smoke-test contexts.
    allow_voice_fallback: bool = False

    @classmethod
    def from_dict(cls, body: Any) -> "SynthesizeRequest":
        """Validate + parse. Raises SynthesizeRequestError on shape errors."""
        if not isinstance(body, dict):
            raise SynthesizeRequestError("request body must be a JSON object")

        # Required field: text (non-empty after strip).
        text = str(body.get("text") or "").strip()
        if not text:
            # Catches missing key, None value, empty string, or
            # whitespace-only string. The pre-fix `body.get('text', '')`
            # accepted whitespace-only and let edge-tts fail at
            # voice.resolve — this surfaces the rejection at the
            # boundary so the typed 400 path fires. (Documented
            # behaviour change in the commit body.)
            raise SynthesizeRequestError('missing "text" field')

        # Required field: out.
        out_path = str(body.get("out") or "").strip()
        if not out_path:
            raise SynthesizeRequestError('missing "out" field')

        # Optional field: lang (default "en", normalised to lowercase).
        lang = str(body.get("lang") or "en").strip().lower() or "en"

        # Optional field: voice (verbatim, may be empty).
        voice = str(body.get("voice") or "").strip()

        # Optional field: boundary (word|sentence, default word).
        boundary = str(body.get("boundary") or "word").strip().lower() or "word"
        if boundary not in _BOUNDARY_MODES:
            raise SynthesizeRequestError(
                f'invalid "boundary" value: {boundary!r} '
                f'(expected one of: {", ".join(_BOUNDARY_MODES)})'
            )

        # Optional field: allow_voice_fallback (bool, default False).
        allow_fallback = body.get("allow_voice_fallback", False)
        if not isinstance(allow_fallback, bool):
            # Accept truthy/falsy for robustness but coerce to bool.
            allow_fallback = bool(allow_fallback)

        return cls(
            text=text, out=out_path, lang=lang, voice=voice,
            boundary=boundary, allow_voice_fallback=allow_fallback,
        )


def normalize_language(lang: str) -> str:
    """Return a lower-cased, stripped BCP-47 language tag.

    This is the canonical normalizer used by both the CLI and HTTP
    surfaces before voice resolution.
    """
    return str(lang or "en").strip().lower() or "en"
