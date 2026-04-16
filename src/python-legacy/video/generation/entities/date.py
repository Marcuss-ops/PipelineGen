
"""
MODULE: Entity: Date
DESCRIPTION:
Handler for "Date" entities. Displays dates on screen.

RESPONSIBILITIES:
- Check if text is a valid date key.
- Render date overlays using `video_overlays.generate_date_overlay`.
- Handles collision detection with EntityStacks.

INTERACTIONS:
- Input: Date string.
- Output: Overlay video file.
"""
import os
import uuid
import re
from typing import Dict, Any, Optional
from moviepy import vfx

from .base import BaseEntityHandler
from ..common.helpers import normalize_date_key, text_contains_date, overlaps_entity_stack
from modules.video.video_overlays import generate_date_overlay, create_text_overlay_clip

class DateHandler(BaseEntityHandler):
    def process(self, segment: Dict[str, Any], segment_idx: int) -> bool:
        ctx = self.context
        
        raw_text = str(segment.get("text_value") or "").strip()
        start = float(segment.get("start_v") or 0.0)
        dur = float(segment.get("dur_v") or 0.0)
        
        # Check if actually a date
        txt_date_key = normalize_date_key(raw_text)
        if not txt_date_key:
            return False
            
        data_date = ctx.associazioni_finali_con_timestamp.get("Date", {})
        # Note: In the refactored loop we might be iterating over pre-flattened segments.
        # If segment comes from the flattened list, we just process it.
        
        # Check overlaps
        if ctx.entity_stack_segments and overlaps_entity_stack(start, start + dur, ctx.entity_stack_segments):
             ctx.status_callback(f"{ctx.main_label} [Date] ⏭️ Skip '{raw_text}' (overlap with EntityStack)", False)
             return False

        # Config
        font_size_date = int(ctx.config_settings.get("date_font_size", 40))
        text_color_date = "white"

        # Background (User provided only)
        bg_path_date = ctx.background_video_for_img_overlays_path
        
        ctx.status_callback(f"{ctx.main_label} [Date] 📅 Processing '{raw_text}'...", False)
        
        try:
             # Logic matching line 2736+
             # If using Remotion for dates? currently Date logic checks overlay_engine but mostly uses proper generation func
             # Actually Date overlays seem to use create_text_overlay_clip (MoviePy) usually.
             
             # Call generate/create function
             # We need to reuse info_overlay_dates? No, that's a dict.
             # Logic calls create_text_overlay_clip directly.
             
            result = create_text_overlay_clip(
                text=raw_text,
                start_v=0, # relative
                dur_v=dur,
                base_w=ctx.base_w,
                base_h=ctx.base_h,
                font_path=ctx.font_path_default,
                font_size=font_size_date,
                color=text_color_date,
                background_path=bg_path_date, # Only user BG
                fps=ctx.base_fps,
                status_callback=lambda m, e=False: ctx.status_callback(f"{ctx.main_label} {m}", e)
            )
            
            if result:
                 out_path, _, _ = result # returns (path, s, e)
                 
                 if out_path:
                     ctx.all_rendered_overlay_files_for_ffmpeg_merge.append((out_path, start, dur))
                     ctx.temp_files_to_clean_overlays.append(out_path)
                     ctx.status_callback(f"{ctx.main_label} [Date] ✅ Created: {os.path.basename(out_path)}", False)
                     return True
            
            return False

        except Exception as e:
            import traceback
            ctx.status_callback(f"{ctx.main_label} [Date] ❌ Error: {e}\n{traceback.format_exc()}", True)
            return False
