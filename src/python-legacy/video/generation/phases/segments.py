
"""
MODULE: Segments Phase
DESCRIPTION:
This phase is the core of the "Stock Footage" generation. It divides the audio into logical segments 
(accounting for middle clips) and generates voiced stock video segments for each part.

RESPONSIBILITIES:
- Calculate timelines for segments, interleaving them with middle clips (Segment 1 -> Middle 1 -> Segment 2...).
- Select appropriate stock footage for each segment using `get_stock_clips_for_segment`.
- Execute parallel generation tasks (`_generate_voiced_stock_segment_task`) to create the video chunks.
- Store the paths of generated segments in `ctx.audio_segments`.

INTERACTIONS:
- Input: `GenerationContext` (audio path, durations).
- Output: Populates `ctx.audio_segments` with paths to generated `.mp4` files.
- Dependencies: `concurrent.futures` for parallelism, `video_generation.generate_stock_segment`.
"""
import os
import logging
import concurrent.futures
from typing import List, Dict, Any, Tuple, Optional
from concurrent.futures import as_completed

from ..common.context import GenerationContext
try:
    from ....video_audio import _generate_voiced_stock_segment_task
except ImportError:
    # Fallback to absolute import or dummy if needed during refactor tests
    try:
        from modules.video.video_audio import _generate_voiced_stock_segment_task
    except ImportError:
        def _generate_voiced_stock_segment_task(args): raise ImportError("_generate_voiced_stock_segment_task not found")


def _dummy_callback(msg: str, error: bool = False):
    if error:
        print(f"[WORKER ERROR] {msg}")
    else:
        print(f"[WORKER INFO] {msg}")

class SegmentsPhase:
    def __init__(self, context: GenerationContext):
        self.ctx = context
        self._stock_by_basename = {}
        self._init_stock_map()

    def run(self):
        ctx = self.ctx
        ctx.status_callback(f"{ctx.main_label} [SegmentsPhase] Starting Segmentation...", False)

        from ..phases.audio import AudioPhase
        voiceover_duration = 0.0
        try:
            from moviepy import AudioFileClip
            with AudioFileClip(ctx.audio_path) as a: voiceover_duration = a.duration
        except: pass
        
        if voiceover_duration <= 0.1:
             ctx.status_callback(f"{ctx.main_label} [SegmentsPhase] Voiceover duration too short: {voiceover_duration}", True)
             return

        audio_segments = self._calculate_segments(voiceover_duration)
        ctx.status_callback(f"{ctx.main_label} [SegmentsPhase] Calculated {len(audio_segments)} segments.", False)

        ctx.stock_tasks_args_list = []
        for i, seg in enumerate(audio_segments):
            duration = seg["duration"]
            if duration <= 0.1: continue
            
            task = {
                "segment_index": i,
                "duration_needed": duration,
                "audio_path_main": ctx.audio_path,
                "audio_offset": seg["start"],
                "stock_clips_src": self._get_stock_clips_for_segment(seg["start"], duration),
                "temp_dir_segments": ctx.temp_dir,
                "temp_dir_mux": ctx.temp_dir,
                "status_callback": _dummy_callback, # Use picklable function
                "base_w": ctx.base_w,
                "base_h": ctx.base_h,
                "base_fps": ctx.base_fps,
                "main_label": ctx.main_label,
                "config_settings": ctx.config_settings,
                "followed_by_clip": seg["followed_by_clip"]
            }
            ctx.stock_tasks_args_list.append(task)
            
        self._execute_tasks()

    def _init_stock_map(self):
        for p in self.ctx.stock_clips_sources:
            if p and os.path.isfile(p):
                bn = os.path.basename(p)
                if bn: self._stock_by_basename[bn] = p

    def _calculate_segments(self, total_dur: float) -> List[Dict[str, Any]]:
        ctx = self.ctx
        segments = []
        middle_durations = ctx.middle_clip_actual_durations
        
        M = len(middle_durations) if middle_durations else 0
        
        if M > 0 and total_dur > 0.1:
            base_seg_dur = total_dur / (M + 1)
            current_time = 0.0
            
            for i in range(M):
                seg_dur = base_seg_dur
                segments.append({
                    "start": current_time,
                    "duration": seg_dur,
                    "followed_by_clip": True
                })
                current_time += seg_dur
            
            remaining = total_dur - current_time
            if remaining > 0.01:
                segments.append({
                    "start": current_time,
                    "duration": remaining,
                    "followed_by_clip": False
                })
        else:
             num = 10
             seg_dur = total_dur / num
             for i in range(num):
                 start = i * seg_dur
                 dur = min(seg_dur, total_dur - start)
                 if dur > 0.01:
                     segments.append({"start": start, "duration": dur, "followed_by_clip": False})
                     
        return segments

    def _get_stock_clips_for_segment(self, start: float, duration: float) -> List[str]:
        end = start + duration
        ctx = self.ctx
        cfg_timestamps = ctx.config_settings.get("stock_clips_timestamps", [])
        
        for ts in cfg_timestamps:
            if not isinstance(ts, dict): continue
            try:
                s = float(ts.get("start", -1))
                e = float(ts.get("end", -1))
                if abs(s) < 1.0 and abs(e) < 1.0:
                    return self._resolve_paths(ts.get("stock_paths", []))
            except: continue
            
        matching = []
        for ts in cfg_timestamps:
            if not isinstance(ts, dict): continue
            try:
                s = float(ts.get("start", -1))
                e = float(ts.get("end", -1))
                if s < 0 or e <= s: continue
                
                if start < e and end > s:
                    paths = self._resolve_paths(ts.get("stock_paths", []))
                    for p in paths:
                         matching.append(f"{p}||{s}||{e}")
            except: continue
            
        if matching: return matching
        return ctx.stock_clips_sources

    def _resolve_paths(self, raw_paths: Any) -> List[str]:
        paths = []
        items = raw_paths if isinstance(raw_paths, (list, tuple)) else [raw_paths]
        for item in items:
            if not item: continue
            s = str(item).strip()
            if os.path.exists(s):
                paths.append(s)
            else:
                bn = os.path.basename(s)
                if bn in self._stock_by_basename:
                     paths.append(self._stock_by_basename[bn])
        return paths

    def _execute_tasks(self):
        ctx = self.ctx
        tasks = ctx.stock_tasks_args_list
        if not tasks: return

        ctx.status_callback(f"{ctx.main_label} [SegmentsPhase] Executing {len(tasks)} tasks...", False)
        
        ctx.stock_segment_results = {}
        processed_count = 0
        
        max_workers = ctx.config_settings.get("max_workers_stock_gen", 3)
        
        with concurrent.futures.ProcessPoolExecutor(max_workers=max_workers) as executor:
            # Use **t to unpack arguments
            futures = {executor.submit(_generate_voiced_stock_segment_task, **t): t for t in tasks}
            
            for future in as_completed(futures):
                task = futures[future]
                idx = task["segment_index"]
                try:
                    result = future.result() # Expect (path, duration) or None
                    if result and len(result) == 2:
                        path, dur = result
                        dur = float(dur) if dur is not None else 0.0
                        ctx.stock_segment_results[idx] = (path, dur)
                        processed_count += 1
                        ctx.status_callback(f"{ctx.main_label} [SegmentsPhase] ✅ Segment {idx} done ({dur:.2f}s)", False)
                    else:
                        ctx.status_callback(f"{ctx.main_label} [SegmentsPhase] ❌ Segment {idx} failed (None)", True)
                except Exception as e:
                     import traceback
                     ctx.status_callback(f"{ctx.main_label} [SegmentsPhase] ❌ Segment {idx} error: {e}", True)
                    
        ctx.status_callback(f"{ctx.main_label} [SegmentsPhase] Completed {processed_count}/{len(tasks)} segments.", False)
        
        # Apply Remotion StyleStock animations to every Nth segment (independent of overlay_engine for text/entities)
        if ctx.stock_segment_results:
            try:
                from modules.video.stock_remotion_tail import apply_stock_remotion_tail
                from modules.video.remotion_renderer import find_remotion_project
                
                remotion_project_path = find_remotion_project()
                
                ctx.status_callback(f"{ctx.main_label} [StockRemotion] Applying StyleStock animations...", False)
                
                ctx.stock_segment_results = apply_stock_remotion_tail(
                    stock_segment_results=ctx.stock_segment_results,
                    stock_clips_sources=ctx.stock_clips_sources,
                    stock_clips_timestamps=getattr(ctx, 'stock_clips_timestamps', None),
                    config_settings=ctx.config_settings,
                    overlay_engine=ctx.overlay_engine,
                    base_w=ctx.base_w,
                    base_h=ctx.base_h,
                    base_fps=ctx.base_fps,
                    temp_dir=ctx.temp_dir,
                    seed=hash(ctx.audio_path) % 10000,
                    remotion_project_path_cached=remotion_project_path,
                    status_callback=ctx.status_callback,
                )
                
                ctx.status_callback(f"{ctx.main_label} [StockRemotion] ✅ StyleStock animations applied", False)
                
            except Exception as e:
                import traceback
                ctx.status_callback(f"{ctx.main_label} [StockRemotion] ⚠️ StyleStock failed: {e}", True)
        
        # Re-mux voiceover after StockRemotion (StyleStock) because re-encode drops audio
        if ctx.stock_segment_results and ctx.audio_path and os.path.exists(ctx.audio_path):
            try:
                from modules.video.video_audio import mux_stock_with_voiceover
            except ImportError:
                try:
                    from ....video_audio import mux_stock_with_voiceover
                except ImportError:
                    mux_stock_with_voiceover = None
            if mux_stock_with_voiceover:
                # Build quick index -> task map for offsets
                task_by_idx = {t.get("segment_index"): t for t in (ctx.stock_tasks_args_list or [])}
                remuxed = {}
                for idx, (path, dur) in ctx.stock_segment_results.items():
                    task = task_by_idx.get(idx) or {}
                    audio_offset = float(task.get("audio_offset", 0.0))
                    dur_f = float(dur) if dur is not None else 0.0
                    if not path or not os.path.exists(path):
                        remuxed[idx] = (path, dur_f)
                        continue
                    try:
                        import hashlib
                        seg_hash = hashlib.md5(f"{ctx.audio_path}|{audio_offset:.3f}|{dur_f:.3f}".encode()).hexdigest()[:6]
                        out_path = os.path.join(ctx.temp_dir, f"stock_voiced_{idx}_{seg_hash}.mp4")
                        ctx.status_callback(
                            f"{ctx.main_label} [Mux] Post-StockRemotion segmento #{idx}: audio {audio_offset:.2f}s → {audio_offset + dur_f:.2f}s (dur {dur_f:.2f}s)",
                            False,
                        )
                        muxed = mux_stock_with_voiceover(
                            silent_video_path=path,
                            full_audio_path=ctx.audio_path,
                            audio_start_offset=audio_offset,
                            audio_segment_duration=dur_f,
                            output_path=out_path,
                            status_callback=ctx.status_callback,
                        )
                        if muxed and os.path.exists(muxed):
                            remuxed[idx] = (muxed, dur_f)
                            ctx.temp_files_to_clean.append(muxed)
                        else:
                            remuxed[idx] = (path, dur_f)
                    except Exception as e:
                        ctx.status_callback(f"{ctx.main_label} [Mux] ⚠️ Remux fallito segmento #{idx}: {e}", True)
                        remuxed[idx] = (path, dur_f)
                ctx.stock_segment_results = remuxed
