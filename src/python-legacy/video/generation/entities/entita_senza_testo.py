
"""
MODULE: Entity: Entita Senza Testo (Images)
DESCRIPTION:
Handler for "Entita_Senza_Testo". These are visual entities (Images) without associated text.
This handler often processes them in batches (Entity Stacks) or singly.

RESPONSIBILITIES:
- Check for associated images in `ctx.formatted_img_entities` or `ctx.associazioni_finali_con_timestamp`.
- Render images using `_render_single_image_overlay_task_worker` (parallelized) or EntityStack logic.
- Integrates with `remotion` for 3D stacks if multiple images overlap.

INTERACTIONS:
- Input: Timestamps.
- Output: Overlay video file (image or stack).
"""
import os
import concurrent.futures
from concurrent.futures import as_completed
from typing import Dict, Any, List, Tuple
from .base import BaseEntityHandler
from ..common.helpers import map_audio_to_video, overlaps_entity_stack
try:
    from ....video_overlays import _render_single_image_overlay_task_worker
except ImportError:
    # Fallback/Mock if running in isolation during refactor (though shouldn't happen in prod)
    def _render_single_image_overlay_task_worker(args): return None

class EntitaSenzaTestoHandler(BaseEntityHandler):
    # This handler processes ALL entities in a batch, not one by one via "process"
    def process(self, segment: Dict[str, Any], segment_idx: int) -> bool:
        return False # Not used for this handler

    def process_batch(self, entita_senza_testo_data: Dict[str, Any], max_workers: int = 4):
        ctx = self.context
        ctx.status_callback(f"{ctx.main_label} [EntitaBatch] 🖼️ Processing Entita_Senza_Testo...", False)
        
        if not entita_senza_testo_data:
             ctx.status_callback(f"{ctx.main_label} [EntitaBatch] No data, skipping.", False)
             return

        tasks_args = []
        task_idx = 0
        
        entita_min_seconds = float(ctx.config_settings.get("entita_min_seconds", 2.5) or 2.5)
        remotion_cached = None # We might need to access this if we want to support Remotion for images (legacy? code says "overlay_engine" passed)

        render_params = {
            "fps": ctx.base_fps, 
            "temp_dir": ctx.temp_dir,
            "background_clip_path": ctx.background_video_for_img_overlays_path,
            "status_callback": lambda m, e=False: ctx.status_callback(f"{ctx.main_label} [EntWorker] {m}", e),
            "sound_effects_list": [], # Default to empty or pass from config if needed
            "overlay_engine": ctx.overlay_engine,
            "remotion_project_path": None, # Passed if needed
            "width": ctx.base_w,
            "height": ctx.base_h,
        }

        # Prepare Tasks
        for entity_name, data in entita_senza_testo_data.items():
            links = self._normalize_links(data.get("Link immagine", []))
            timestamps = data.get("Timestamps", [])
            if not links or not timestamps: continue

            for occ in timestamps:
                s = float(occ.get("timestamp_start", -1))
                e = float(occ.get("timestamp_end", -1))
                if s < 0 or e <= s: continue

                start_v = map_audio_to_video(ctx, s)
                if start_v > ctx.video_duration_for_overlays_limit: continue

                max_dur = ctx.video_duration_for_overlays_limit - start_v
                dur_v = min(e - s, max_dur)
                
                if dur_v < entita_min_seconds: dur_v = min(entita_min_seconds, max_dur)
                if dur_v < 0.1: continue

                if ctx.entity_stack_segments and overlaps_entity_stack(start_v, start_v + dur_v, ctx.entity_stack_segments):
                    continue

                task_data = {
                    "entity_name": entity_name,
                    "image_urls": links,
                    "start_v": start_v, # Used for context
                    "dur_v": dur_v,     # Used by worker
                    "effect_params": {
                        "initial_scale": ctx.config_settings.get("entita_initial_scale", 0.4),
                        "zoom_factor": ctx.config_settings.get("entita_zoom_factor", 1.5),
                    }
                }
                tasks_args.append((task_idx, task_data, render_params))
                task_idx += 1

        # Execute Tasks
        if not tasks_args:
             return

        ctx.status_callback(f"{ctx.main_label} [EntitaBatch] Submitting {len(tasks_args)} tasks...", False)
        count = 0
        errors = 0
        
        with concurrent.futures.ThreadPoolExecutor(max_workers=max_workers) as executor:
            futures = {executor.submit(_render_single_image_overlay_task_worker, args): args[1] for args in tasks_args}
            
            for future in as_completed(futures):
                task_data = futures[future]
                orig_start = task_data["start_v"]
                orig_dur = task_data["dur_v"]
                
                try:
                    result = future.result()
                    # Expected: (out_mp4, _, _, downloaded_img)
                    if result and len(result) == 4:
                        out_mp4, _, _, dl_img = result
                        ctx.all_rendered_overlay_files_for_ffmpeg_merge.append((out_mp4, orig_start, orig_dur))
                        ctx.temp_files_to_clean_overlays.append(out_mp4)
                        if dl_img: ctx.temp_files_to_clean_overlays.append(dl_img)
                        count += 1
                        ctx.status_callback(f"{ctx.main_label} [EntitaBatch] ✅ Added '{task_data['entity_name']}'", False)
                    else:
                        errors += 1
                except Exception as e:
                    ctx.status_callback(f"{ctx.main_label} [EntitaBatch] ❌ Error: {e}", True)
                    errors += 1
        
        ctx.status_callback(f"{ctx.main_label} [EntitaBatch] Completed. {count} generated, {errors} errors.", False)

    def _normalize_links(self, raw) -> List[str]:
        # Reuse logic from main
        links = []
        items = raw if isinstance(raw, list) else ([raw] if raw else [])
        for item in items:
            if isinstance(item, str) and item.strip(): links.append(item.strip())
            elif isinstance(item, dict):
                 for k in ["image_url", "url", "link", "src", "path"]:
                     v = item.get(k)
                     if v: links.append(v.strip()); break
        return links
