"""
MODULE: Overlays Phase - REMOTION ONLY
DESCRIPTION:
This phase generates all entity overlays using Remotion EntityStack.
All MoviePy-based entity handlers are REMOVED - only Remotion is used.

RESPONSIBILITIES:
- Use entity_stack_grouper to prepare entity groups for Remotion.
- Call render_entity_stack for each group.
- Collect all generated overlay files into ctx.all_rendered_overlay_files_for_ffmpeg_merge.
- Process subtitles separately (SRT-based).

INTERACTIONS:
- Input: GenerationContext (entity data, parsed from config).
- Output: Populates ctx.all_rendered_overlay_files_for_ffmpeg_merge.
- Dependencies: entity_stack_grouper, remotion_renderer.
"""
import os
import uuid
import logging
from typing import Dict, Any, List, Optional

from ..common.context import GenerationContext
from ..common.helpers import map_audio_to_video
from ..entities.subtitles import SubtitleHandler

logger = logging.getLogger(__name__)


class OverlayPhase:
    def __init__(self, context: GenerationContext):
        self.ctx = context
        self.subtitle_handler = SubtitleHandler(context)

    def run(self):
        ctx = self.ctx
        ctx.status_callback(f"{ctx.main_label} {'='*20} [OverlayPhase] START (REMOTION MODE) {'='*20}", False)
        ctx.status_callback(f"{ctx.main_label} [OverlayPhase] Using Remotion EntityStack for ALL entities...", False)
        
        # Check if we should skip Remotion
        if ctx.overlay_engine == "python":
            ctx.status_callback(f"{ctx.main_label} [OverlayPhase] ⚠️ overlay_engine=python, skipping Remotion overlays", True)
        else:
            # 1. Render all entities with Remotion EntityStack
            self._render_entity_stack_overlays()
        
        # 2. Subtitles (SRT-based, not Remotion)
        self.subtitle_handler.process_all()
        
        ctx.status_callback(f"{ctx.main_label} [OverlayPhase] Finished.", False)
        ctx.status_callback(f"{ctx.main_label} {'='*20} [OverlayPhase] END {'='*20}", False)

    def _render_entity_stack_overlays(self):
        """Render all entities using Remotion EntityStack."""
        ctx = self.ctx
        
        # Check if we have entities to render
        if not ctx.associazioni_finali_con_timestamp:
            ctx.status_callback(f"{ctx.main_label} [EntityStack] No entities to render", False)
            return
        
        # Import Remotion modules
        try:
            from modules.video.entity_stack_grouper import prepare_entity_stack_groups
            from modules.video.remotion_renderer import (
                render_entity_stack,
                render_entity_stack_history_snapshots,
                render_entity_stack_per_entity_chain,
                find_remotion_project,
            )
            from modules.video.video_ffmpeg import trim_video_reencode
        except ImportError as e:
            ctx.status_callback(f"{ctx.main_label} [EntityStack] ❌ Import failed: {e}", True)
            return
        
        # Find Remotion project
        remotion_project_path = find_remotion_project()
        if not remotion_project_path:
            ctx.status_callback(f"{ctx.main_label} [EntityStack] ❌ Remotion project not found", True)
            return
        
        ctx.status_callback(f"{ctx.main_label} [EntityStack] 📁 Remotion project: {remotion_project_path}", False)
        
        # Prepare entity groups
        try:
            video_duration_limit = getattr(ctx, 'video_duration_for_overlays_limit', ctx.audio_duration)
            # Default behavior:
            # - use user-provided background if present
            # - do NOT use stock/base segment as EntityStack background
            # If no user background is provided, Remotion fallback is used (dark striped background).
            allow_user_bg = bool(ctx.config_settings.get("entity_stack_allow_user_background", True))
            stack_background_path = ctx.background_video_for_img_overlays_path if allow_user_bg else None
            
            stack_groups = prepare_entity_stack_groups(
                associazioni_finali_con_timestamp=ctx.associazioni_finali_con_timestamp,
                map_audio_to_video=lambda t: map_audio_to_video(ctx, t),
                video_duration_limit=video_duration_limit,
                remotion_style_manager=None,  # Use default styles
                background_path=stack_background_path,
                fps=int(ctx.base_fps),
                equalize_durations=False,
                hold_seconds=1.0,
                min_entity_seconds=2.0,
                stack_layer_style=ctx.config_settings.get("stack_layer_style", "DEFAULT"),
            )
            
            ctx.status_callback(f"{ctx.main_label} [EntityStack] 📦 Prepared {len(stack_groups)} entity groups", False)
            
        except Exception as e:
            ctx.status_callback(f"{ctx.main_label} [EntityStack] ❌ prepare_entity_stack_groups failed: {e}", True)
            import traceback
            logger.error(traceback.format_exc())
            return
        
        if not stack_groups:
            ctx.status_callback(f"{ctx.main_label} [EntityStack] ⚠️ No entity groups to render", False)
            return
        
        # Store entity stack segments in context for overlap checking
        ctx.entity_stack_segments = []

        # Probe base duration once (used for background segment clamps)
        base_video_duration = None
        try:
            if getattr(ctx, "base_video_path", None) and os.path.exists(ctx.base_video_path):
                import subprocess
                probe_cmd = [
                    "ffprobe", "-v", "error",
                    "-show_entries", "format=duration",
                    "-of", "default=noprint_wrappers=1:nokey=1",
                    ctx.base_video_path,
                ]
                res = subprocess.run(probe_cmd, capture_output=True, text=True, timeout=10)
                if res.returncode == 0 and res.stdout.strip():
                    base_video_duration = float(res.stdout.strip())
        except Exception:
            base_video_duration = None
        
        # Render each entity group
        rendered_count = 0
        for group_idx, group in enumerate(stack_groups):
            if not group or 'entities' not in group:
                continue
            
            entities = group.get('entities', [])
            start_v = group.get('start_v', 0.0)
            total_duration = group.get('total_duration', 0.0)
            background_path = group.get('background_path')
            # Optional: use segment of base video as EntityStack background.
            # Default is False so Remotion fallback (dark striped background) is used.
            use_base_segment_bg = bool(ctx.config_settings.get("entity_stack_use_base_segment_background", False))
            if use_base_segment_bg:
                if getattr(ctx, "base_video_path", None) and os.path.exists(ctx.base_video_path):
                    # Clamp duration to base video to avoid empty/invalid segments
                    start_v_clamped = max(0.0, float(start_v))
                    dur_v = float(total_duration or 0.0)
                    if base_video_duration is not None:
                        if start_v_clamped >= base_video_duration - 0.05:
                            dur_v = 0.0
                        else:
                            dur_v = min(dur_v, max(0.0, base_video_duration - start_v_clamped))
                    if dur_v < 0.1:
                        background_path = None
                    else:
                        total_duration = dur_v
                    segment_path = os.path.join(
                        ctx.temp_dir,
                        f"entitystack_bg_segment_{group_idx:03d}_{uuid.uuid4().hex[:6]}.mp4",
                    )
                    _status = lambda m, err=False: ctx.status_callback(f"{ctx.main_label} {m}", err)
                    if trim_video_reencode(
                        ctx.base_video_path,
                        segment_path,
                        start_time=start_v_clamped,
                        duration=dur_v,
                        base_w=ctx.base_w,
                        base_h=ctx.base_h,
                        base_fps=ctx.base_fps,
                        status_callback=lambda msg: _status(msg, False),
                    ):
                        try:
                            from modules.video.video_ffmpeg import has_video_stream
                        except Exception:
                            try:
                                from ....video_ffmpeg import has_video_stream
                            except Exception:
                                has_video_stream = None
                        if callable(has_video_stream) and not has_video_stream(segment_path):
                            background_path = None
                            ctx.status_callback(
                                f"{ctx.main_label} [EntityStack] ⚠️ Segmento background senza stream video, uso fallback griglia",
                                True,
                            )
                        else:
                            background_path = segment_path
                            ctx.temp_files_to_clean_overlays.append(segment_path)
                            ctx.status_callback(
                                f"{ctx.main_label} [EntityStack] 🎞️ Sfondo = segmento base video {start_v_clamped:.1f}s–{start_v_clamped + dur_v:.1f}s",
                                False,
                            )
                    else:
                        background_path = None
                else:
                    background_path = None

            if not entities:
                continue
            
            # Log group info
            entity_types = [e.get('type', 'UNKNOWN') for e in entities]
            ctx.status_callback(
                f"{ctx.main_label} [EntityStack] Rendering group {group_idx + 1}/{len(stack_groups)}: "
                f"{len(entities)} entities ({', '.join(set(entity_types))}) @ {start_v:.2f}s", 
                False
            )
            
            # Track segment for overlap checking
            ctx.entity_stack_segments.append({
                'start': start_v,
                'end': start_v + total_duration
            })
            
            try:
                # Generate output path
                out_path = os.path.join(
                    ctx.temp_dir, 
                    f"entitystack_{group_idx:03d}_{uuid.uuid4().hex[:6]}.mp4"
                )
                
                # Get stack layer style from config
                stack_layer_style = ctx.config_settings.get("stack_layer_style", "DEFAULT")
                light_mode = bool(ctx.config_settings.get("entity_stack_light_mode", False))
                _status = lambda m, err=False: ctx.status_callback(f"{ctx.main_label} {m}", err)

                # Strategia veloce: 1 clip per entità (sfondo = snapshot delle precedenti), poi concat.
                # Molto più veloce del render unico con N entità.
                if len(entities) >= 2:
                    ctx.status_callback(
                        f"{ctx.main_label} [EntityStack] ⚡ Render per entità (1 clip + snapshot per entità, poi concat)...",
                        False,
                    )
                    try:
                        clip = render_entity_stack_per_entity_chain(
                            entities=entities,
                            output_path=out_path,
                            background_path=background_path,
                            fps=int(ctx.base_fps),
                            width=ctx.base_w,
                            height=ctx.base_h,
                            remotion_project_path=remotion_project_path,
                            status_callback=_status,
                            stack_layer_style=stack_layer_style,
                            light_mode=light_mode,
                        )
                    except Exception as e:
                        logger.warning("[EntityStack] Per-entity chain fallback a render unico: %s", e)
                        if _status:
                            _status(f"[EntityStack] ⚠️ Chain fallback: {e}", True)
                        clip = render_entity_stack(
                            entities=entities,
                            remotion_project_path=remotion_project_path,
                            output_path=out_path,
                            fps=int(ctx.base_fps),
                            width=ctx.base_w,
                            height=ctx.base_h,
                            background_path=background_path,
                            stack_layer_style=stack_layer_style,
                            status_callback=_status,
                            history_snapshots=None,
                        )
                else:
                    clip = render_entity_stack(
                        entities=entities,
                        remotion_project_path=remotion_project_path,
                        output_path=out_path,
                        fps=int(ctx.base_fps),
                        width=ctx.base_w,
                        height=ctx.base_h,
                        background_path=background_path,
                        stack_layer_style=stack_layer_style,
                        status_callback=_status,
                        history_snapshots=None,
                    )
                
                # Close clip if moviepy object returned
                if clip and hasattr(clip, 'close'):
                    try:
                        clip.close()
                    except Exception:
                        pass
                
                # Verify output
                if os.path.exists(out_path) and os.path.getsize(out_path) > 1000:
                    ctx.all_rendered_overlay_files_for_ffmpeg_merge.append(
                        (out_path, start_v, total_duration)
                    )
                    ctx.temp_files_to_clean_overlays.append(out_path)
                    rendered_count += 1
                    ctx.status_callback(
                        f"{ctx.main_label} [EntityStack] ✅ Rendered group {group_idx + 1}: {os.path.basename(out_path)}",
                        False
                    )
                else:
                    ctx.status_callback(
                        f"{ctx.main_label} [EntityStack] ⚠️ Empty/missing output for group {group_idx + 1}",
                        True
                    )
                    
            except Exception as e:
                ctx.status_callback(f"{ctx.main_label} [EntityStack] ❌ Render failed for group {group_idx + 1}: {e}", True)
                import traceback
                logger.error(traceback.format_exc())
                continue
        
        ctx.status_callback(
            f"{ctx.main_label} [EntityStack] 🎬 Rendered {rendered_count}/{len(stack_groups)} entity groups",
            False
        )
