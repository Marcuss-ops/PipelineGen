
"""
Refactored Video Generation Module.
This file now acts as a wrapper around the modular `modules.video.generation` package.
"""

import os
import logging
import traceback
from typing import List, Optional, Dict, Any, Callable, Tuple

# Import the new Orchestrator
try:
    from modules.video.generation.orchestrator import VideoGenerationOrchestrator
except ImportError:
    # Fallback for development structure if needed
    # Fallback for development structure if needed
    from .generation.orchestrator import VideoGenerationOrchestrator

try:
    from modules.video.upload.drive_uploader import upload_video_to_group_folder
except ImportError:
    # Fallback or optional
    upload_video_to_group_folder = None

# Logger setup
logger = logging.getLogger(__name__)

# Default Constants (kept for signature compatibility if referenced)
FONT_PATH_DEFAULT = "Arial" 

def generate_video_parallelized(
    start_clips_paths: Optional[List[str]],
    middle_clips_paths: Optional[List[str]],
    stock_clips_sources: Optional[List[str]],
    end_clips_paths: Optional[List[str]],
    audio_path: str,
    output_path: str,
    temp_dir: str,
    base_w: int = 1920,
    base_h: int = 1080,
    base_fps: float = 24.0,
    status_callback: Callable[[str, bool], None] = lambda m, error=False: None,
    max_workers_segment_gen: int = 1,
    max_workers_general: int = 4,
    background_video_for_img_overlays_path: Optional[str] = None,
    associazioni_finali_con_timestamp: Optional[Dict[str, Any]] = None,
    formatted_img_entities: Optional[Dict[str, Any]] = None,
    config_settings: Optional[Dict[str, Any]] = None,
    FONT_PATH_DEFAULT: str = FONT_PATH_DEFAULT,
    audio_language_for_srt: Optional[str] = None,
    segments_for_srt_generation: Optional[List[Dict[str, Any]]] = None,
    output_dir_in: Optional[str] = None,
    voiceover_name: Optional[str] = None,
) -> Tuple[Optional[str], str]:
    """
    Main entry point for video generation. 
    Refactored to delegate to VideoGenerationOrchestrator.
    """
    
    # helper to normalize inputs
    def _ensure_list_str(maybe_list: Any) -> List[str]:
        if maybe_list is None: return []
        if isinstance(maybe_list, str):
            s = maybe_list.strip()
            if "||" in s: s = s.split("||", 1)[0].strip()
            return [s] if s else []
        if isinstance(maybe_list, (list, tuple, set)):
            out: List[str] = []
            for x in maybe_list:
                if x is None: continue
                try:
                    if hasattr(x, "name"): x = getattr(x, "name")
                except Exception: pass
                
                try:
                    s = str(x).strip()
                    if s:
                        if "||" in s: s = s.split("||", 1)[0].strip()
                        out.append(s)
                except Exception: pass
            return out
        return []

    # Normalize inputs
    start_clips_paths = _ensure_list_str(start_clips_paths)
    middle_clips_paths = _ensure_list_str(middle_clips_paths)
    stock_clips_sources = _ensure_list_str(stock_clips_sources)
    end_clips_paths = _ensure_list_str(end_clips_paths)
    
    # Prepare Config
    if config_settings is None: config_settings = {}
    if voiceover_name and isinstance(voiceover_name, str):
        config_settings["voiceover_name"] = voiceover_name.strip()
    
    # Store worker counts in config if not present
    if "max_workers_stock_gen" not in config_settings:
        config_settings["max_workers_stock_gen"] = max_workers_segment_gen
    if "max_workers_general" not in config_settings:
        config_settings["max_workers_general"] = max_workers_general

    # Default Stock Remotion animation settings (every 5th stock, first 15s)
    if "enable_stock_remotion_tail" not in config_settings:
        config_settings["enable_stock_remotion_tail"] = True
    if "stock_remotion_every_n" not in config_settings:
        config_settings["stock_remotion_every_n"] = 5
    if "stock_remotion_tail_seconds" not in config_settings:
        config_settings["stock_remotion_tail_seconds"] = 15.0
        
    try:
        orchestrator = VideoGenerationOrchestrator(
            audio_path=audio_path,
            output_path=output_path,
            config_settings=config_settings,
            base_w=base_w,
            base_h=base_h,
            base_fps=base_fps,
            status_callback=status_callback,
            temp_dir=temp_dir,
            start_clips_paths=start_clips_paths,
            middle_clips_paths=middle_clips_paths,
            stock_clips_sources=stock_clips_sources,
            end_clips_paths=end_clips_paths,
            background_video_for_img_overlays_path=background_video_for_img_overlays_path,
            associazioni_finali_con_timestamp=associazioni_finali_con_timestamp,
            formatted_img_entities=formatted_img_entities,
            audio_language_for_srt=audio_language_for_srt,
            segments_for_srt_generation=segments_for_srt_generation,
            font_path_default=FONT_PATH_DEFAULT
        )
        
        final_video_path = orchestrator.run()
        
        if final_video_path and os.path.exists(final_video_path):
             # --- AUTO UPLOAD TO DRIVE REMOVED FROM WORKER ---
             # The Master Server now handles this in /upload_completed_video
             # to avoid credential issues on worker nodes.
             pass
             # --- AUTO UPLOAD TO DRIVE END ---

             return final_video_path, ""
        else:
             return None, "Video generation failed (no output file)."
             
    except Exception as e:
        err_msg = f"Error in generate_video_parallelized: {str(e)}\n{traceback.format_exc()}"
        status_callback(err_msg, True)
        logger.error(err_msg)
        return None, err_msg
