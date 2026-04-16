
"""
MODULE: Entity: Numeri
DESCRIPTION:
Handler for the "Numeri" entity. Displays large, animated numbers on screen.

RESPONSIBILITIES:
- Render dynamic number overlays.
- Uses `video_overlays.generate_number_overlay` (Remotion) or MoviePy fallback.
- Applies specific styling for "Top 10" style videos (big fonts, center alignment).

INTERACTIONS:
- Input: Entity segment data.
- Output: Generated video file (overlay).
"""
import os
import uuid
import re
from typing import Dict, Any, Optional
from moviepy import vfx, ImageClip

from .base import BaseEntityHandler
from ..common.helpers import get_stock_clip_for_overlay
from ...video_overlays import generate_numeri_overlay
from ...video_clips import apply_gaussian_blur

class NumeriHandler(BaseEntityHandler):
    def process(self, segment: Dict[str, Any], segment_idx: int) -> bool:
        ctx = self.context
        
        text = str(segment.get("text_value") or "").strip()
        start = float(segment.get("start_v") or 0.0)
        dur = float(segment.get("dur_v") or 0.0)
        
        # Check if contains digits
        if not any(c.isdigit() for c in text):
             ctx.status_callback(f"{ctx.main_label} [Numeri] Skip '{text}' (no digits)", False)
             return False

        ctx.status_callback(f"{ctx.main_label} [Numeri] 🔢 Processing '{text}' (start: {start:.2f}s)...", False)

        stock_bg_clip = get_stock_clip_for_overlay(ctx, start, dur)
        if stock_bg_clip is None:
            ctx.status_callback(f"{ctx.main_label} [Numeri] ❌ Error: No stock clip available, skip", True)
            return False

        effective_duration = min(dur + 1.5, stock_bg_clip.duration)

        # Calculate dynamics (logic from original)
        num_digits = len([c for c in text if c.isdigit()])
        animation_duration = max((num_digits * 0.15) + 1.5, 2.0)
        static_hold = 2.0
        zoom_out = 0.8
        ideal_duration = max(animation_duration + static_hold + zoom_out, 4.8)
        
        # max possible duration (simplified, assuming we don't have global limit overlap access easily yet)
        # In original: max_possible_duration = video_duration_for_overlays_limit - start
        # We'll use a safe large number for now or pass limit in context. 
        # For now assuming ample time.
        final_duration = min(ideal_duration, effective_duration)
        final_duration = max(final_duration, 4.8)

        # Background processing
        bg_temp_path = None
        try:
            bg_frame = stock_bg_clip.subclipped(0, min(final_duration, stock_bg_clip.duration))
            if bg_frame.duration < final_duration:
                 frame_array = bg_frame.get_frame(0)
                 bg_frame = ImageClip(frame_array).with_duration(final_duration)
            else:
                 bg_frame = bg_frame.subclipped(0, final_duration)
            
            bg_frame = apply_gaussian_blur(bg_frame, sigma=5)
            
            bg_temp_path = os.path.join(ctx.temp_dir, f"bg_numeri_{ctx.text_overlay_count:03d}_{uuid.uuid4().hex[:6]}.mp4")
            bg_frame.write_videofile(
                bg_temp_path,
                codec="libx264",
                audio=False,
                fps=ctx.base_fps,
                bitrate="5000k",
                preset="slow",
                logger=None,
                threads=max(1, 4) # simplified
            )
            bg_frame.close()
            ctx.temp_files_to_clean_overlays.append(bg_temp_path)
            
        except Exception as e:
            ctx.status_callback(f"{ctx.main_label} [Numeri] ❌ Error bg processing: {e}", True)
            return False

        # Generate Overlay
        try:
            comp_numeri = generate_numeri_overlay(
                text=text,
                start_v=0, # relative to clip
                dur_v=final_duration,
                base_w=ctx.base_w,
                base_h=ctx.base_h,
                background_path=bg_temp_path,
                fps=ctx.base_fps,
                font_path=ctx.font_path_default,
                text_color="white", # Defaults mostly
                status_callback=lambda m, e=False: ctx.status_callback(f"{ctx.main_label} {m}", e)
            )
            
            if comp_numeri:
                 ctx.text_overlay_count += 1
                 out_path = os.path.join(ctx.temp_dir, f"numeri_{ctx.text_overlay_count:03d}_{uuid.uuid4().hex[:6]}.mp4")
                 comp_numeri.write_videofile(
                     out_path,
                     codec="libx264",
                     audio=False,
                     fps=ctx.base_fps,
                     logger=None,
                     threads=max(1, 4)
                 )
                 comp_numeri.close()
                 
                 if os.path.exists(out_path):
                     ctx.all_rendered_overlay_files_for_ffmpeg_merge.append((out_path, start, final_duration))
                     ctx.temp_files_to_clean_overlays.append(out_path)
                     ctx.status_callback(f"{ctx.main_label} [Numeri] ✅ Created: {os.path.basename(out_path)}", False)
                     return True
            
            return False
            
        except Exception as e:
             import traceback
             ctx.status_callback(f"{ctx.main_label} [Numeri] ❌ Error generation: {e}\n{traceback.format_exc()}", True)
             return False
        finally:
             if bg_temp_path and os.path.exists(bg_temp_path):
                 # cleanup handled by context lists generally, but good to ensure closed clips
                 pass
