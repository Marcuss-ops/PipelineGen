"""
MODULE: Audio Phase
DESCRIPTION:
This phase is responsible for analyzing and preparing the main audio track for the video.

RESPONSIBILITIES:
- Retrieve the duration of the main audio file.
- Validate audio integrity (via `get_audio_duration`).
- Store audio duration in the Context for subsequent timeline calculations.

INTERACTIONS:
- Input: `GenerationContext` (reads `audio_path`).
- Output: Updates `ctx.audio_duration`.
- Dependencies: `ffmpeg_utils` or MoviePy for duration checks.
"""
import os
from moviepy import AudioFileClip
from ..common.context import GenerationContext

class AudioPhase:
    def __init__(self, context: GenerationContext):
        self.ctx = context

    def get_audio_duration(self) -> float:
        ctx = self.ctx
        try:
            with AudioFileClip(ctx.audio_path) as audio:
                duration = getattr(audio, "duration", 0.0)
                duration = float(duration) if duration is not None else 0.0
                ctx.status_callback(f"{ctx.main_label} [AudioPhase] Audio duration: {duration:.2f}s", False)
                return duration
        except Exception as e:
            ctx.status_callback(f"{ctx.main_label} [AudioPhase] Error reading audio: {e}", True)
            raise

    # Note: Background music addition happens in Assembly phase usually.
