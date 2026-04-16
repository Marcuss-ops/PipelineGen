
"""
MODULE: Finalization Phase
DESCRIPTION:
This is the final step of the pipeline. It merges the "Base Video" with all the generated "Overlay" clips.

RESPONSIBILITIES:
- Take the `base_video_path` from Assembly.
- Take the list of overlays from `ctx.all_rendered_overlay_files_for_ffmpeg_merge`.
- Execute the heavy FFmpeg merge operation (`merge_overlays_ffmpeg`).
- Clean up all temporary files (overlays, base video, segments).
- Return the path to the final, polished video file.

INTERACTIONS:
- Input: `GenerationContext`, `base_video_path`.
- Output: The final video file path.
- Dependencies: `video_ffmpeg.merge_overlays_ffmpeg`.
"""
import os
import shutil
import subprocess
from typing import List
from ..common.context import GenerationContext
try:
    from ....video_ffmpeg import merge_overlays_ffmpeg
except ImportError:
    from modules.video.video_ffmpeg import merge_overlays_ffmpeg

class FinalizationPhase:
    def __init__(self, context: GenerationContext):
        self.ctx = context

    def run(self, base_video_path: str) -> str:
        ctx = self.ctx
        ctx.status_callback(f"{ctx.main_label} [Finalization] Merging overlays...", False)

        # Ensure base video has audio (voiceover + background music). If missing, rebuild audio track.
        def _has_audio_stream(path: str) -> bool:
            try:
                probe_cmd = [
                    "ffprobe", "-v", "error", "-select_streams", "a:0",
                    "-show_entries", "stream=index", "-of", "csv=p=0", path
                ]
                res = subprocess.run(probe_cmd, capture_output=True, text=True, timeout=10)
                return res.returncode == 0 and bool(res.stdout.strip())
            except Exception:
                return False

        def _pick_music_path() -> str:
            music_list = ctx.config_settings.get("background_music_paths") or []
            if isinstance(music_list, str):
                music_list = [music_list] if music_list.strip() else []
            if not music_list:
                legacy = ctx.config_settings.get("background_music") or []
                if isinstance(legacy, str):
                    legacy = [legacy] if legacy.strip() else []
                if isinstance(legacy, (list, tuple)):
                    music_list = legacy
            if not isinstance(music_list, (list, tuple)):
                return ""
            for p in music_list:
                if p and os.path.exists(p):
                    return p
            return ""

        def _probe_duration(path: str) -> float:
            try:
                cmd = [
                    "ffprobe", "-v", "error",
                    "-show_entries", "format=duration",
                    "-of", "default=noprint_wrappers=1:nokey=1", path
                ]
                res = subprocess.run(cmd, capture_output=True, text=True, timeout=10)
                if res.returncode == 0 and res.stdout.strip():
                    return float(res.stdout.strip())
            except Exception:
                pass
            return 0.0

        def _compute_middle_clip_timestamps() -> List[tuple]:
            """Build absolute (start,end) timestamps for middle clips on final timeline."""
            middle_ts_local = []
            current_time = ctx.total_start_duration
            sorted_indices = sorted(ctx.stock_segment_results.keys())
            tasks = ctx.stock_tasks_args_list
            mid_idx_local = 0

            for idx in sorted_indices:
                _, dur = ctx.stock_segment_results[idx]
                current_time += dur
                task = next((t for t in tasks if t["segment_index"] == idx), None)
                if task and task.get("followed_by_clip", False):
                    if mid_idx_local < len(ctx.middle_clip_actual_durations):
                        mdur = ctx.middle_clip_actual_durations[mid_idx_local]
                        middle_ts_local.append((current_time, current_time + mdur))
                        current_time += mdur
                        mid_idx_local += 1
            return middle_ts_local

        def _compute_excluded_audio_ranges() -> List[tuple]:
            """
            Build ranges where master VO/BG should be muted:
            - initial clip window (0 -> total_start_duration)
            - middle clips
            """
            ranges = []
            try:
                start_dur = float(ctx.total_start_duration or 0.0)
                if start_dur > 0.01:
                    ranges.append((0.0, start_dur))
            except Exception:
                pass
            ranges.extend(_compute_middle_clip_timestamps())
            return ranges

        def _ffmpeg_gate_expr_for_timestamps(timestamps: List[tuple]) -> str:
            """
            Return FFmpeg expression: 1 outside excluded ranges, 0 during (for VO/music).
            """
            if not timestamps:
                return "1"
            parts = [f"between(t\\,{float(s):.6f}\\,{float(e):.6f})" for s, e in timestamps if float(e) > float(s)]
            if not parts:
                return "1"
            return f"if({'+'.join(parts)},0,1)"

        def _ffmpeg_base_gate_expr(timestamps: List[tuple]) -> str:
            """
            Return FFmpeg expression: 1 during excluded ranges (intro+middle), 0 outside (for base only).
            """
            if not timestamps:
                return "0"
            parts = [f"between(t\\,{float(s):.6f}\\,{float(e):.6f})" for s, e in timestamps if float(e) > float(s)]
            if not parts:
                return "0"
            return f"if({'+'.join(parts)},1,0)"

        def _ffmpeg_intro_only_base_gate_expr(start_dur: float) -> str:
            """
            Keep only intro audio from base track; mute base for middle/stock/end.
            This avoids leaking middle clip audio when middle files contain audio tracks.
            """
            try:
                s = float(start_dur or 0.0)
            except Exception:
                s = 0.0
            if s <= 0.01:
                return "0"
            return f"if(between(t\\,0\\,{s:.6f}),1,0)"

        def _rebuild_audio_on_video(video_path: str, base_with_audio: str, prefer_base_audio: bool = True) -> bool:
            """Ensure video_path has audio by remuxing from base or rebuilding from VO + music."""
            temp_out = os.path.join(ctx.temp_dir, f"final_with_audio_{ctx.main_label_safe()}.mp4")
            # 1) Prefer remuxing audio from base if it has audio
            if prefer_base_audio and base_with_audio and os.path.exists(base_with_audio) and _has_audio_stream(base_with_audio):
                cmd = [
                    "ffmpeg", "-y", "-i", video_path, "-i", base_with_audio,
                    "-map", "0:v", "-map", "1:a",
                    "-c:v", "copy", "-c:a", "aac", "-b:a", "192k",
                    "-shortest", temp_out
                ]
                res = subprocess.run(cmd, capture_output=True, text=True, timeout=300)
                if res.returncode == 0 and os.path.exists(temp_out) and _has_audio_stream(temp_out):
                    os.replace(temp_out, video_path)
                    return True
                ctx.status_callback(
                    f"{ctx.main_label} [Finalization] ⚠️ Remux audio from base failed: {res.stderr[:500] if res.stderr else res.returncode}",
                    True,
                )

            # 2) Rebuild audio from voiceover (+ optional music), forced to target video duration.
            # When video already has audio (base from concat), the base already contains the correct
            # per-segment voiceover; do NOT add the full VO again (would be double + misaligned by intro duration).
            music_path = _pick_music_path()
            video_has_audio = _has_audio_stream(video_path)
            target_duration = _probe_duration(video_path)
            excluded_ts = _compute_excluded_audio_ranges()
            keep_middle_clean = str(ctx.config_settings.get("mute_master_on_middle_clips", "1")).strip().lower() in ("1", "true", "yes")
            # Default ON: evita doppi audio sull'intro quando la clip iniziale ha già traccia.
            keep_start_clean = str(ctx.config_settings.get("mute_master_on_start_clips", "1")).strip().lower() in ("1", "true", "yes")
            if keep_middle_clean or keep_start_clean:
                gate_expr = _ffmpeg_gate_expr_for_timestamps(excluded_ts)
            else:
                gate_expr = "1"

            if video_has_audio and ctx.audio_path and os.path.exists(ctx.audio_path):
                # Base may miss VO after concat. Mix: base (intro+middle only) + VO delayed by intro (stock+end) + music (stock+end).
                # If intro has no audio (e.g. Remotion clip), first N seconds are silent; use delay=0 when VO should play from start.
                raw_start = float(ctx.total_start_duration or 0.0)
                override = ctx.config_settings.get("voiceover_start_offset_seconds")
                if override is not None:
                    try:
                        start_dur = float(override)
                    except (TypeError, ValueError):
                        start_dur = raw_start
                else:
                    start_dur = raw_start
                # When we don't mute VO on start clips, play VO from t=0 (no delay) so intro has audio even if base is silent
                if not keep_start_clean:
                    start_dur = 0.0
                start_ms = max(0, int(round(start_dur * 1000)))
                ctx.status_callback(
                    f"{ctx.main_label} [Finalization] VO delay: {start_dur:.2f}s ({start_ms}ms) (intro total_start_duration={raw_start:.2f}s, mute_on_start={keep_start_clean})",
                    False,
                )
                # Base audio must carry intro only.
                base_gate = _ffmpeg_intro_only_base_gate_expr(raw_start)
                if music_path:
                    vol = float(ctx.config_settings.get("background_music_volume", 0.5))
                    filter_complex = (
                        f"[0:a]aresample=44100,volume='{base_gate}':eval=frame[base];"
                        f"[1:a]aresample=44100,adelay={start_ms}|{start_ms},volume='{gate_expr}':eval=frame[vo];"
                        f"[2:a]aresample=44100,volume='{vol}*{gate_expr}':eval=frame[bg];"
                        "[base][vo][bg]amix=inputs=3:duration=longest:normalize=0[aout]"
                    )
                    cmd = [
                        "ffmpeg", "-y", "-i", video_path, "-i", ctx.audio_path,
                        "-stream_loop", "-1", "-i", music_path,
                        "-filter_complex", filter_complex,
                        "-map", "0:v", "-map", "[aout]",
                        "-c:v", "copy", "-c:a", "aac", "-b:a", "192k", "-ar", "44100", "-ac", "2",
                    ]
                else:
                    filter_complex = (
                        f"[0:a]aresample=44100,volume='{base_gate}':eval=frame[base];"
                        f"[1:a]aresample=44100,adelay={start_ms}|{start_ms},volume='{gate_expr}':eval=frame,apad[vo];"
                        "[base][vo]amix=inputs=2:duration=longest:normalize=0[aout]"
                    )
                    cmd = [
                        "ffmpeg", "-y", "-i", video_path, "-i", ctx.audio_path,
                        "-filter_complex", filter_complex,
                        "-map", "0:v", "-map", "[aout]",
                        "-c:v", "copy", "-c:a", "aac", "-b:a", "192k", "-ar", "44100", "-ac", "2",
                    ]
                if target_duration > 0:
                    cmd.extend(["-t", f"{target_duration:.6f}"])
                cmd.append(temp_out)
                res = subprocess.run(cmd, capture_output=True, text=True, timeout=300)
            elif video_has_audio:
                # No voiceover path: only add music on top of base (legacy fallback).
                if music_path:
                    vol = float(ctx.config_settings.get("background_music_volume", 0.5))
                    filter_complex = (
                        "[0:a]aresample=44100[base];"
                        f"[1:a]aresample=44100,volume='{vol}*{gate_expr}':eval=frame[bg];"
                        "[base][bg]amix=inputs=2:duration=longest:normalize=0[aout]"
                    )
                    cmd = [
                        "ffmpeg", "-y", "-i", video_path,
                        "-stream_loop", "-1", "-i", music_path,
                        "-filter_complex", filter_complex,
                        "-map", "0:v", "-map", "[aout]",
                        "-c:v", "copy", "-c:a", "aac", "-b:a", "192k", "-ar", "44100", "-ac", "2",
                    ]
                else:
                    filter_complex = "[0:a]aresample=44100[aout]"
                    cmd = [
                        "ffmpeg", "-y", "-i", video_path,
                        "-filter_complex", filter_complex,
                        "-map", "0:v", "-map", "[aout]",
                        "-c:v", "copy", "-c:a", "aac", "-b:a", "192k", "-ar", "44100", "-ac", "2",
                    ]
                if target_duration > 0:
                    cmd.extend(["-t", f"{target_duration:.6f}"])
                cmd.append(temp_out)
                res = subprocess.run(cmd, capture_output=True, text=True, timeout=300)
            elif ctx.audio_path and os.path.exists(ctx.audio_path):
                # Video has no audio: build from voiceover (+ optional music) with gate.
                if music_path:
                    vol = float(ctx.config_settings.get("background_music_volume", 0.5))
                    filter_complex = (
                        f"[1:a]aresample=44100,volume='{gate_expr}':eval=frame[vo];"
                        f"[2:a]aresample=44100,volume='{vol}*{gate_expr}':eval=frame[bg];"
                        "[vo][bg]amix=inputs=2:duration=longest:normalize=0[aout]"
                    )
                    cmd = [
                        "ffmpeg", "-y", "-i", video_path, "-i", ctx.audio_path,
                        "-stream_loop", "-1", "-i", music_path,
                        "-filter_complex", filter_complex,
                        "-map", "0:v", "-map", "[aout]",
                        "-c:v", "copy", "-c:a", "aac", "-b:a", "192k", "-ar", "44100", "-ac", "2",
                    ]
                else:
                    filter_complex = f"[1:a]aresample=44100,volume='{gate_expr}':eval=frame,apad[aout]"
                    cmd = [
                        "ffmpeg", "-y", "-i", video_path, "-i", ctx.audio_path,
                        "-filter_complex", filter_complex,
                        "-map", "0:v", "-map", "[aout]",
                        "-c:v", "copy", "-c:a", "aac", "-b:a", "192k", "-ar", "44100", "-ac", "2",
                    ]
                if target_duration > 0:
                    cmd.extend(["-t", f"{target_duration:.6f}"])
                cmd.append(temp_out)
                res = subprocess.run(cmd, capture_output=True, text=True, timeout=300)
            else:
                res = type("Res", (), {"returncode": 1, "stderr": "No audio path and video has no audio"})()

            if res.returncode == 0 and os.path.exists(temp_out) and _has_audio_stream(temp_out):
                os.replace(temp_out, video_path)
                return True
            if res.returncode != 0:
                ctx.status_callback(
                    f"{ctx.main_label} [Finalization] ⚠️ Rebuild audio on final failed: {res.stderr[:500] if getattr(res, 'stderr', None) else res.returncode}",
                    True,
                )
            return False

        # Optionally force voiceover (+ music) rebuild. Skip when Assembly already baked master audio.
        force_voiceover_audio = str(ctx.config_settings.get("force_voiceover_audio", "1")).strip().lower() in ("1", "true", "yes")
        if force_voiceover_audio and not getattr(ctx, "audio_baked_in_assembly", False) and ctx.audio_path and os.path.exists(ctx.audio_path):
            try:
                if _rebuild_audio_on_video(base_video_path, "", prefer_base_audio=False):
                    ctx.status_callback(
                        f"{ctx.main_label} [Finalization] ✅ Forced rebuild of base audio (voiceover + music)",
                        False,
                    )
            except Exception as e:
                ctx.status_callback(
                    f"{ctx.main_label} [Finalization] ⚠️ Forced rebuild failed: {e}",
                    True,
                )

        if not _has_audio_stream(base_video_path):
            music_path = _pick_music_path() or None

            temp_audio_base = os.path.join(ctx.temp_dir, f"base_with_audio_{ctx.main_label_safe()}.mp4")
            rebuilt = False
            if ctx.audio_path and os.path.exists(ctx.audio_path):
                try:
                    if music_path:
                        vol = float(ctx.config_settings.get("background_music_volume", 0.5))
                        cmd = [
                            "ffmpeg", "-y", "-i", base_video_path, "-i", ctx.audio_path, "-i", music_path,
                            "-filter_complex",
                            f"[1:a]volume=1.0[vo];[2:a]volume={vol}[bg];[vo][bg]amix=inputs=2:duration=first:normalize=0[aout]",
                            "-map", "0:v", "-map", "[aout]",
                            "-c:v", "copy", "-c:a", "aac", "-b:a", "192k",
                            "-shortest", temp_audio_base
                        ]
                    else:
                        cmd = [
                            "ffmpeg", "-y", "-i", base_video_path, "-i", ctx.audio_path,
                            "-map", "0:v", "-map", "1:a",
                            "-c:v", "copy", "-c:a", "aac", "-b:a", "192k",
                            "-shortest", temp_audio_base
                        ]
                    res = subprocess.run(cmd, capture_output=True, text=True, timeout=300)
                    if res.returncode == 0 and os.path.exists(temp_audio_base) and _has_audio_stream(temp_audio_base):
                        ctx.temp_files_to_clean.append(base_video_path)
                        base_video_path = temp_audio_base
                        rebuilt = True
                        ctx.status_callback(f"{ctx.main_label} [Finalization] Rebuilt base audio track (voiceover + music)", False)
                    else:
                        ctx.status_callback(
                            f"{ctx.main_label} [Finalization] ⚠️ Audio rebuild ffmpeg failed: {res.stderr[:500] if res.stderr else res.returncode}",
                            True,
                        )
                except Exception as e:
                    ctx.status_callback(f"{ctx.main_label} [Finalization] ⚠️ Audio rebuild failed: {e}", True)

            if not rebuilt and ctx.audio_path and os.path.exists(ctx.audio_path):
                try:
                    cmd = [
                        "ffmpeg", "-y", "-i", base_video_path, "-i", ctx.audio_path,
                        "-map", "0:v", "-map", "1:a",
                        "-c:v", "copy", "-c:a", "aac", "-b:a", "192k",
                        "-shortest", temp_audio_base
                    ]
                    res = subprocess.run(cmd, capture_output=True, text=True, timeout=300)
                    if res.returncode == 0 and os.path.exists(temp_audio_base) and _has_audio_stream(temp_audio_base):
                        ctx.temp_files_to_clean.append(base_video_path)
                        base_video_path = temp_audio_base
                        ctx.status_callback(f"{ctx.main_label} [Finalization] Rebuilt base audio track (voiceover only)", False)
                except Exception as e:
                    ctx.status_callback(f"{ctx.main_label} [Finalization] ⚠️ Voiceover-only rebuild failed: {e}", True)

            if not _has_audio_stream(base_video_path):
                ctx.status_callback(
                    f"{ctx.main_label} [Finalization] ❌ Base has no audio - final video will be silent. Check voiceover and Assembly.",
                    True,
                )
        
        if not ctx.all_rendered_overlay_files_for_ffmpeg_merge:
            ctx.status_callback(f"{ctx.main_label} [Finalization] No overlays to merge. Copying base.", False)
            shutil.copy2(base_video_path, ctx.output_path)
            # Base already has correct audio when baked in Assembly; no need to rebuild
            return ctx.output_path

        # Determine middle clip timestamps for exclusion logic (if needed by merger)
        # In original: middle_clip_timestamps passed to merge function? 
        # Check signature: merge_overlays_ffmpeg(base, overlays, out, middle_clip_timestamps=None)
        # We need to calculate middle clip absolute timestamps on the timeline.
        
        # We constructed the timeline in AssemblyPhase.
        # But we didn't store the exact timestamps of middle clips in absolute time.
        # However, `map_audio_to_video` computed them relative to audio? No.
        # We need to reconstruct the timeline or store it.
        # Original script lines 4340: pass middle_clip_timestamps
        # We need to have tracked them.
        
        # For Refactor MVP: If middle clip timestamps are crucial (to avoid overlay overlap?), 
        # we can reconstruct them by iterating segments and accumulating duration like Assembly does.
        # Stock Results are indexed.
        
        # List of (start, end) tuples for merge_overlays_ffmpeg
        middle_ts = _compute_middle_clip_timestamps()

        # Call merger
        temp_final = os.path.join(ctx.temp_dir, f"video_final_temp_{ctx.main_label_safe()}.mp4")
        merged = merge_overlays_ffmpeg(
            base_video_path=base_video_path,
            overlay_files=ctx.all_rendered_overlay_files_for_ffmpeg_merge,
            output_path=temp_final,
            middle_clip_timestamps=middle_ts
        )
        
        if merged and os.path.exists(merged):
             if os.path.exists(ctx.output_path): os.remove(ctx.output_path)
             shutil.move(merged, ctx.output_path)
             # When Assembly baked audio, just copy base audio onto final; otherwise rebuild from VO
             enforce_master_audio = str(ctx.config_settings.get("enforce_master_audio_final", "1")).strip().lower() in ("1", "true", "yes")
             if enforce_master_audio:
                 try:
                     prefer_base = getattr(ctx, "audio_baked_in_assembly", False)
                     if _rebuild_audio_on_video(ctx.output_path, base_video_path, prefer_base_audio=prefer_base):
                         ctx.status_callback(
                             f"{ctx.main_label} [Finalization] ✅ Master audio applied on final video.",
                             False,
                         )
                 except Exception as e:
                     ctx.status_callback(
                         f"{ctx.main_label} [Finalization] ⚠️ Master audio attach failed: {e}",
                         True,
                     )
             try:
                 if not _has_audio_stream(ctx.output_path):
                     ctx.status_callback(
                         f"{ctx.main_label} [Finalization] ⚠️ Final video missing audio. Attempting repair...",
                         True,
                     )
                     if _rebuild_audio_on_video(ctx.output_path, base_video_path):
                         ctx.status_callback(
                             f"{ctx.main_label} [Finalization] ✅ Audio repaired on final video.",
                             False,
                         )
                     else:
                         ctx.status_callback(
                             f"{ctx.main_label} [Finalization] ❌ Audio repair failed. Final video may be silent.",
                             True,
                         )
             except Exception as e:
                 ctx.status_callback(
                     f"{ctx.main_label} [Finalization] ⚠️ Error while repairing final audio: {e}",
                     True,
                 )
             ctx.status_callback(f"{ctx.main_label} [Finalization] SUCCESS. Video saved to {ctx.output_path}", False)
             return ctx.output_path
        else:
             raise RuntimeError("Final merge failed.")
