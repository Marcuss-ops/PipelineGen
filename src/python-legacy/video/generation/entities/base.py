
"""
MODULE: Base Entity Handler
DESCRIPTION:
This module defines the abstract base class `BaseEntityHandler`. All specific entity handlers 
must inherit from this class to ensure a consistent interface.

RESPONSIBILITIES:
- Define the contract (`process` method) for entity handlers.
- Store a reference to the `GenerationContext`.
- Provide common helper methods if needed (currently minimal).

INTERACTIONS:
- Inherited by: `FrasiImportantiHandler`, `NumeriHandler`, etc.
- Used by: `EntityManager` (to type-hint and manage handlers).
"""
from abc import ABC, abstractmethod
from typing import Any, Dict
from ..common.context import GenerationContext

class BaseEntityHandler(ABC):
    def __init__(self, context: GenerationContext):
        self.context = context

    @abstractmethod
    def process(self, segment: Dict[str, Any], segment_idx: int) -> bool:
        """
        Process the segment.
        Returns True if processed (overlay created/handled), False if skipped/failed.
        Implementation should append to context.all_rendered_overlay_files_for_ffmpeg_merge
        and context.temp_files_to_clean_overlays if successful.
        """
        pass
