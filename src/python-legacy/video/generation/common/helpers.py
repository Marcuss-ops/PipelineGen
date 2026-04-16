"""
MODULE: Helpers
DESCRIPTION:
This module contains pure utility functions and helper logic that are shared across multiple phases 
and entities. It logic that doesn't belong to a specific phase but is needed globally.

RESPONSIBILITIES:
- Timestamp mapping (`map_audio_to_video`): synchronizing audio events with video timeline (accounting for intros/middle clips).
- Stock clip selection: finding the right background footage for overlays.
- Date normalization: parsing text to identify date formats.
- Overlap detection: ensuring entities don't collide visually.

INTERACTIONS:
- Used by: `OverlayPhase`, `EntityHandlers` (e.g., `DateHandler`, `ParoleImportanti`), `SegmentsPhase`.
- stateless: Most functions here are pure or take Context as an explicit dependency without retaining state.
"""
from typing import Optional, Any
from .context import GenerationContext

def get_stock_clip_for_overlay(context: GenerationContext, start_time: float, duration: float) -> Optional[Any]:
    """
    Select an appropriate stock clip for text overlay based on start time.
    Returns a MoviePy VideoFileClip or None.
    """
    callback = context.status_callback
    label = context.main_label
    
    if not context.stock_clips_for_text_overlays:
        if context.background_clip_for_moviepy_overlays is not None:
            return context.background_clip_for_moviepy_overlays
        return None
    
    count = len(context.stock_clips_for_text_overlays)
    if count == 0:
        return None
        
    clip_index = int(start_time) % count
    selected_clip = context.stock_clips_for_text_overlays[clip_index]
    
    if selected_clip.duration >= duration:
        return selected_clip
        
    # Prefer stock clip even if short (will be looped), unless we have a fallback background 
    # and the stock clip is VERY short (< 50% needed).
    if context.background_clip_for_moviepy_overlays is not None and selected_clip.duration < duration * 0.5:
        return context.background_clip_for_moviepy_overlays
        
    return selected_clip

def normalize_date_key(value: Any) -> str:
    try:
        return " ".join(str(value or "").strip().lower().split())
    except Exception:
        return ""

def text_contains_date(text_value: str, date_key: str) -> bool:
    if not text_value or not date_key:
        return False
    return date_key in text_value

def overlaps_entity_stack(start_v: float, end_v: float, segments: list) -> bool:
    """True if [start_v, end_v] overlaps with any entity stack segment."""
    if not segments:
        return False
    for (s, e) in segments:
        if max(start_v, s) < min(end_v, e): # Overlap condition
            return True
    return False

def map_audio_to_video(ctx: GenerationContext, audio_time_s: float) -> float:
    """
    Map audio timestamp to video timestamp accounting for intros, stock segments, and middle clips.
    """
    if audio_time_s < 0: return 0.0

    video_time_accum = ctx.total_start_duration  # Start after intro clips
    remaining_audio_time = audio_time_s

    # Use actual stock segment durations
    actual_stock_durations = []
    if ctx.stock_segment_results:
        sorted_indices = sorted(ctx.stock_segment_results.keys())
        for i in sorted_indices:
            if i in ctx.stock_segment_results:
                _, actual_dur = ctx.stock_segment_results[i]
                actual_stock_durations.append(actual_dur)
    else:
        # Fallback to planned durations from tasks if results empty (shouldn't happen in loop)
        # We assume ctx.stock_tasks_args_list is populated
        actual_stock_durations = [t.get("duration_needed", 5.0) for t in ctx.stock_tasks_args_list]

    middle_clip_idx = 0
    
    # Iterate segments to find position
    # This logic matches Generatevideoparallelized.py lines ~1860+
    # But simplified: we need to iterate segments and check if audio falls within.
    
    # Wait, the logic in original iterates through segments and accumulates video time.
    # It consumes remaining_audio_time.
    
    for i in range(len(actual_stock_durations)):
        stock_dur = actual_stock_durations[i]
        
        # Determine audio duration covered by this stock segment
        # We need info from stock_tasks to know how much audio is in this segment
        task = next((t for t in ctx.stock_tasks_args_list if t.get("segment_index") == i), None)
        # If task not found (shouldn't happen), assume stock dur covers audio dur
        audio_dur_in_segment = task.get("duration_needed", stock_dur) if task else stock_dur
        
        if remaining_audio_time <= audio_dur_in_segment:
            # Found the segment
            return video_time_accum + remaining_audio_time
        
        # Advance
        remaining_audio_time -= audio_dur_in_segment
        video_time_accum += stock_dur
        
        # Add middle clip duration if applicable
        if task and task.get("followed_by_clip", False) and middle_clip_idx < len(ctx.middle_clip_actual_durations):
            video_time_accum += ctx.middle_clip_actual_durations[middle_clip_idx]
            middle_clip_idx += 1
            
    # If beyond all segments, map linearly (or clamp)
    return video_time_accum + remaining_audio_time
