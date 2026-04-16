
"""
MODULE: Generation Context
DESCRIPTION:
This module defines the `GenerationContext` dataclass, which serves as the shared state container 
for the entire video generation lifecycle. It eliminates the need to pass dozens of arguments between functions.

RESPONSIBILITIES:
- Store static configuration (paths, resolution, fps).
- Store runtime state (timestamps, generated file paths, counters).
- Provide centralized access to logging/status callbacks.
- Hold references to data loaded from external sources (formatted_img_entities, etc.).

INTERACTIONS:
- Created by: `VideoGenerationOrchestrator`.
- Passed to: Every `BasePhase` and `BaseEntityHandler`.
- Modifiable by: Phases (to update state like `audio_segments`, `temp_files_to_clean`).
"""
from dataclasses import dataclass, field
from typing import List, Optional, Dict, Any, Callable, Tuple
import os

@dataclass
class GenerationContext:
    # Arguments
    audio_path: str
    output_path: str
    temp_dir: str
    base_w: int
    base_h: int
    base_fps: float
    status_callback: Callable[[str, bool], None]
    
    # Config
    config_settings: Dict[str, Any]
    font_path_default: str
    
    # Content Sources
    start_clips_paths: List[str] = field(default_factory=list)
    middle_clips_paths: List[str] = field(default_factory=list)
    stock_clips_sources: List[str] = field(default_factory=list)
    end_clips_paths: List[str] = field(default_factory=list)
    
    # Optional / Advanced
    background_video_for_img_overlays_path: Optional[str] = None
    associazioni_finali_con_timestamp: Optional[Dict[str, Any]] = None
    formatted_img_entities: Optional[Dict[str, Any]] = None
    audio_language_for_srt: Optional[str] = None
    segments_for_srt_generation: Optional[List[Dict[str, Any]]] = None
    output_dir_in: Optional[str] = None
    
    # Runtime State
    overlay_engine: str = "remotion"
    video_style: str = "young"
    temp_files_to_clean: List[str] = field(default_factory=list)
    
    # Overlay Runtime State
    text_overlay_count: int = 0
    all_rendered_overlay_files_for_ffmpeg_merge: List[Tuple[str, float, float]] = field(default_factory=list)
    temp_files_to_clean_overlays: List[str] = field(default_factory=list)
    
    # Runtime Stock/MoviePy Objects (initialized in phases)
    stock_clips_for_text_overlays: List[Any] = field(default_factory=list) # List[VideoFileClip]
    background_clip_for_moviepy_overlays: Any = None # Optional[VideoFileClip]
    stock_segment_results: Dict[int, Tuple[str, float]] = field(default_factory=dict)
    
    # Audio (set by AudioPhase)
    audio_duration: float = 0.0

    # Intermediate State for Mapping
    total_start_duration: float = 0.0
    middle_clip_actual_durations: List[float] = field(default_factory=list)
    middle_clip_timestamps_abs: List[Tuple[float, float]] = field(default_factory=list)
    stock_tasks_args_list: List[Dict[str, Any]] = field(default_factory=list)
    video_duration_for_overlays_limit: float = 0.0
    
    # Entity Stack State
    entity_stack_segments: List[Tuple[float, float]] = field(default_factory=list)
    
    # Base video (set by Orchestrator after AssemblyPhase, used by OverlayPhase for entity background)
    base_video_path: Optional[str] = None
    # When True, Assembly has already baked master audio (VO from 0 + music); Finalization should not rebuild it
    audio_baked_in_assembly: bool = False

    # Computed
    main_label: str = ""

    def __post_init__(self):
        if not self.main_label:
            self.main_label = f"[GenVidPar:{os.path.basename(self.audio_path)}]"

    def main_label_safe(self) -> str:
        """Label safe for use in file paths (no brackets/colons that can break FFmpeg or shells)."""
        s = (self.main_label or "").strip()
        for c in ("[", "]", ":", "*", "?", '"', "'", "|", "\\"):
            s = s.replace(c, "_")
        return s.strip("_") or "video"
