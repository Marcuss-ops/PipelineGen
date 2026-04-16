"""
Utils Module - Utilities & Configuration

Vedi modules/utils/README.md per documentazione completa.
"""

# Export principali
try:
    from .utils import *
    from .cache_manager import cache_manager
    from .prompts import *
except ImportError:
    pass

__all__ = []

