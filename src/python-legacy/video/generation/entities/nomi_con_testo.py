
import os
import textwrap
from typing import Dict, Any
from moviepy import vfx, TextClip # Updated to vfx import style if possible, or use explicit imports

from .base import BaseEntityHandler
# Note: MoviePy v2 TextClip might need font path handling
from ..common.context import GenerationContext

class NomiConTestoHandler(BaseEntityHandler):
    def process(self, segment: Dict[str, Any], segment_idx: int) -> bool:
        ctx = self.context
        
        text = str(segment.get("text_value") or "").strip()
        start = float(segment.get("start_v") or 0.0)
        dur = float(segment.get("dur_v") or 0.0) # Assume corrected/calculated before passed or recalculated here
        
        # Nomi_Con_Testo logic caps duration based on background clip?
        # In original: max_duration = background_clip_for_moviepy_overlays.duration - start_v_text
        # We need access to background_clip_for_moviepy_overlays
        
        bg_clip = ctx.background_clip_for_moviepy_overlays
        max_duration = 3600.0 # Default fallback
        if bg_clip:
             max_duration = bg_clip.duration - start
        
        final_duration = min(dur, max_duration)
        if final_duration < 0.1: return False
        
        ctx.status_callback(f"{ctx.main_label} [NomiConTesto] Processing '{text}'...", False)
        
        try:
             font_size = int(ctx.config_settings.get("nomi_font_size", 40))
             wrapped = "\n".join(textwrap.wrap(text, width=30))
             
             # Note: TextClip API in MoviePy 1.x vs 2.x
             # Assuming 1.x or compatible wrapper. If 2.0, might need font argument differently.
             # Original code uses: TextClip(..., method="caption", ...)
             
             txt_clip = TextClip(
                 txt=wrapped,
                 fontsize=font_size,
                 color=ctx.config_settings.get("text_color", "white"),
                 method="caption",
                 font=ctx.font_path_default,
                 size=(int(ctx.base_w * 0.6), None)
             ).with_duration(final_duration).with_fps(ctx.base_fps)
             
             txt_clip = txt_clip.with_position(("center", "center")).with_start(start).with_effects([vfx.FadeIn(0.3), vfx.FadeOut(0.3)])
             
             # In original, this appends to overlay_clips_moviepy (list of clips to composite)
             # NOT rendered to generic file usually?
             # Line 4206: overlay_clips_moviepy.append(txt_clip_generic)
             # So this Handler returns an Object, not a File Path?
             # But our BaseHandler contract implies we usually return True/False and maybe add to context lists.
             
             # We should add a list to context: overlay_objects_moviepy
             # I need to update Context again? Or use a generic list.
             # I can add it to context.
             
             if not hasattr(ctx, "overlay_objects_moviepy"):
                 ctx.overlay_objects_moviepy = [] # Dynamic attachment or I update Context class
             
             ctx.overlay_objects_moviepy.append(txt_clip)
             
             ctx.status_callback(f"{ctx.main_label} [NomiConTesto] Added clip object.", False)
             return True
             
        except Exception as e:
             import traceback
             ctx.status_callback(f"{ctx.main_label} [NomiConTesto] Error: {e}\n{traceback.format_exc()}", True)
             return False
