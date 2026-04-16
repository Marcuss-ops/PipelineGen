
"""
MODULE: Intros Phase
DESCRIPTION:
This phase processes the fixed video clips (Intro, Outro, and Middle clips) that structure the video.

RESPONSIBILITIES:
- Validate existence of start/end/middle clips.
- Process start clips with transcription, translation, and subtitle burn-in.
- Calculate the total duration of the "Start" sequence (Intros) to determine where the main content begins.
- Calculate and store the actual duration of each "Middle" clip for accurate timestamp mapping.
- Populate `ctx.total_start_duration` and `ctx.middle_clip_actual_durations`.

INTERACTIONS:
- Input: `GenerationContext` (paths for intros, middle clips).
- Output: Updates timeline variables in Context.
- Dependencies: `video_core.get_clip_duration`, `video_clips.process_single_middle_clip`.
"""
import os
import uuid
from moviepy import VideoFileClip
from ..common.context import GenerationContext

# Import subtitle processing function
try:
    from modules.video.video_clips import process_single_middle_clip
except ImportError:
    try:
        from ....video_clips import process_single_middle_clip
    except ImportError:
        process_single_middle_clip = None

class IntrosPhase:
    def __init__(self, context: GenerationContext):
        self.ctx = context

    def _process_start_clip_with_subtitles(self, clip_path: str, clip_index: int) -> tuple:
        """
        Process a start clip with transcription, translation, and subtitle burn-in.
        Returns (processed_path, duration) or (original_path, duration) if processing fails.
        """
        ctx = self.ctx
        label = f"{ctx.main_label} [IntrosPhase][StartClip#{clip_index+1}]"
        
        if process_single_middle_clip is None:
            ctx.status_callback(f"{label} ⚠️ Subtitle processing unavailable, using original clip", True)
            try:
                with VideoFileClip(clip_path) as clip:
                    return clip_path, clip.duration
            except Exception:
                return clip_path, 0.0
        
        ctx.status_callback(f"{label} 🎬 Processing start clip with subtitles: {os.path.basename(clip_path)}", False)
        
        try:
            # Create a dedicated temp directory for this clip's processing
            clip_temp_dir = os.path.join(ctx.temp_dir, f"start_clip_{clip_index}_{uuid.uuid4().hex[:6]}")
            os.makedirs(clip_temp_dir, exist_ok=True)
            
            # Get target language from context
            target_language = ctx.audio_language_for_srt
            
            # Process the clip with subtitles (no sound effects for intros)
            processed_path = process_single_middle_clip(
                clip_path=clip_path,
                sound_effects_list=[],  # No sound effects for intro clips
                temp_dir=clip_temp_dir,
                clip_index=clip_index,
                target_language=target_language,
                base_w=ctx.base_w,
                base_h=ctx.base_h,
                base_fps=ctx.base_fps,
                status_callback=ctx.status_callback,
                raise_translation_errors=False,
                timeout_burnin=120.0  # 2 minute timeout for subtitle burn-in
            )
            
            if processed_path and os.path.exists(processed_path):
                # Get the duration of the processed clip
                with VideoFileClip(processed_path) as clip:
                    dur = float(getattr(clip, "duration", 0) or 0)
                ctx.status_callback(f"{label} ✅ Processed with subtitles: {os.path.basename(processed_path)} ({dur:.2f}s)", False)
                return processed_path, dur
            else:
                ctx.status_callback(f"{label} ⚠️ Processing failed, using original clip", True)
                with VideoFileClip(clip_path) as clip:
                    d = float(getattr(clip, "duration", 0) or 0)
                    return clip_path, d
                    
        except Exception as e:
            ctx.status_callback(f"{label} ❌ Error processing start clip: {e}", True)
            try:
                with VideoFileClip(clip_path) as clip:
                    d = float(getattr(clip, "duration", 0) or 0)
                    return clip_path, d
            except Exception:
                return clip_path, 0.0

    def run(self):
        ctx = self.ctx
        ctx.status_callback(f"{ctx.main_label} {'='*20} [IntrosPhase] START {'='*20}", False)
        ctx.status_callback(f"{ctx.main_label} [IntrosPhase] Processing Intro/Outro/Middle clips...", False)
        
        # 1. Start Clips - Now with subtitle processing
        ctx.total_start_duration = 0.0
        valid_start_clips = []
        
        for idx, p in enumerate(ctx.start_clips_paths):
            if p and os.path.exists(p):
                try:
                    # Process with subtitles
                    processed_path, dur = self._process_start_clip_with_subtitles(p, idx)
                    dur = float(dur) if dur is not None else 0.0
                    ctx.total_start_duration += dur
                    valid_start_clips.append(processed_path)
                    ctx.status_callback(f"{ctx.main_label} [IntrosPhase] Start clip processed: {os.path.basename(processed_path)} ({dur:.2f}s)", False)
                except Exception as e:
                    ctx.status_callback(f"{ctx.main_label} [IntrosPhase] Error processing start clip {p}: {e}", True)
                    # Fallback: use original clip
                    try:
                        with VideoFileClip(p) as clip:
                            dur = float(getattr(clip, "duration", 0) or 0)
                            ctx.total_start_duration += dur
                            valid_start_clips.append(p)
                    except Exception:
                        pass
            else:
                ctx.status_callback(f"{ctx.main_label} [IntrosPhase] Start clip not found: {p}", True)
        ctx.start_clips_paths = valid_start_clips

        # 2. Middle Clips (duration only, processing happens during segment phase)
        ctx.middle_clip_actual_durations = []
        valid_middle_clips = []
        for p in ctx.middle_clips_paths:
            if p and os.path.exists(p):
                try:
                    with VideoFileClip(p) as clip:
                        dur = clip.duration
                        ctx.middle_clip_actual_durations.append(dur)
                        valid_middle_clips.append(p)
                except Exception as e:
                     ctx.status_callback(f"{ctx.main_label} [IntrosPhase] Error reading middle clip {p}: {e}", True)
        ctx.middle_clips_paths = valid_middle_clips
        
        # 3. End Clips
        # Just validate they exist for later
        valid_end_clips = []
        for p in ctx.end_clips_paths:
             if p and os.path.exists(p):
                 valid_end_clips.append(p)
        ctx.end_clips_paths = valid_end_clips

        ctx.status_callback(f"{ctx.main_label} [IntrosPhase] Total start duration: {float(ctx.total_start_duration or 0):.2f}s", False)
        ctx.status_callback(f"{ctx.main_label} {'='*20} [IntrosPhase] END {'='*20}", False)
