"""Shared typed state for the slide-worker generation layers.

This module intentionally has no imports from the orchestration or DOM
layers. Keeping the state bundle here prevents circular imports between the
runner and the extracted step modules.
"""

from __future__ import annotations

from dataclasses import dataclass, field
from typing import Optional


@dataclass
class GenerationContext:
    """Per-request mutable state bundle passed through generation steps."""

    request_id: str
    prompt: str
    original_prompt: str
    prompt_text: str
    output_path: str
    style_id: str
    generation_id: str
    req_width: int
    req_height: int
    ratio: str
    image_mode_active: bool = False
    ratio_selected: str = ""
    baseline_candidates: list = field(default_factory=list)
    baseline_src_set: set = field(default_factory=set)
    matched_candidate_meta: Optional[dict] = None
    natural_w: int = 0
    natural_h: int = 0
    complete: bool = False
    image_bytes: bytes = b""
    fetch_method: str = ""
    saved: bool = False
    saved_format: str = ""
    candidate_records: list = field(default_factory=list)
    pixel_stats: dict = field(default_factory=dict)
    t0: float = 0.0


class StepError(Exception):
    """Typed step failure surfaced by the generation orchestrator."""

    def __init__(
        self,
        code: str,
        *,
        screenshot_path=None,
        diag_extra: Optional[dict] = None,
        error_message: str = "",
        **legacy_kwargs,
    ):
        super().__init__(f"{code}: {error_message}" if error_message else code)
        self.code = code
        self.screenshot_path = screenshot_path
        self.error_message = error_message
        self.diag_extra = dict(diag_extra or {})
        self.diag_extra.update(legacy_kwargs)
