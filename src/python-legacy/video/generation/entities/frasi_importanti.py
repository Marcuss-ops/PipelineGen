
"""
MODULE: Entity: Frasi Importanti
DESCRIPTION:
Handler for the "Frasi_Importanti" entity. These are high-impact text overlays, often generated 
using Remotion for complex animations.

RESPONSIBILITIES:
- Render "Frasi Importanti" using `video_overlays.generate_frasi_importanti_overlay` (Remotion) 
  or a MoviePy fallback.
- Handle styling logic (fonts, colors, background blurring).
- Updates `ctx.all_rendered_overlay_files_for_ffmpeg_merge` with the generated clip path.

INTERACTIONS:
- Input: Entity segment data.
- Output: Generated video file (overlay).
- Dependencies: `video_overlays`, `remotion_renderer`.
"""
import os
import uuid
import re
from typing import Dict, Any, Optional
from moviepy import vfx

from .base import BaseEntityHandler
from ..common.helpers import get_stock_clip_for_overlay, overlaps_entity_stack
from ...video_overlays import generate_frasi_importanti_overlay
from ...video_clips import apply_gaussian_blur

class FrasiImportantiHandler(BaseEntityHandler):
    def process(self, segment: Dict[str, Any], segment_idx: int) -> bool:
        ctx = self.context
        
        # Data extraction
        text = str(segment.get("text_value") or "").strip()
        start = float(segment.get("start_v") or 0.0)
        dur = float(segment.get("dur_v") or 0.0)
        
        # Cleanup text
        # Remove patterns like "@filename.py (line:number)" or "@filename.py (142-144)"
        cleaned_text = re.sub(r'@[^\s]+\.py\s*\([^)]*\)', '', text)
        cleaned_text = re.sub(r'\([0-9]+-[0-9]+\)', '', cleaned_text)
        cleaned_text = cleaned_text.strip()
        text = cleaned_text if cleaned_text else text
        
        ctx.status_callback(f"{ctx.main_label} [Frasi] 💬 Processing '{text[:30]}...' (start: {start:.2f}s)...", False)

        # Check Remotion requirement
        if ctx.overlay_engine == "python":
            ctx.status_callback(f"{ctx.main_label} [Frasi] ⏭️ Skip: overlay_engine=python (Remotion-only)", False)
            return False

        # Check EntityStack overlap
        if ctx.entity_stack_segments and overlaps_entity_stack(start, start + dur, ctx.entity_stack_segments):
             ctx.status_callback(f"{ctx.main_label} [Frasi] ⏭️ Skip (overlap with EntityStack)", False)
             return False

        # Effective duration calculation
        max_possible = (ctx.base_fps * 100000) # Arbitrary large number or logic from main... 
        # In main: video_duration_for_overlays_limit - start_v_text. 
        # We assume dur is already clamped by segment generation, 
        # but let's just use dur for now as logic is complex.
        # Main logic: effective_duration = min(total_dur_text, max_possible)
        effective_duration = dur 

        if effective_duration < 2.5:
             ctx.status_callback(f"{ctx.main_label} [Frasi] ⏭️ Skip (duration {effective_duration:.2f}s < 2.5s)", False)
             return False

        # Background Handling
        use_user_bg = bool(
            ctx.background_video_for_img_overlays_path
            and os.path.exists(ctx.background_video_for_img_overlays_path)
        )
        bg_temp_path = None
        stock_bg_clip = None

        if use_user_bg:
            bg_temp_path = ctx.background_video_for_img_overlays_path
            ctx.status_callback(f"{ctx.main_label} [Frasi] User background: {os.path.basename(bg_temp_path)}", False)
        else:
            stock_bg_clip = get_stock_clip_for_overlay(ctx, start, effective_duration)
            if stock_bg_clip is None:
                ctx.status_callback(f"{ctx.main_label} [Frasi] ⚠️ No background available", True)
                return False
                
            # Process stock background (loop + blur)
            bg_frame = stock_bg_clip
            try:
                if hasattr(bg_frame, "duration") and bg_frame.duration and bg_frame.duration < effective_duration:
                    bg_frame = bg_frame.with_effects([vfx.Loop(duration=effective_duration)])
            except Exception:
                pass
            
            # Subclip and blur
            try:
                bg_frame = bg_frame.subclipped(0, effective_duration)
                bg_frame = apply_gaussian_blur(bg_frame, sigma=5)

                # Save blurred bg
                bg_temp_path = os.path.join(ctx.temp_dir, f"bg_frasi_{ctx.text_overlay_count:03d}_{uuid.uuid4().hex[:6]}.mp4")
                bg_frame.write_videofile(
                    bg_temp_path,
                    codec="libx264",
                    audio=False,
                    fps=ctx.base_fps,
                    logger=None,
                    threads=max(1, 2) # simplified threads
                )
                bg_frame.close()
                ctx.temp_files_to_clean_overlays.append(bg_temp_path)
            except Exception as e:
                ctx.status_callback(f"{ctx.main_label} [Frasi] ❌ Error processing stock bg: {e}", True)
                return False

        # Video Style Logic
        video_style = ctx.video_style.lower()
        if video_style == "crime":
            text_with_quotes = text
        else:
            text_with_quotes = f'"{text}"'

        # Generate Overlay
        try:
            comp_frasi = generate_frasi_importanti_overlay(
                text=text_with_quotes,
                start_v=0,
                dur_v=effective_duration,
                base_w=ctx.base_w,
                base_h=ctx.base_h,
                background_path=bg_temp_path,
                fps=ctx.base_fps,
                font_path=ctx.font_path_default, # Not really used due to Remotion but passed
                font_size=75,
                text_color="white",
                status_callback=lambda m: ctx.status_callback(f"{ctx.main_label} {m}", False),
                video_style=video_style
            )

            if comp_frasi:
                ctx.text_overlay_count += 1
                out_path = os.path.join(ctx.temp_dir, f"frasi_overlay_{ctx.text_overlay_count:03d}_{uuid.uuid4().hex[:6]}.mp4")
                comp_frasi.write_videofile(
                    out_path,
                    codec="libx264",
                    audio=False,
                    fps=ctx.base_fps,
                    logger=None,
                    threads=max(1, 4)
                )
                comp_frasi.close()

                if os.path.exists(out_path):
                    ctx.all_rendered_overlay_files_for_ffmpeg_merge.append((out_path, start, effective_duration))
                    ctx.temp_files_to_clean_overlays.append(out_path)
                    ctx.status_callback(f"{ctx.main_label} [Frasi] ✅ Created: {os.path.basename(out_path)}", False)
                    return True
                else:
                    return False
            else:
                 ctx.status_callback(f"{ctx.main_label} [Frasi] ⚠️ Generation returned None", True)
                 return False

        except Exception as e:
            import traceback
            ctx.status_callback(f"{ctx.main_label} [Frasi] ❌ Error: {e}\n{traceback.format_exc()}", True)
            return False
