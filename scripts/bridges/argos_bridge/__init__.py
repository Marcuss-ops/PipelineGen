"""Argos Translate bridge package.

Shared core (BCP-47 mapping + translation) consumed by both the one-shot
CLI (argos_translator.py) and the persistent HTTP sidecar
(argos_server.py), mirroring the edge_tts_bridge split.
"""

from .core import bcp47_to_argos, translate_text

__all__ = ["bcp47_to_argos", "translate_text"]
