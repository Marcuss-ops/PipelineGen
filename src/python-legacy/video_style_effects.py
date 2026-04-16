"""
Compat shim (legacy import path).

Alcuni worker/utility importano ancora `video_style_effects` dalla root di `refactored/`.
Il codice reale è in `modules.video.video_style_effects`.
"""

from modules.video.video_style_effects import *  # noqa: F403

