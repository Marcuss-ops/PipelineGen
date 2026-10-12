"""Small compatibility fixes for the Microsoft Edge TTS service migration.

edge-tts 7.2.8 still targets the legacy synthesis host, which began returning
HTTP 403 in October 2026. Keep the workaround at the bridge boundary until an
upstream release includes the fix, so both the persistent sidecar and legacy
CLI use the same endpoint without editing site-packages.
"""

from datetime import timezone
from email.utils import parsedate_to_datetime
from importlib import import_module
from typing import Optional

from edge_tts.constants import TRUSTED_CLIENT_TOKEN
from edge_tts.drm import DRM


def _parse_http_date(value: str) -> Optional[float]:
    """Parse an HTTP Date header independently of the process locale."""
    try:
        parsed = parsedate_to_datetime(value)
    except (TypeError, ValueError, IndexError):
        return None
    if parsed.tzinfo is None:
        parsed = parsed.replace(tzinfo=timezone.utc)
    return parsed.timestamp()


def apply_edge_tts_compat() -> None:
    """Route synthesis to the current Microsoft host and fix date parsing."""
    communicate_module = import_module("edge_tts.communicate")
    communicate_module.WSS_URL = (
        "wss://api.msedgeservices.com/tts/cognitiveservices/websocket/v1"
        f"?Ocp-Apim-Subscription-Key={TRUSTED_CLIENT_TOKEN}"
    )
    DRM.parse_rfc2616_date = staticmethod(_parse_http_date)
