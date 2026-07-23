"""Voice resolution for the Edge TTS sidecar.

This module resolves the voice identifier used by edge-tts. The
business registry of voices lives in the Go backend
(`cfg.Media.Multilingual.Languages[*].edge_tts_voice`); the Python
sidecar only keeps a tiny emergency technical fallback for when the
caller does not supply an explicit voice.

Resolution order:
  1. Explicit `voice` argument → return it verbatim.
  2. Network fallback via `edge_tts.list_voices()` → best locale match.
  3. Last-resort emergency fallback → 'en-US-AriaNeural'.
"""

import sys
from typing import Optional

from edge_tts import list_voices


# Minimal emergency technical fallback. This is NOT the business
# registry of voices; it exists only so the sidecar can produce audio
# when the caller cannot provide a voice and the network is unavailable.
_EMERGENCY_FALLBACK = "en-US-AriaNeural"


async def resolve_voice(lang: str, voice: Optional[str] = None) -> str:
    """Resolve the best Edge TTS voice for a language code.

    Args:
        lang: BCP-47 language code (e.g. "it", "en-US").
        voice: Optional explicit voice identifier. When provided it
            takes precedence and is returned as-is.

    Returns:
        A voice ShortName suitable for `edge_tts.Communicate`.
    """
    if voice:
        return voice

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
