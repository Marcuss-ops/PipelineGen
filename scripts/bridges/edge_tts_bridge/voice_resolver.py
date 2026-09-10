"""Voice resolution for the Edge TTS sidecar.

This module resolves the voice identifier used by edge-tts. The
business registry of voices lives in the Go backend
(`cfg.Media.Multilingual.Languages[*].edge_tts_voice`); the Python
sidecar only keeps a tiny emergency technical fallback for when the
caller explicitly opts into auto-resolution.

Production contract (PR-VO-EDGE-VOICE-FAIL-CLOSED, Aug 2026):
  - Production callers MUST pass an explicit voice identifier.
  - When `voice` is absent and `allow_fallback=False` (default),
    the resolver raises ValueError — no silent auto-selection.
  - When `allow_fallback=True` (debug / smoke tests only), the
    legacy resolution order applies:
      1. Network fallback via `edge_tts.list_voices()` → best locale match.
      2. Last-resort emergency fallback → 'en-US-AriaNeural'.
"""

import sys
from typing import Optional

from edge_tts import list_voices


# Minimal emergency technical fallback. This is NOT the business
# registry of voices; it is only reachable with allow_fallback=True.
_EMERGENCY_FALLBACK = "en-US-AriaNeural"


class VoiceResolutionError(Exception):
    """Raised when no explicit voice is provided and fallback is disabled."""
    pass


async def resolve_voice(
    lang: str,
    voice: Optional[str] = None,
    allow_fallback: bool = False,
) -> str:
    """Resolve the best Edge TTS voice for a language code.

    Args:
        lang: BCP-47 language code (e.g. "it", "en-US").
        voice: Optional explicit voice identifier. When provided it
            takes precedence and is returned as-is.
        allow_fallback: When False (default, production), raises
            VoiceResolutionError if no explicit voice is given.
            Set to True only in debug/smoke-test contexts.

    Returns:
        A voice ShortName suitable for `edge_tts.Communicate`.

    Raises:
        VoiceResolutionError: when no explicit voice is provided
            and allow_fallback is False.
    """
    if voice:
        return voice

    if not allow_fallback:
        raise VoiceResolutionError(
            f"No explicit voice provided for language {lang!r} "
            f"and voice auto-resolution is disabled in production. "
            f"Pass a concrete voice identifier or set allow_fallback=True "
            f"(debug / smoke tests only)."
        )

    # ── Legacy fallback path (allow_fallback=True only) ──────────────
    # Caller did not provide a voice — query edge-tts for a best match.
    # This is the emergency technical fallback path; the canonical
    # business voice mapping is supplied by the Go backend.
    try:
        voices = await list_voices()
    except Exception as exc:  # pylint: disable=broad-except
        sys.stderr.write(
            f"[tts_edge] list_voices network call failed for lang={lang!r}: "
            f"{type(exc).__name__}: {exc}\n"
        )
        sys.stderr.flush()
        voices = []

    if not voices:
        sys.stderr.write(
            f"[tts_edge] no voices available for lang={lang!r}: "
            f"returning emergency fallback {_EMERGENCY_FALLBACK!r}\n"
        )
        sys.stderr.flush()
        return _EMERGENCY_FALLBACK

    lang_lower = lang.lower()
    parts = lang_lower.split("-")
    base = parts[0]
    region = parts[1].upper() if len(parts) > 1 else None

    # Prefer an exact locale match.
    if region:
        for v in voices:
            if v["Locale"].lower() == lang_lower:
                return v["ShortName"]

    # Prefer a base-language match with a Multilingual voice.
    for v in voices:
        if v["Locale"].lower().startswith(f"{base}-"):
            if "Multilingual" in v["ShortName"]:
                return v["ShortName"]

    # Fall back to any base-language match.
    for v in voices:
        if v["Locale"].lower().startswith(f"{base}-"):
            return v["ShortName"]

    # Nothing matched the requested language — use the first voice
    # returned by the service as a last-ditch emergency fallback.
    sys.stderr.write(
        f"[tts_edge] no locale match for lang={lang!r}: "
        f"returning first network voice {voices[0]['ShortName']!r} "
        f"(locale={voices[0]['Locale']!r})\n"
    )
    sys.stderr.flush()
    return voices[0]["ShortName"]
