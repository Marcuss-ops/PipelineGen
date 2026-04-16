"""
MODULE: Orchestrator
DESCRIPTION:
This module acts as the central brain of the video generation process. It is responsible for 
instantiating the `GenerationContext` and executing the generation Phases in a strict sequential order.

RESPONSIBILITIES:
- Initialize the `GenerationContext` with all configuration and paths.
- Instantiate and run each Phase (Initialization, Audio, Intros, Segments, Assembly, Overlays, Finalization).
- Handle global errors and update the status callback.
- Ensure the final video path is returned or an error is raised.

INTERACTIONS:
- Instantiates: `GenerationContext` (holds state).
- Executes: `InitializationPhase`, `AudioPhase`, `IntrosPhase`, `SegmentsPhase`, `AssemblyPhase`, `OverlayPhase`, `FinalizationPhase`.
- Called by: `Generatevideoparallelized.py` (wrapper entry point).
"""
import os
import traceback
import hashlib
from typing import Dict, Any, Callable, List, Optional

from .common.context import GenerationContext
from .phases.initialization import InitializationPhase
from .phases.audio import AudioPhase
from .phases.intros import IntrosPhase
from .phases.segments import SegmentsPhase
from .phases.assembly import AssemblyPhase
from .phases.overlays import OverlayPhase
from .phases.finalization import FinalizationPhase

class VideoGenerationOrchestrator:
    def __init__(self, 
                 audio_path: str,
                 output_path: str,
                 config_settings: Dict[str, Any],
                 base_w: int, 
                 base_h: int, 
                 base_fps: float,
                 status_callback: Callable[[str, bool], None], 
                 temp_dir: str,
                 # Extra args
                 start_clips_paths: List[str] = [],
                 middle_clips_paths: List[str] = [],
                 stock_clips_sources: List[str] = [],
                 end_clips_paths: List[str] = [],
                 background_video_for_img_overlays_path: Optional[str] = None,
                 associazioni_finali_con_timestamp: Optional[Dict] = None,
                 formatted_img_entities: Optional[Dict] = None,
                 audio_language_for_srt: Optional[str] = None,
                 segments_for_srt_generation: Optional[List[Dict]] = None,
                 font_path_default: str = "Arial"
                 ):
                 
        self.ctx = GenerationContext(
            audio_path=audio_path,
            output_path=output_path,
            temp_dir=temp_dir,
            base_w=base_w,
            base_h=base_h,
            base_fps=base_fps,
            status_callback=status_callback,
            config_settings=config_settings,
            font_path_default=font_path_default,
            start_clips_paths=start_clips_paths,
            middle_clips_paths=middle_clips_paths,
            stock_clips_sources=stock_clips_sources,
            end_clips_paths=end_clips_paths,
            background_video_for_img_overlays_path=background_video_for_img_overlays_path,
            associazioni_finali_con_timestamp=associazioni_finali_con_timestamp,
            formatted_img_entities=formatted_img_entities,
            audio_language_for_srt=audio_language_for_srt,
            segments_for_srt_generation=segments_for_srt_generation,
            # Init from config/env
            overlay_engine=str(config_settings.get("overlay_engine") or os.environ.get("VELOX_OVERLAY_ENGINE", "remotion")).strip().lower(),
            video_style=config_settings.get("video_style", "young").lower()
        )

    def run(self) -> str:
        ctx = self.ctx
        try:
            # Phase 1: Init
            InitializationPhase(ctx).run()
            
            # Phase 2: Audio
            ctx.audio_duration = AudioPhase(ctx).get_audio_duration()
            
            # Phase 3: Intros
            IntrosPhase(ctx).run()
            
            # Phase 4: Segments (Downloads + Generation)
            SegmentsPhase(ctx).run()
            
            # Phase 5: Assembly (Base Video)
            base_video_path = AssemblyPhase(ctx).run()
            ctx.base_video_path = base_video_path

            # Phase 6: Overlays (Entities)
            # This populates ctx.all_rendered_overlay_files_for_ffmpeg_merge
            OverlayPhase(ctx).run()

            # Set clearer output name: voiceover + language + hash
            try:
                out_dir = os.path.dirname(ctx.output_path) or "."
                audio_base = (ctx.config_settings.get("voiceover_name") or os.path.splitext(os.path.basename(ctx.audio_path))[0] if ctx.audio_path else "voiceover")
                lang = (ctx.audio_language_for_srt or ctx.config_settings.get("audio_language") or "und").strip() or "und"
                try:
                    st = os.stat(ctx.audio_path)
                    hash_payload = f"{ctx.audio_path}|{st.st_size}|{int(st.st_mtime)}"
                except Exception:
                    hash_payload = f"{ctx.audio_path}|{lang}"
                short_hash = hashlib.md5(hash_payload.encode()).hexdigest()[:8]
                safe_base = "".join(c if c.isalnum() or c in ("-", "_") else "_" for c in audio_base).strip("_")
                safe_lang = "".join(c if c.isalnum() or c in ("-", "_") else "_" for c in lang).strip("_")
                ctx.output_path = os.path.join(out_dir, f"{safe_base}_{safe_lang}_{short_hash}.mp4")
                ctx.status_callback(f"{ctx.main_label} [Orchestrator] Output rename: {os.path.basename(ctx.output_path)}", False)
            except Exception as e:
                ctx.status_callback(f"{ctx.main_label} [Orchestrator] Output rename failed: {e}", True)

            # Phase 7: Finalization (Merge)
            final_path = FinalizationPhase(ctx).run(base_video_path)
            
            return final_path
            
        except Exception as e:
            ctx.status_callback(f"{ctx.main_label} [Orchestrator] CRITICAL ERROR: {e}\n{traceback.format_exc()}", True)
            raise
