from __future__ import annotations

import gc
import os
import random
import shutil
import uuid
from pathlib import Path
from typing import Any, Dict, List, Optional, Tuple


def apply_stock_remotion_tail(
    *,
    stock_segment_results: Dict[int, Tuple[str, float]],
    stock_clips_sources: Optional[List[str]] = None,
    stock_clips_timestamps: Optional[List[Dict[str, Any]]] = None,
    config_settings: Optional[Dict[str, Any]] = None,
    overlay_engine: str = "remotion",
    base_w: int = 1920,
    base_h: int = 1080,
    base_fps: float = 30.0,
    temp_dir: str = "/tmp",
    seed: int = 0,
    remotion_project_path_cached: Optional[str] = None,
    status_callback: Optional[Any] = None,
    max_render_seconds: Optional[float] = None,
    public_namespace: Optional[str] = None,
) -> Dict[int, Tuple[str, float]]:
    """
    Applica animazione Remotion ai primi N secondi di ogni N-esimo segmento stock.
    - Ogni stock_remotion_every_n segmenti (es. ogni 4°): i primi stock_remotion_tail_seconds (es. 15s)
      del segmento vengono renderizzati con Remotion (StyleStock); il resto del segmento resta ffmpeg
      e viene concatenato dopo. Poi il mux con voiceover usa il segmento completo come ora.
    """
    def _log(msg: str, is_error: bool = False) -> None:
        if status_callback:
            try:
                status_callback(msg, is_error)
            except Exception:
                pass

    # Normalizza seed per evitare NoneType + int
    try:
        seed = int(seed or 0)
    except Exception:
        seed = 0

    enable_stock_remotion_tail = bool((config_settings or {}).get("enable_stock_remotion_tail", True))
    if not enable_stock_remotion_tail or not stock_segment_results:
        return stock_segment_results

    # Stock animation is independent of overlay_engine (which controls text/entity overlays).
    # Apply StyleStock animations to stock segments whenever Remotion is available.
    try:
        from .remotion_renderer import render_remotion_animation, find_remotion_project
    except Exception:
        try:
            from modules.video.remotion_renderer import render_remotion_animation, find_remotion_project
        except Exception:
            render_remotion_animation = None
            find_remotion_project = None

    if not render_remotion_animation or not find_remotion_project:
        _log("[StockRemotion] ⚠️ Remotion renderer non disponibile, salto animazioni stock", True)
        return stock_segment_results

    try:
        from .video_ffmpeg import trim_video_reencode, concatenate_videos_ffmpeg_safe
    except Exception:
        from modules.video.video_ffmpeg import trim_video_reencode, concatenate_videos_ffmpeg_safe

    stock_remotion_every_n = int((config_settings or {}).get("stock_remotion_every_n", 4) or 4)
    stock_remotion_head_seconds = float((config_settings or {}).get("stock_remotion_tail_seconds", 15.0) or 15.0)  # primi N secondi animati (config name kept)
    stock_remotion_style_ids = (config_settings or {}).get("stock_remotion_style_ids") or [
        "Stock-MotionFrame",
        "Stock-TripleSplit",
        "Stock-SplitWide",
        "Stock-StackReveal",
        "Stock-InfinitePan",
        "Stock-OrbitCamera",
        "Stock-FocusSwap",
        "Stock-FreeformFloating",
        "Stock-SlideLock",
    ]

    _log(f"[StockRemotion] ✅ Attivo | ogni {stock_remotion_every_n}° stock, primi {stock_remotion_head_seconds:.2f}s Remotion, resto ffmpeg | stili={len(stock_remotion_style_ids)}", False)

    # Costruisci pool globale stock
    global_stock_pool: List[str] = []
    try:
        for p in (stock_clips_sources or []):
            if p and os.path.isfile(p):
                global_stock_pool.append(p)
        if stock_clips_timestamps and isinstance(stock_clips_timestamps, list):
            for ts in stock_clips_timestamps:
                if not isinstance(ts, dict):
                    continue
                raw_paths = ts.get("stock_paths") or []
                items = raw_paths if isinstance(raw_paths, (list, tuple, set)) else [raw_paths]
                for item in items:
                    if item is None:
                        continue
                    try:
                        if hasattr(item, "name"):
                            item = getattr(item, "name")
                    except Exception:
                        pass
                    s = str(item).strip()
                    if s and os.path.isfile(s):
                        global_stock_pool.append(s)
    except Exception:
        pass

    # Dedup per basename
    dedup_seen: set[str] = set()
    dedup_pool: List[str] = []
    for p in global_stock_pool:
        if not p:
            continue
        bn = os.path.basename(p) or p
        if bn in dedup_seen:
            continue
        dedup_seen.add(bn)
        dedup_pool.append(p)
    global_stock_pool = dedup_pool

    remotion_project_path = remotion_project_path_cached or find_remotion_project()
    if not remotion_project_path:
        _log("[StockRemotion] ⚠️ Progetto Remotion non trovato, salto animazioni stock", True)
        return stock_segment_results

    try:
        remotion_project_path = os.path.realpath(remotion_project_path)
    except Exception:
        pass

    public_dir = os.path.join(remotion_project_path, "public")
    os.makedirs(public_dir, exist_ok=True)
    _log(f"[StockRemotion] 📦 Pool stock (fallback): {len(global_stock_pool)} clip | public={public_dir}", False)
    _log(f"[StockRemotion] 📁 Remotion project: {remotion_project_path}", False)

    style_clip_counts = {
        "Stock-MotionFrame": 1,
        "Stock-SplitWide": 2,
        "Stock-StackReveal": 2,
        "Stock-FocusSwap": 2,
        "Stock-TripleSplit": 3,
        "Stock-InfinitePan": 3,
        "Stock-OrbitCamera": 3,
        "Stock-FreeformFloating": 3,
        "Stock-SlideLock": 3,
    }

    if not public_namespace:
        public_namespace = f"job_{uuid.uuid4().hex[:8]}"

    created_public_files: List[str] = []

    total_segments = len(stock_segment_results)
    _log(f"[StockRemotion] 🎯 Segmenti stock totali: {total_segments}", False)

    for idx in sorted(stock_segment_results.keys()):
        one_based_idx = idx + 1
        if stock_remotion_every_n <= 0 or (one_based_idx % stock_remotion_every_n) != 0:
            continue

        stock_data = stock_segment_results.get(idx)
        if not stock_data:
            _log(f"[StockRemotion] ⚠️ Dati mancanti per segmento #{one_based_idx}", True)
            continue
        stock_path, stock_dur = stock_data
        if not stock_path or stock_dur <= 0:
            _log(f"[StockRemotion] ⚠️ Segmento #{one_based_idx} invalido (path/dur)", True)
            continue

        head_seconds = min(stock_dur, stock_remotion_head_seconds)
        if max_render_seconds and max_render_seconds > 0:
            head_seconds = min(head_seconds, max_render_seconds)
        if head_seconds <= 0.05:
            _log(f"[StockRemotion] ⏭️ Head troppo corto per segmento #{one_based_idx}", False)
            continue

        # Primi head_seconds del segmento (stock_path) → input per Remotion
        segment_head_path = os.path.join(temp_dir, f"stock_head_{one_based_idx}_{uuid.uuid4().hex[:6]}.mp4")
        ok_head_trim = trim_video_reencode(
            input_path=stock_path,
            output_path=segment_head_path,
            start_time=0.0,
            duration=head_seconds,
            base_w=base_w,
            base_h=base_h,
            base_fps=base_fps,
            status_callback=lambda m: _log(f"[StockRemotion] {m}", False),
        )
        if not ok_head_trim or not (os.path.exists(segment_head_path) and os.path.getsize(segment_head_path) > 1000):
            _log(f"[StockRemotion] ⚠️ Trim primi {head_seconds:.2f}s fallito per segmento #{one_based_idx}", True)
            continue

        rng = random.Random(seed + idx)
        style_id = rng.choice(stock_remotion_style_ids)
        clip_count = style_clip_counts.get(style_id, 1)

        # Usa lo stesso clip (primi 15s del segmento) per tutti gli slot dello stile
        props: Dict[str, Any] = {}
        for i in range(clip_count):
            ext = Path(segment_head_path).suffix or ".mp4"
            pub_name = f"stock_anim_{public_namespace}_{one_based_idx}_{i}_{uuid.uuid4().hex[:6]}{ext}"
            dest_path = os.path.join(public_dir, pub_name)
            shutil.copy2(segment_head_path, dest_path)
            created_public_files.append(dest_path)
            if clip_count == 1:
                props["videoSrc"] = f"/{pub_name}"
            else:
                props[f"video{i+1}Src"] = f"/{pub_name}"

        if style_id == "Stock-TripleSplit":
            props["styleMode"] = rng.choice(["full", "inset"])

        out_anim = os.path.join(temp_dir, f"stock_remotion_{one_based_idx}_{uuid.uuid4().hex[:6]}.mp4")
        duration_in_frames = max(1, int(round(head_seconds * float(base_fps))))

        _log(f"[StockRemotion] 🎞️ Segmento #{one_based_idx}: stile={style_id} primi {head_seconds:.2f}s Remotion, resto ffmpeg | dur segmento={stock_dur:.2f}s", False)

        _prev_bundle = os.environ.get("VELOX_REMOTION_BUNDLE_CACHE")
        _prev_clean = os.environ.get("VELOX_REMOTION_CLEAN_BUNDLES")
        os.environ["VELOX_REMOTION_BUNDLE_CACHE"] = "0"
        os.environ["VELOX_REMOTION_CLEAN_BUNDLES"] = "1"
        _log("[StockRemotion] 🧹 Bundle cache OFF per asset dinamici", False)
        try:
            clip = render_remotion_animation(
                animation_id=style_id,
                output_path=out_anim,
                fps=int(base_fps),
                width=base_w,
                height=base_h,
                duration_in_frames=duration_in_frames,
                props=props,
                remotion_project_path=remotion_project_path,
                status_callback=lambda m, e=False: _log(f"[StockRemotion] {m}", e),
            )
        finally:
            if _prev_bundle is None:
                os.environ.pop("VELOX_REMOTION_BUNDLE_CACHE", None)
            else:
                os.environ["VELOX_REMOTION_BUNDLE_CACHE"] = _prev_bundle
            if _prev_clean is None:
                os.environ.pop("VELOX_REMOTION_CLEAN_BUNDLES", None)
            else:
                os.environ["VELOX_REMOTION_CLEAN_BUNDLES"] = _prev_clean
        try:
            if clip:
                clip.close()
        except Exception:
            pass

        if not (os.path.exists(out_anim) and os.path.getsize(out_anim) > 1000):
            _log(f"[StockRemotion] ⚠️ Render fallito per segmento #{one_based_idx}", True)
            if os.path.exists(segment_head_path):
                try:
                    os.remove(segment_head_path)
                except Exception:
                    pass
            continue

        # Concat: [primi 15s Remotion] + [resto segmento ffmpeg]
        rest_path = None
        if stock_dur > head_seconds:
            rest_path = os.path.join(temp_dir, f"stock_rest_{one_based_idx}_{uuid.uuid4().hex[:6]}.mp4")
            ok_rest = trim_video_reencode(
                input_path=stock_path,
                output_path=rest_path,
                start_time=head_seconds,
                duration=max(0.05, stock_dur - head_seconds),
                base_w=base_w,
                base_h=base_h,
                base_fps=base_fps,
                status_callback=lambda m: _log(f"[StockRemotion] {m}", False),
            )
            if not ok_rest or not (os.path.exists(rest_path) and os.path.getsize(rest_path) > 1000):
                _log(f"[StockRemotion] ⚠️ Trim resto fallito per segmento #{one_based_idx}, uso solo Remotion", True)
                rest_path = None

        if rest_path:
            concat_out = os.path.join(temp_dir, f"stock_headmix_{one_based_idx}_{uuid.uuid4().hex[:6]}.mp4")
            ok_concat = concatenate_videos_ffmpeg_safe(
                [out_anim, rest_path],
                concat_out,
                base_w=base_w,
                base_h=base_h,
                base_fps=base_fps,
                status_callback=lambda m: _log(f"[StockRemotion] {m}", False),
            )
            if ok_concat and os.path.exists(concat_out) and os.path.getsize(concat_out) > 1000:
                stock_segment_results[idx] = (concat_out, stock_dur)
            else:
                _log(f"[StockRemotion] ⚠️ Concat fallito per segmento #{one_based_idx}", True)
        else:
            stock_segment_results[idx] = (out_anim, stock_dur)

        if os.path.exists(segment_head_path):
            try:
                os.remove(segment_head_path)
            except Exception:
                pass

        gc.collect()

    # Cleanup public assets created for this run
    for p in created_public_files:
        try:
            if os.path.exists(p):
                os.remove(p)
        except Exception:
            pass

    return stock_segment_results
