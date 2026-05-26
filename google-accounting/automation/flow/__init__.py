"""
Pacchetto automazione Google Flow.

Diviso in moduli:
  - config.py    : STYLE_MAP, selettori CSS/XPath, costanti
  - capture.py   : ImageCapturer — cattura immagini via network + DOM
  - engine.py    : ImageFXFlowAutomation — orchestratore principale
"""

from .config import STYLE_MAP
from .engine import ImageFXFlowAutomation, generate_flow_images

__all__ = [
    "ImageFXFlowAutomation",
    "STYLE_MAP",
    "generate_flow_images",
]