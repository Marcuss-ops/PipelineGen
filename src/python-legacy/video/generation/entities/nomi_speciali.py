
import os
import uuid
from typing import Dict, Any
from moviepy import vfx

from .base import BaseEntityHandler
from ..common.helpers import overlaps_entity_stack
from ...video_overlays import generate_nomi_speciali_overlay

class NomiSpecialiHandler(BaseEntityHandler):
    def process(self, segment: Dict[str, Any], segment_idx: int) -> bool:
        ctx = self.context
        
        text = str(segment.get("text_value") or "").strip()
        start = float(segment.get("start_v") or 0.0)
        # Note: We need max_possible logic from context if available, otherwise rely on caller or dur_v
        # In main logic: max_possible = video_duration_limit - start
        # Here we assume segment['dur_v'] is already clamped or we blindly use it.
        # But NomiSpeciali RECALCULATES duration.
        
        dur_limit_rel = 100000.0 # TODO: pass video_duration_limit in context
        # ctx.video_duration_limit ?? 
        
        # Recalculate duration logic matches main
        num_letters = len([c for c in text if c.isalpha()])
        ideal_duration = (num_letters * 0.15) + 3.0
        final_duration = ideal_duration # simplified
        
        if ctx.entity_stack_segments and overlaps_entity_stack(start, start + final_duration, ctx.entity_stack_segments):
             ctx.status_callback(f"{ctx.main_label} [Nomi] ⏭️ Skip '{text}' (overlap)", False)
             return False

        bg_path = ctx.background_video_for_img_overlays_path
        video_style = ctx.video_style.lower()
        
        ctx.status_callback(f"{ctx.main_label} [Nomi] 🔤 Processing '{text}' (style: {video_style})...", False)

        try:
            comp = generate_nomi_speciali_overlay(
                txt=text,
                start_v=0,
                dur_v=final_duration,
                base_w=ctx.base_w,
                base_h=ctx.base_h,
                background_path=bg_path,
                fps=ctx.base_fps,
                font_path=ctx.font_path_default,
                base_font_size=35, # Hardcoded in main
                text_color="white",
                status_callback=ctx.status_callback,
                video_style=video_style
            )
            
            if comp:
                 # ctx.text_overlay_count += ? Main uses text_overlay_count shared?
                 out_path = os.path.join(ctx.temp_dir, f"nomi_{uuid.uuid4().hex[:6]}.mp4")
                 comp.write_videofile(
                     out_path, codec="libx264", audio=False, fps=ctx.base_fps, 
                     logger=None, threads=max(1, 2)
                 )
                 comp.close()
                 
                 if os.path.exists(out_path):
                     ctx.all_rendered_overlay_files_for_ffmpeg_merge.append((out_path, start, final_duration))
                     ctx.temp_files_to_clean_overlays.append(out_path)
                     ctx.status_callback(f"{ctx.main_label} [Nomi] ✅ Created: {os.path.basename(out_path)}", False)
                     return True
            
            return False

        except Exception as e:
            ctx.status_callback(f"{ctx.main_label} [Nomi] ❌ Error: {e}", True)
            return False
