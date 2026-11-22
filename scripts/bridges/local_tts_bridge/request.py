"""Request parsing for the local (open-weight) TTS sidecar.

This module owns the validated JSON-RPC body schema for POST /synthesize
and the English-only language gate used by the pilot. It mirrors the
edge_tts_bridge wire contract so the Go processor can talk to either
sidecar without a second protocol.

The gate is intentionally fail-closed: the pilot is certified for English
only. A request for `it`, `fr`, `pl`, ... is rejected at the boundary
instead of silently producing an accented English voice.
"""

import os
from dataclasses import dataclass
from typing import Any, Tuple


class SynthesizeRequestError(Exception):
    """Raised on shape / policy errors. Forwarded verbatim as `error`."""

    def __init__(self, message: str):
        super().__init__(message)
        self.message = message


# Canonical boundary values accepted on the wire. Local engines have no
# native word boundaries yet, so `none` is the honest default. `word` and
# `sentence` are accepted for wire compatibility and ignored (zero
# boundaries are returned), which lets the caller's best_effort timing
# policy degrade instead of failing the whole synthesis.
_BOUNDARY_MODES = ("word", "sentence", "none")


def allowed_languages() -> Tuple[str, ...]:
    """Return the base language codes the pilot is allowed to serve."""
    raw = os.environ.get("LOCAL_TTS_LANGS", "en")
    codes = tuple(part.strip().lower() for part in raw.split(",") if part.strip())
    return codes or ("en",)


def base_language(lang: str) -> str:
    """Return the ISO-639-1 base code for a BCP-47 tag (`en-US` -> `en`)."""
    normalized = str(lang or "en").strip().lower().replace("_", "-")
    return normalized.split("-", 1)[0] or "en"


def normalize_rate(rate: Any) -> str:
    """Validate and canonicalise a speaking rate.

    Accepted wire values are signed integer percentages in the same window
    as the Edge bridge: "-50%" .. "+100%". The empty string means "engine
    default" and is preserved as "". Multiplier spellings ("1.1x") are
    normalised by the Go boundary before they reach either sidecar.
    """
    raw = str(rate or "").strip()
    if not raw:
        return ""
    if not raw.endswith("%"):
        raise SynthesizeRequestError(
            f'invalid "rate" value: {raw!r} '
            '(expected a signed percentage in [-50%, +100%], e.g. "+10%")'
        )

    number = raw[:-1]
    if number[:1] not in ("+", "-"):
        raise SynthesizeRequestError(
            f'invalid "rate" value: {raw!r} '
            '(expected a signed percentage in [-50%, +100%], e.g. "+10%")'
        )
    sign = -1 if number[0] == "-" else 1
    number = number[1:]
    if not number.isdigit() or len(number) > 3:
        raise SynthesizeRequestError(
            f'invalid "rate" value: {raw!r} '
            '(expected a signed percentage in [-50%, +100%], e.g. "+10%")'
        )

    value = sign * int(number)
    if value < -50 or value > 100:
        raise SynthesizeRequestError(
            f'invalid "rate" value: {raw!r} (expected -50%..+100%)'
        )
    return f"{value:+d}%"


def atempo_filter(rate: Any) -> list:
    """Return the FFmpeg args that apply `rate` through the atempo filter.

    Local engines synthesize at their native speed, so the only portable way
    to honour a caller rate is a post-synthesis tempo change. A single atempo
    instance covers the whole accepted window because the factor stays within
    [0.5, 2.0]. An empty list means identity (no filter).
    """
    normalized = normalize_rate(rate)
    if not normalized:
        return []
    percent = int(normalized[:-1])
    if percent == 0:
        return []
    factor = 1.0 + percent / 100.0
    return ["-af", f"atempo={factor:g}"]


@dataclass
class SynthesizeRequest:
    """Validated JSON-RPC body for POST /synthesize."""

    text: str
    out: str
    engine: str
    lang: str = "en"
    voice: str = ""
    rate: str = ""
    boundary: str = "none"

    @classmethod
    def from_dict(cls, body: Any, default_engine: str) -> "SynthesizeRequest":
        if not isinstance(body, dict):
            raise SynthesizeRequestError("request body must be a JSON object")

        text = str(body.get("text") or "").strip()
        if not text:
            raise SynthesizeRequestError('missing "text" field')

        out_path = str(body.get("out") or "").strip()
        if not out_path:
            raise SynthesizeRequestError('missing "out" field')

        engine = str(body.get("engine") or default_engine).strip().lower()
        if not engine:
            raise SynthesizeRequestError('missing "engine" field')

        lang = str(body.get("lang") or "en").strip().lower() or "en"
        allowed = allowed_languages()
        if base_language(lang) not in allowed:
            raise SynthesizeRequestError(
                f"language {lang!r} is outside the local TTS pilot scope "
                f"({', '.join(allowed)}); route it to the Edge bridge"
            )

        voice = str(body.get("voice") or "").strip()
        rate = normalize_rate(body.get("rate") or "")

        boundary = str(body.get("boundary") or "none").strip().lower() or "none"
        if boundary not in _BOUNDARY_MODES:
            raise SynthesizeRequestError(
                f'invalid "boundary" value: {boundary!r} '
                f'(expected one of: {", ".join(_BOUNDARY_MODES)})'
            )

        return cls(
            text=text,
            out=out_path,
            engine=engine,
            lang=lang,
            voice=voice,
            rate=rate,
            boundary=boundary,
        )
