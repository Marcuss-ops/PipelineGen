
"""
MODULE: Assembly Phase
DESCRIPTION:
This phase stitches together all the linear components of the video to create the "Base Video".

RESPONSIBILITIES:
- Concatenate: Start Clips + (Segment 1 + Middle 1 + Segment 2...) + End Clips.
- Bake master audio: voiceover (+ music) with offset/gate so VO does NOT play over initial and middle clips (they have no audio).
- Fallback: add only background music if no voiceover.
- Produce the `base_video_path` which is the canvas for overlays (and for entity overlays on top).

INTERACTIONS:
- Input: `GenerationContext` (lists of clips, music path).
- Output: Returns `base_video_path`.
- Dependencies: `ffmpeg_utils.concatenate_videos`, bake step (ffmpeg).
"""
import os
import subprocess
from typing import List, Tuple
from ..common.context import GenerationContext
try:
    from ....ffmpeg_utils import concatenate_videos_fast, get_ffmpeg_processor
except ImportError:
    try:
        from modules.video.ffmpeg_utils import concatenate_videos_fast, get_ffmpeg_processor
    except ImportError:
        pass  # Handle separately or rely on fallback

try:
    from ....video_ffmpeg import concatenate_videos_ffmpeg_safe
except ImportError:
    try:
        from modules.video.video_ffmpeg import concatenate_videos_ffmpeg_safe
    except ImportError:
        concatenate_videos_ffmpeg_safe = None


def _probe_duration(path: str) -> float:
    try:
        cmd = [
            "ffprobe", "-v", "error",
            "-show_entries", "format=duration",
            "-of", "default=noprint_wrappers=1:nokey=1", path,
        ]
        res = subprocess.run(cmd, capture_output=True, text=True, timeout=10)
        if res.returncode == 0 and res.stdout.strip():
            return float(res.stdout.strip())
    except Exception:
        pass
    return 0.0


def _pick_music_path(ctx: GenerationContext) -> str:
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
        if p and os.path.exists(str(p)):
            return str(p)
    return ""


def _compute_middle_clip_timestamps(ctx: GenerationContext) -> List[Tuple[float, float]]:
    """Absolute (start, end) timestamps for middle clips on the final timeline."""
    if getattr(ctx, "middle_clip_timestamps_abs", None):
        return list(ctx.middle_clip_timestamps_abs)
    middle_ts_local: List[Tuple[float, float]] = []
    current_time = float(ctx.total_start_duration or 0.0)
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


def _split_video_for_insertions(
    source_video_path: str,
    split_points: List[float],
    temp_dir: str,
    label_safe: str,
) -> List[str]:
    """
    Split source video into segments at absolute split points (seconds).
    Returns a list of segment paths in temporal order.
    """
    total_dur = _probe_duration(source_video_path)
    if total_dur <= 0:
        return [source_video_path]

    # Keep only valid strictly increasing points inside (0, total_dur)
    filtered_points: List[float] = []
    for p in sorted(split_points):
        try:
            fp = float(p)
        except Exception:
            continue
        if fp <= 0.01 or fp >= (total_dur - 0.01):
            continue
        if not filtered_points or fp - filtered_points[-1] > 0.01:
            filtered_points.append(fp)

    if not filtered_points:
        return [source_video_path]

    boundaries = [0.0] + filtered_points + [total_dur]
    out_segments: List[str] = []
    for i in range(len(boundaries) - 1):
        seg_start = boundaries[i]
        seg_end = boundaries[i + 1]
        if seg_end - seg_start <= 0.01:
            continue
        out_path = os.path.join(temp_dir, f"base_cut_{i:03d}_{label_safe}.mp4")
        cmd = [
            "ffmpeg",
            "-y",
            "-i",
            source_video_path,
            "-ss",
            f"{seg_start:.6f}",
            "-to",
            f"{seg_end:.6f}",
            "-c:v",
            "libx264",
            "-preset",
            "veryfast",
            "-crf",
            "20",
            "-c:a",
            "aac",
            "-b:a",
            "192k",
            out_path,
        ]
        res = subprocess.run(cmd, capture_output=True, text=True, timeout=300)
        if res.returncode != 0 or not os.path.exists(out_path):
            return [source_video_path]
        out_segments.append(out_path)

    return out_segments if out_segments else [source_video_path]


def _compute_excluded_audio_ranges(ctx: GenerationContext) -> List[Tuple[float, float]]:
    """Ranges where VO/music must be muted: intro only (middle clips must NOT pause voiceover)."""
    ranges: List[Tuple[float, float]] = []
    start_dur = _safe_start_duration(ctx)
    if start_dur > 0.01:
        ranges.append((0.0, start_dur))
    return ranges


def _safe_start_duration(ctx: GenerationContext) -> float:
    """Return a safe intro duration value for audio gating/mixing logic."""
    try:
        value = float(getattr(ctx, "total_start_duration", 0.0) or 0.0)
        return max(0.0, value)
    except Exception:
        return 0.0


def _ffmpeg_gate_expr_for_timestamps(timestamps: List[Tuple[float, float]]) -> str:
    """FFmpeg expression: 1 outside excluded ranges, 0 during (for VO/music)."""
    if not timestamps:
        return "1"
    parts = [f"between(t\\,{float(s):.6f}\\,{float(e):.6f})" for s, e in timestamps if float(e) > float(s)]
    if not parts:
        return "1"
    return f"if({'+'.join(parts)},0,1)"


def _ffmpeg_base_gate_expr(timestamps: List[Tuple[float, float]]) -> str:
    """FFmpeg expression: 1 during intro+middle ranges, 0 outside (for base clip audio)."""
    if not timestamps:
        return "0"
    parts = [f"between(t\\,{float(s):.6f}\\,{float(e):.6f})" for s, e in timestamps if float(e) > float(s)]
    if not parts:
        return "0"
    return f"if({'+'.join(parts)},1,0)"


def _ffmpeg_intro_only_base_gate_expr(start_dur: float) -> str:
    """
    FFmpeg expression for base audio:
    keep ONLY intro range from base, mute everything else (including middle clips).
    """
    try:
        s = float(start_dur or 0.0)
    except Exception:
        s = 0.0
    if s <= 0.01:
        return "0"
    return f"if(between(t\\,0\\,{s:.6f}),1,0)"


def _has_audio_stream(path: str) -> bool:
    """True if the file has at least one audio stream."""
    try:
        cmd = [
            "ffprobe", "-v", "error", "-select_streams", "a:0",
            "-show_entries", "stream=index", "-of", "csv=p=0", path,
        ]
        res = subprocess.run(cmd, capture_output=True, text=True, timeout=10)
        return res.returncode == 0 and bool((res.stdout or "").strip())
    except Exception:
        return False


def _intro_only_gate_expr(start_dur: float) -> str:
    """Gate: 1 only in (0, start_dur), 0 elsewhere. Per usare solo l'audio della clip iniziale nell'intro."""
    if start_dur <= 0.01:
        return "0"
    return f"if(between(t\\,0\\,{float(start_dur):.6f}),1,0)"


def _bake_master_audio_on_base(ctx: GenerationContext, base_video_path: str):
    """
    Replace the base video's audio with VO (+ music), applying offset and gate so that
    voiceover and music do NOT play over initial clips.
    Middle clips are visual-only inserts and must NOT pause voiceover.
    - Intro: use audio from the first start clip file directly (così si sente sempre), then base for middle.
    - Delay VO by total_start_duration so it starts after the intro.
    - Gate VO and music to 0 only during (0, total_start_duration).
    Returns the path to the new file, or None on failure.
    """
    video_dur = _probe_duration(base_video_path)
    if video_dur <= 0:
        ctx.status_callback(f"{ctx.main_label} [AssemblyPhase] ⚠️ Could not probe base duration, skip bake", True)
        return None
    music_path = _pick_music_path(ctx)
    vol = float(ctx.config_settings.get("background_music_volume", 0.5))
    out_path = os.path.join(ctx.temp_dir, f"base_with_audio_{ctx.main_label_safe()}.mp4")

    # Offset: VO starts after intro (mute only intro, not middle clips).
    # Keep defensive initialization to avoid any UnboundLocal regressions.
    start_dur = _safe_start_duration(ctx)
    excluded_ts = _compute_excluded_audio_ranges(ctx)
    gate_expr = _ffmpeg_gate_expr_for_timestamps(excluded_ts)
    # IMPORTANT: base audio must come only from intro.
    # Middle clips are expected to be silent in final master mix.
    base_gate_expr = _ffmpeg_intro_only_base_gate_expr(start_dur)
    start_ms = max(0, int(round(start_dur * 1000)))
    base_has_audio = _has_audio_stream(base_video_path)
    # Prima start clip: usiamo il suo audio esplicitamente per l'intro (il concat a volte non lo preserva)
    first_start_clip = (ctx.start_clips_paths or [None])[0] if ctx.start_clips_paths else None
    use_start_clip_audio = (
        first_start_clip
        and os.path.exists(first_start_clip)
        and _has_audio_stream(first_start_clip)
        and start_dur > 0.01
    )
    intro_gate_expr = _intro_only_gate_expr(start_dur) if use_start_clip_audio else None
    ctx.status_callback(
        f"{ctx.main_label} [AssemblyPhase] Bake audio: VO delay={start_dur:.2f}s, gate intro-only ({len(excluded_ts)} ranges), base_audio={'yes' if base_has_audio else 'no'}, intro_from_clip={'yes' if use_start_clip_audio else 'no'}",
        False,
    )

    try:
        if use_start_clip_audio:
            # Intro: audio dalla clip iniziale (input aggiuntivo). Middle+rest: base. Poi VO + music gated.
            # [intro_file] atrim 0:start_dur, apad to video_dur, volume=intro_gate -> [intro]
            # [0:a] volume=base_gate (middle + eventuale resto base) -> [base]
            # [1:a] adelay + gate -> [vo], [2:a] gate -> [bg]
            # amix intro + base + vo + bg (4 inputs) oppure intro + base + vo (3)
            # Indici: 0=base_video, 1=vo, 2=music, 3=start_clip
            if music_path:
                filter_complex = (
                    f"[3:a]aresample=44100,atrim=0:{start_dur:.6f},asetpts=PTS-STARTPTS,apad=whole_dur={video_dur:.6f},volume='{intro_gate_expr}':eval=frame[intro];"
                    f"[0:a]aresample=44100,volume='{base_gate_expr}':eval=frame[base];"
                    f"[1:a]aresample=44100,adelay={start_ms}|{start_ms},volume='{gate_expr}':eval=frame,apad=whole_dur={video_dur:.6f}[vo];"
                    f"[2:a]aresample=44100,atrim=0:{video_dur:.6f},asetpts=PTS-STARTPTS,volume='{vol}*{gate_expr}':eval=frame[bg];"
                    "[intro][base][vo][bg]amix=inputs=4:duration=longest:normalize=0[aout]"
                )
                cmd = [
                    "ffmpeg", "-y", "-i", base_video_path, "-i", ctx.audio_path,
                    "-stream_loop", "-1", "-i", music_path, "-i", first_start_clip,
                    "-filter_complex", filter_complex,
                    "-map", "0:v", "-map", "[aout]",
                    "-t", f"{video_dur:.6f}",
                    "-c:v", "copy", "-c:a", "aac", "-b:a", "192k", "-ar", "44100", "-ac", "2",
                    out_path,
                ]
            else:
                filter_complex = (
                    f"[2:a]aresample=44100,atrim=0:{start_dur:.6f},asetpts=PTS-STARTPTS,apad=whole_dur={video_dur:.6f},volume='{intro_gate_expr}':eval=frame[intro];"
                    f"[0:a]aresample=44100,volume='{base_gate_expr}':eval=frame[base];"
                    f"[1:a]aresample=44100,adelay={start_ms}|{start_ms},volume='{gate_expr}':eval=frame,apad=whole_dur={video_dur:.6f}[vo];"
                    "[intro][base][vo]amix=inputs=3:duration=longest:normalize=0[aout]"
                )
                cmd = [
                    "ffmpeg", "-y", "-i", base_video_path, "-i", ctx.audio_path, "-i", first_start_clip,
                    "-filter_complex", filter_complex,
                    "-map", "0:v", "-map", "[aout]",
                    "-t", f"{video_dur:.6f}",
                    "-c:v", "copy", "-c:a", "aac", "-b:a", "192k", "-ar", "44100", "-ac", "2",
                    out_path,
                ]
        elif base_has_audio:
            # Mix: base audio (solo intro+middle) + VO (solo stock/end) + music (solo stock/end)
            if music_path:
                filter_complex = (
                    f"[0:a]aresample=44100,volume='{base_gate_expr}':eval=frame[base];"
                    f"[1:a]aresample=44100,adelay={start_ms}|{start_ms},volume='{gate_expr}':eval=frame,apad=whole_dur={video_dur:.6f}[vo];"
                    f"[2:a]aresample=44100,atrim=0:{video_dur:.6f},asetpts=PTS-STARTPTS,volume='{vol}*{gate_expr}':eval=frame[bg];"
                    "[base][vo][bg]amix=inputs=3:duration=longest:normalize=0[aout]"
                )
                cmd = [
                    "ffmpeg", "-y", "-i", base_video_path, "-i", ctx.audio_path,
                    "-stream_loop", "-1", "-i", music_path,
                    "-filter_complex", filter_complex,
                    "-map", "0:v", "-map", "[aout]",
                    "-t", f"{video_dur:.6f}",
                    "-c:v", "copy", "-c:a", "aac", "-b:a", "192k", "-ar", "44100", "-ac", "2",
                    out_path,
                ]
            else:
                filter_complex = (
                    f"[0:a]aresample=44100,volume='{base_gate_expr}':eval=frame[base];"
                    f"[1:a]aresample=44100,adelay={start_ms}|{start_ms},volume='{gate_expr}':eval=frame,apad=whole_dur={video_dur:.6f}[vo];"
                    "[base][vo]amix=inputs=2:duration=longest:normalize=0[aout]"
                )
                cmd = [
                    "ffmpeg", "-y", "-i", base_video_path, "-i", ctx.audio_path,
                    "-filter_complex", filter_complex,
                    "-map", "0:v", "-map", "[aout]",
                    "-t", f"{video_dur:.6f}",
                    "-c:v", "copy", "-c:a", "aac", "-b:a", "192k", "-ar", "44100", "-ac", "2",
                    out_path,
                ]
        elif music_path:
            # Base senza audio: solo VO + music (gate su intro+middle)
            filter_complex = (
                f"[1:a]aresample=44100,adelay={start_ms}|{start_ms},volume='{gate_expr}':eval=frame,apad=whole_dur={video_dur:.6f}[vo];"
                f"[2:a]aresample=44100,atrim=0:{video_dur:.6f},asetpts=PTS-STARTPTS,volume='{vol}*{gate_expr}':eval=frame[bg];"
                "[vo][bg]amix=inputs=2:duration=longest:normalize=0[aout]"
            )
            cmd = [
                "ffmpeg", "-y", "-i", base_video_path, "-i", ctx.audio_path,
                "-stream_loop", "-1", "-i", music_path,
                "-filter_complex", filter_complex,
                "-map", "0:v", "-map", "[aout]",
                "-t", f"{video_dur:.6f}",
                "-c:v", "copy", "-c:a", "aac", "-b:a", "192k", "-ar", "44100", "-ac", "2",
                out_path,
            ]
        else:
            filter_complex = (
                f"[1:a]aresample=44100,adelay={start_ms}|{start_ms},volume='{gate_expr}':eval=frame,apad=whole_dur={video_dur:.6f}[aout]"
            )
            cmd = [
                "ffmpeg", "-y", "-i", base_video_path, "-i", ctx.audio_path,
                "-filter_complex", filter_complex,
                "-map", "0:v", "-map", "[aout]",
                "-t", f"{video_dur:.6f}",
                "-c:v", "copy", "-c:a", "aac", "-b:a", "192k", "-ar", "44100", "-ac", "2",
                out_path,
            ]
        res = subprocess.run(cmd, capture_output=True, text=True, timeout=300)
        if res.returncode == 0 and os.path.exists(out_path):
            ctx.temp_files_to_clean.append(base_video_path)
            ctx.audio_baked_in_assembly = True
            ctx.status_callback(
                f"{ctx.main_label} [AssemblyPhase] Baked master audio (base intro/middle + VO + music) onto base",
                False,
            )
            return out_path
        err = (res.stderr or "")[:300] if hasattr(res, "stderr") else str(res.returncode)
        ctx.status_callback(f"{ctx.main_label} [AssemblyPhase] ⚠️ Bake audio failed: {err}", True)
    except Exception as e:
        ctx.status_callback(f"{ctx.main_label} [AssemblyPhase] ⚠️ Bake audio error: {e}", True)
    return None


class AssemblyPhase:
    def __init__(self, context: GenerationContext):
        self.ctx = context

    def run(self) -> str:
        ctx = self.ctx
        ctx.status_callback(f"{ctx.main_label} [AssemblyPhase] Starting Assembly...", False)

        # 1. Build STOCK-ONLY base first (stock + end only).
        #    Start clips and middle clips are inserted later on the already baked base.
        stock_only_segments: List[str] = []

        stock_results = ctx.stock_segment_results
        tasks = ctx.stock_tasks_args_list
        sorted_indices = sorted(stock_results.keys())

        # Insert points are on STOCK-ONLY timeline (before adding start clips).
        insertion_points: List[float] = []
        middle_paths_to_insert: List[str] = []
        current_time = 0.0

        for idx in sorted_indices:
            path, dur = stock_results[idx]
            if os.path.exists(path):
                stock_only_segments.append(path)
                seg_dur = _probe_duration(path) or dur or 0.0
                current_time += max(0.0, float(seg_dur))

            # Followed-by-middle means: insert middle exactly at this boundary on stock-only base.
            task = next((t for t in tasks if t["segment_index"] == idx), None)
            if task and task.get("followed_by_clip", False):
                next_idx = len(middle_paths_to_insert)
                if next_idx < len(ctx.middle_clips_paths):
                    mid_path = ctx.middle_clips_paths[next_idx]
                    if os.path.exists(mid_path):
                        insertion_points.append(current_time)
                        middle_paths_to_insert.append(mid_path)

        # Keep end clips as part of stock base tail.
        stock_only_segments.extend(ctx.end_clips_paths)

        if not stock_only_segments:
            raise RuntimeError("No segments to concatenate!")

        # 2. Concatenate STOCK-ONLY base
        stock_only_base_path = os.path.join(
            ctx.temp_dir,
            f"base_stock_only_{len(stock_only_segments)}_{ctx.main_label_safe()}.mp4",
        )
        if not concatenate_videos_fast(stock_only_segments, stock_only_base_path):
            raise RuntimeError("Concatenation failed (stock-only base).")

        # 3. Bake voiceover/music on stock-only base FIRST.
        #    This guarantees continuous VO over stock timeline (middle clips are independent visuals).
        stock_baked_base_path = stock_only_base_path
        if ctx.audio_path and os.path.exists(ctx.audio_path):
            original_start_duration = ctx.total_start_duration
            try:
                # No delay here: base starts at stock timeline t=0.
                ctx.total_start_duration = 0.0
                baked_stock = _bake_master_audio_on_base(ctx, stock_only_base_path)
                if baked_stock:
                    stock_baked_base_path = baked_stock
            finally:
                ctx.total_start_duration = original_start_duration

        # 4. Split baked stock base and insert middle clips at computed points.
        final_segments_to_concat: List[str] = []
        middle_ts_abs: List[Tuple[float, float]] = []
        start_total = 0.0
        for p in ctx.start_clips_paths:
            start_total += max(0.0, float(_probe_duration(p) or 0.0))

        # Start clips are always placed in front of the baked stock timeline.
        final_segments_to_concat.extend(ctx.start_clips_paths)

        if insertion_points and middle_paths_to_insert:
            base_parts = _split_video_for_insertions(
                source_video_path=stock_baked_base_path,
                split_points=insertion_points,
                temp_dir=ctx.temp_dir,
                label_safe=ctx.main_label_safe(),
            )

            # If split fails, fallback to previous behaviour (append middle after related segments).
            split_ok = len(base_parts) == (len(insertion_points) + 1)
            if split_ok:
                shift = 0.0
                for i, part in enumerate(base_parts):
                    final_segments_to_concat.append(part)
                    if i < len(middle_paths_to_insert):
                        mid = middle_paths_to_insert[i]
                        final_segments_to_concat.append(mid)
                        mdur = _probe_duration(mid)
                        if (not mdur or mdur <= 0) and i < len(ctx.middle_clip_actual_durations):
                            mdur = ctx.middle_clip_actual_durations[i]
                        mdur = float(mdur or 0.0)
                        abs_start = start_total + float(insertion_points[i]) + shift
                        abs_end = abs_start + max(0.0, mdur)
                        if abs_end > abs_start:
                            middle_ts_abs.append((abs_start, abs_end))
                        shift += max(0.0, mdur)
            else:
                ctx.status_callback(
                    f"{ctx.main_label} [AssemblyPhase] ⚠️ Split fallback: middle clips skipped to keep stable timeline",
                    True,
                )
                final_segments_to_concat.append(stock_baked_base_path)
        else:
            final_segments_to_concat.append(stock_baked_base_path)

        if middle_ts_abs:
            ctx.middle_clip_timestamps_abs = middle_ts_abs
            ctx.status_callback(
                f"{ctx.main_label} [AssemblyPhase] Middle clip timestamps: "
                + ", ".join(f"{s:.2f}-{e:.2f}s" for s, e in middle_ts_abs[:4])
                + (" ..." if len(middle_ts_abs) > 4 else ""),
                False,
            )

        # 5. Concatenate final timeline (start + baked stock with middle insertions)
        base_video_path = os.path.join(
            ctx.temp_dir, f"base_video_{len(final_segments_to_concat)}_{ctx.main_label_safe()}.mp4"
        )
        concat_ok = False
        # When middle clips are involved, use robust safe concat (re-encode) to avoid frozen frames.
        if middle_paths_to_insert and callable(concatenate_videos_ffmpeg_safe):
            base_w, base_h, base_fps = 1920, 1080, 30.0
            try:
                proc = get_ffmpeg_processor()
                info = proc.get_video_info(stock_baked_base_path) if proc else {}
                base_w = int(info.get("width") or base_w)
                base_h = int(info.get("height") or base_h)
                base_fps = float(info.get("fps") or base_fps)
            except Exception:
                pass
            concat_ok = bool(
                concatenate_videos_ffmpeg_safe(
                    final_segments_to_concat,
                    base_video_path,
                    base_w=base_w,
                    base_h=base_h,
                    base_fps=base_fps,
                    status_callback=lambda msg: ctx.status_callback(f"{ctx.main_label} [AssemblyPhase] {msg}", False),
                )
            )
        else:
            concat_ok = bool(concatenate_videos_fast(final_segments_to_concat, base_video_path))

        if not concat_ok:
            raise RuntimeError("Concatenation failed (final timeline).")

        # CRITICAL: Set video_duration_for_overlays_limit for OverlayPhase
        try:
            from moviepy import VideoFileClip
            with VideoFileClip(base_video_path) as clip:
                d = getattr(clip, "duration", 0) or 0
                ctx.video_duration_for_overlays_limit = float(d)
                ctx.status_callback(f"{ctx.main_label} [AssemblyPhase] Set overlay limit: {ctx.video_duration_for_overlays_limit:.2f}s", False)
        except Exception as e:
            ctx.video_duration_for_overlays_limit = float(ctx.audio_duration or 0.0)
            ctx.status_callback(f"{ctx.main_label} [AssemblyPhase] Overlay limit fallback to audio duration: {ctx.video_duration_for_overlays_limit:.2f}s", True)

        # 6. If no voiceover exists, add only background music.
        #    (When voiceover exists, audio is already baked on stock timeline before insertion.)
        if not (ctx.audio_path and os.path.exists(ctx.audio_path)):
            # No voiceover: add only background music if available
            import random
            def _ensure_list(value) -> List[str]:
                if not value:
                    return []
                if isinstance(value, str):
                    s = value.strip()
                    return [s] if s else []
                if isinstance(value, (list, tuple, set)):
                    out: List[str] = []
                    for v in value:
                        if not v:
                            continue
                        out.append(str(v).strip())
                    return [v for v in out if v]
                return []
            music_list = _ensure_list(ctx.config_settings.get("background_music_paths"))
            if not music_list:
                music_list = _ensure_list(ctx.config_settings.get("background_music"))
            if music_list:
                music_path = random.choice(music_list)
                if os.path.exists(music_path):
                    music_video_path = os.path.join(ctx.temp_dir, f"video_with_music_{ctx.main_label_safe()}.mp4")
                    proc = get_ffmpeg_processor()
                    vol = float(ctx.config_settings.get("background_music_volume", 0.5))
                    if proc.add_background_music_ffmpeg(base_video_path, music_path, music_video_path, music_volume=vol):
                        ctx.temp_files_to_clean.append(base_video_path)
                        base_video_path = music_video_path

        ctx.status_callback(f"{ctx.main_label} [AssemblyPhase] Base video created (with audio): {base_video_path}", False)
        return base_video_path
