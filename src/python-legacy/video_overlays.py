"""
Compat shim (legacy import path).

Alcuni worker/utility importano ancora `video_overlays` dalla root di `refactored/`.
Il codice reale è in `modules.video.video_overlays`.
"""

from modules.video.video_overlays import *  # noqa: F403

