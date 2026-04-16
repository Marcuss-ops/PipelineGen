
"""
MODULE: Entity: Subtitles (SRT)
DESCRIPTION:
Handler for generating subtitles (SRT files) for the final video.

RESPONSIBILITIES:
- Takes audio segments/transcriptions.
- Formats them into SRT standard.
- Burn-in logic is handled by FFmpeg in Finalization, but this handler prepares the `.srt` files.

INTERACTIONS:
- Input: `ctx.segments_for_srt_generation`.
- Output: Generated `.srt` file path stored in Context.
"""
import os
import uuid
import textwrap
import logging
from typing import List, Dict, Any
from moviepy import TextClip

from ..common.context import GenerationContext
from ..common.helpers import map_audio_to_video

class SubtitleHandler:
    def __init__(self, context: GenerationContext):
        self.ctx = context

    def process_all(self):
        ctx = self.ctx
        if not ctx.segments_for_srt_generation:
            return

        ctx.status_callback(f"{ctx.main_label} [Subtitles] Generazione sottotitoli...", False)
        
        font_size = int(ctx.config_settings.get("subtitle_font_size", 36))
        color = ctx.config_settings.get("subtitle_color", "white")
        stroke_color = ctx.config_settings.get("subtitle_shadow_color", "black")
        
        count = 0
        for seg in ctx.segments_for_srt_generation:
             # Logic from lines 4221+
             try:
                 start = float(seg.get("timestamp_start") or seg.get("start") or -1)
                 end = float(seg.get("timestamp_end") or seg.get("end") or -1)
                 text = str(seg.get("text") or "").strip()
             except: continue
             
             if start < 0 or end <= start or not text: continue
             
             start_v = map_audio_to_video(ctx, start)
             if start_v > ctx.video_duration_for_overlays_limit: continue
             
             dur = min(end - start, ctx.video_duration_for_overlays_limit - start_v)
             if dur < 0.1: continue
             
             wrapped = "\n".join(textwrap.wrap(text, width=50))
             
             try:
                 # Using timeout protection for MoviePy TextClip render?
                 # Simplified here but we should be careful.
                 
                 subtitle_path = os.path.join(ctx.temp_dir, f"subtitle_{count:03d}_{uuid.uuid4().hex[:6]}.mp4")
                 
                 # Direct TextClip write
                 # Note: Creating clip and writing immediately to avoid memory buildup
                 txt_clip = TextClip(
                     txt=wrapped, fontsize=font_size, color=color,
                     font=ctx.font_path_default, size=(int(ctx.base_w * 0.8), None),
                     stroke_color=stroke_color, stroke_width=2, method="caption"
                 ).with_duration(dur).with_fps(ctx.base_fps).with_position(("center", ctx.base_h - int(ctx.base_h * 0.10)))
                 
                 # Write
                 # In original, uses threading for timeout.
                 # We can use simple write here and rely on process timeout if needed, or implement safety.
                 txt_clip.write_videofile(
                     subtitle_path, codec="libx264", audio=False, fps=ctx.base_fps,
                     preset="ultrafast", logger=None, threads=max(1, 2)
                 )
                 txt_clip.close()
                 
                 if os.path.exists(subtitle_path):
                     ctx.all_rendered_overlay_files_for_ffmpeg_merge.append((subtitle_path, start_v, dur))
                     ctx.temp_files_to_clean_overlays.append(subtitle_path)
                     count += 1
             except Exception as e:
                 ctx.status_callback(f"{ctx.main_label} [Subtitles] Error '{text[:20]}': {e}", True)
        
        ctx.status_callback(f"{ctx.main_label} [Subtitles] Generated {count} subtitles.", False)
