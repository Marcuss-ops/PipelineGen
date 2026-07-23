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


@dataclass
class SynthesizeRequest:
    """Validated JSON-RPC body for POST /synthesize."""

    text: str
    out: str
    lang: str = "en"
    voice: str = ""

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

        return cls(text=text, out=out_path, lang=lang, voice=voice)


def normalize_language(lang: str) -> str:
    """Return a lower-cased, stripped BCP-47 language tag.

    This is the canonical normalizer used by both the CLI and HTTP
    surfaces before voice resolution.
    """
    return str(lang or "en").strip().lower() or "en"
