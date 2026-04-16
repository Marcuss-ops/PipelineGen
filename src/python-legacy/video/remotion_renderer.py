"""
Remotion Renderer - Wrapper Python per renderizzare animazioni Remotion
"""

import os
import sys
import subprocess
import json
import logging
import tempfile
import uuid
import hashlib
from pathlib import Path
from typing import Optional, Dict, Any, List, Callable
import random
import re
import time
from moviepy import VideoFileClip

logger = logging.getLogger(__name__)

_RE_RENDERED = re.compile(r"Rendered\s+(\d+)\/(\d+)")
_RE_ENCODED = re.compile(r"Encoded\s+(\d+)\/(\d+)")

_BACKGROUND_COMPAT_CACHE: Dict[str, str] = {}

def _asset_hash_for_path(path: str) -> str:
    try:
        ap = os.path.abspath(path)
        st = os.stat(ap)
        payload = f"{ap}|{st.st_mtime_ns}|{st.st_size}"
        return hashlib.md5(payload.encode("utf-8")).hexdigest()[:12]
    except Exception:
        try:
            return hashlib.md5(str(path).encode("utf-8")).hexdigest()[:12]
        except Exception:
            return uuid.uuid4().hex[:12]

def _asset_hash_for_url(url: str) -> str:
    try:
        return hashlib.md5(url.encode("utf-8")).hexdigest()[:12]
    except Exception:
        return uuid.uuid4().hex[:12]

def _guess_image_ext(src: str) -> str:
    try:
        ext = os.path.splitext(src)[1].lower()
    except Exception:
        ext = ""
    if ext in (".jpg", ".jpeg", ".png", ".webp"):
        return ext
    return ".jpg"

def _deterministic_public_name(prefix: str, hash_hex: str, ext: str) -> str:
    return f"{prefix}_{hash_hex}{ext}"

def _find_remotion_entry_point(remotion_project_path: str) -> Optional[str]:
    """Trova entry point Remotion (src/index.tsx|ts|jsx|js oppure index.* in root)."""
    candidates = [
        os.path.join(remotion_project_path, "src", "index.tsx"),
        os.path.join(remotion_project_path, "src", "index.ts"),
        os.path.join(remotion_project_path, "src", "index.jsx"),
        os.path.join(remotion_project_path, "src", "index.js"),
        os.path.join(remotion_project_path, "index.tsx"),
        os.path.join(remotion_project_path, "index.ts"),
        os.path.join(remotion_project_path, "index.jsx"),
        os.path.join(remotion_project_path, "index.js"),
    ]
    for path in candidates:
        if os.path.isfile(path):
            return path
    return None

def _normalize_entitystack_image(
    src_str: str,
    public_dir: str,
    status_callback: Optional[Any] = None,
) -> str:
    if not src_str:
        return ""
    src_str = str(src_str).strip()
    if not src_str or src_str.lower() in ("none", "null"):
        return ""

    # Already a public-path style
    if src_str.startswith("/"):
        img_path = os.path.join(public_dir, src_str.lstrip("/"))
        if status_callback:
            status_callback(f"[EntityStack] 🧪 imageUrl file: {_file_status(img_path)}", False)
        if not os.path.exists(img_path):
            logger.warning("[EntityStack] File pubblico mancante: %s", src_str)
            return ""
        return src_str

    os.makedirs(public_dir, exist_ok=True)
    if src_str.startswith("http://") or src_str.startswith("https://"):
        try:
            from modules.utils.utils import download_image
        except ImportError:
            try:
                from ..utils.utils import download_image  # type: ignore
            except Exception:
                from utils import download_image  # type: ignore

        img_hash = _asset_hash_for_url(src_str)
        ext = _guess_image_ext(src_str)
        public_name = _deterministic_public_name("entitystack_img", img_hash, ext)
        public_path = os.path.join(public_dir, public_name)
        if os.path.exists(public_path):
            return f"/{public_name}"
        downloaded = download_image([src_str], public_path, status_callback)
        if not downloaded or not os.path.exists(downloaded):
            logger.warning("[EntityStack] Download immagine fallito, salto: %s", src_str)
            return ""
        return f"/{public_name}"

    # Local filesystem path
    if os.path.exists(src_str):
        import shutil
        img_hash = _asset_hash_for_path(src_str)
        ext = _guess_image_ext(src_str)
        public_name = _deterministic_public_name("entitystack_img", img_hash, ext)
        public_path = os.path.join(public_dir, public_name)
        if os.path.exists(public_path):
            return f"/{public_name}"
        shutil.copy2(src_str, public_path)
        return f"/{public_name}"

    logger.warning("[EntityStack] Path immagine non esiste, salto: %s", src_str)
    return ""

def prewarm_entitystack_assets(
    image_sources: List[str],
    background_paths: List[str],
    remotion_project_path: str,
    fps: int,
    width: int,
    height: int,
    status_callback: Optional[Any] = None,
    light_mode: bool = False,
) -> None:
    public_dir = os.path.join(remotion_project_path, "public")
    os.makedirs(public_dir, exist_ok=True)

    if not light_mode:
        for bg in sorted(set([p for p in background_paths if p])):
            bg = _normalize_path_input(bg)
            if not bg or not os.path.exists(bg):
                continue
            try:
                compat_bg = _ensure_background_compatibility(
                    bg,
                    width=int(width),
                    height=int(height),
                    fps=int(fps),
                    public_dir=public_dir,
                    status_callback=status_callback,
                )
                bg_hash = _asset_hash_for_path(compat_bg)
                bg_name = _deterministic_public_name("entitystack_bg", bg_hash, ".mp4")
                bg_dest = os.path.join(public_dir, bg_name)
                if not os.path.exists(bg_dest):
                    import shutil
                    shutil.copy2(compat_bg, bg_dest)
                if status_callback:
                    status_callback(f"[EntityStack] 🧊 Prewarm background: /{bg_name}", False)
            except Exception as e:
                logger.warning("[EntityStack] Prewarm background fallito: %s", e)

    for src in sorted(set([s for s in image_sources if s])):
        try:
            normalized = _normalize_entitystack_image(src, public_dir, status_callback)
            if normalized and status_callback:
                status_callback(f"[EntityStack] 🧊 Prewarm image: {normalized}", False)
        except Exception as e:
            logger.warning("[EntityStack] Prewarm image fallito: %s", e)

def _normalize_entitystack_entities(
    entities: List[Dict[str, Any]],
    public_dir: str,
    status_callback: Optional[Any] = None,
) -> List[Dict[str, Any]]:
    normalized_entities: List[Dict[str, Any]] = []
    for e in entities:
        try:
            copy_e = dict(e)
        except Exception:
            copy_e = e
        if str(copy_e.get("type", "")).upper() == "IMAGE":
            src = copy_e.get("content")
            src_str = str(src).strip() if src is not None else ""
            if not src_str or src_str.lower() in ("none", "null"):
                copy_e["content"] = ""
            else:
                try:
                    normalized = _normalize_entitystack_image(src_str, public_dir, status_callback)
                    copy_e["content"] = normalized or ""
                except Exception:
                    copy_e["content"] = ""
        normalized_entities.append(copy_e)
    return normalized_entities

def _history_snapshot_hash(payload: Dict[str, Any]) -> str:
    try:
        raw = json.dumps(payload, sort_keys=True, ensure_ascii=False)
        return hashlib.md5(raw.encode("utf-8")).hexdigest()[:12]
    except Exception:
        return uuid.uuid4().hex[:12]

def _find_remotion_bin(remotion_project_path: str) -> Optional[str]:
    remotion_bin = os.path.join(remotion_project_path, "node_modules", ".bin", "remotion")
    if os.path.isfile(remotion_bin):
        return os.path.abspath(remotion_bin)
    return None

def _render_remotion_still(
    *,
    remotion_project_path: str,
    composition_id: str,
    output_path: str,
    props: Dict[str, Any],
    frame: int,
    fps: int,
    width: int,
    height: int,
    status_callback: Optional[Any] = None,
    disable_bundle_cache: bool = False,
) -> bool:
    remotion_bin = _find_remotion_bin(remotion_project_path)
    cmd_base = [remotion_bin] if remotion_bin else ["npx", "remotion"]
    cmd = cmd_base + [
        "still",
        "src/index.ts",
        composition_id,
        output_path,
        "--props",
        json.dumps(props),
        "--frame",
        str(int(max(0, frame))),
        "--fps",
        str(int(fps)),
        "--width",
        str(int(width)),
        "--height",
        str(int(height)),
        "--image-format",
        "png",
    ]
    remotion_binaries_dir = _get_remotion_binaries_dir(remotion_project_path)
    os.makedirs(remotion_binaries_dir, exist_ok=True)
    if _should_use_binaries_dir(remotion_binaries_dir):
        cmd += ["--binaries-directory", remotion_binaries_dir]
    if disable_bundle_cache:
        cmd.append("--bundle-cache=false")
    try:
        result = _run_with_filtered_progress(
            cmd,
            cwd=remotion_project_path,
            timeout=900,
            env=_merge_env(_build_remotion_env_overrides(remotion_binaries_dir)),
        )
        if result.returncode != 0:
            logger.error("[EntityStack] ❌ Still render error: %s", result.stderr)
            if status_callback:
                status_callback(f"[EntityStack] ❌ Still render error: {result.stderr}", True)
            return False
        return os.path.exists(output_path)
    except Exception as e:
        logger.error("[EntityStack] ❌ Still render exception: %s", e)
        if status_callback:
            status_callback(f"[EntityStack] ❌ Still render exception: {e}", True)
        return False

def render_entity_stack_history_snapshots(
    *,
    entities: List[Dict[str, Any]],
    background_path: Optional[str],
    remotion_project_path: str,
    fps: int,
    width: int,
    height: int,
    stack_layer_style: Optional[str] = None,
    light_mode: bool = False,
    status_callback: Optional[Any] = None,
) -> List[Dict[str, Any]]:
    """
    Render PNG snapshots at segment boundaries for EntityStack history layer.
    Current behaviour: full-frame still (all entities visible at that frame).
    For "only previous entity + live background behind mask": render a composition
    that outputs only the single previous entity with transparent background, so
    the live background layer in EntityStack can show through.
    """
    if not entities or len(entities) < 2:
        return []

    public_dir = os.path.join(remotion_project_path, "public")
    os.makedirs(public_dir, exist_ok=True)

    chosen_style = (stack_layer_style or "").strip().upper() or "DEFAULT"

    # Ensure background in public (deterministic) if present
    background_filename = None
    if background_path and os.path.exists(background_path) and not light_mode:
        try:
            background_path = _ensure_background_compatibility(
                background_path,
                width=int(width),
                height=int(height),
                fps=int(fps),
                public_dir=public_dir,
                status_callback=status_callback,
            )
            bg_hash = _asset_hash_for_path(background_path)
            background_filename = _deterministic_public_name("entitystack_bg", bg_hash, ".mp4")
            background_dest = os.path.join(public_dir, background_filename)
            if not os.path.exists(background_dest):
                import shutil
                shutil.copy2(background_path, background_dest)
        except Exception as e:
            logger.warning("[EntityStack] History background copy failed: %s", e)
            background_filename = None

    # Normalize images for deterministic public paths
    norm_entities = _normalize_entitystack_entities(entities, public_dir, status_callback)

    # Precompute start frames
    starts: List[int] = []
    cumulative = 0
    for e in norm_entities:
        starts.append(cumulative)
        cumulative += int(e.get("duration", 0) or 0)

    history_snapshots: List[Dict[str, Any]] = []
    for idx in range(1, len(norm_entities)):
        start_frame = starts[idx]
        snap_frame = max(0, start_frame - 1)

        # Snapshot "solo entità" con sfondo trasparente (PNG alpha) così dietro la maschera si vede il video in movimento
        entity_index = idx - 1
        payload = {
            "idx": idx,
            "frame": snap_frame,
            "style": chosen_style,
            "light": bool(light_mode),
            "background": background_filename or "",
            "entities": norm_entities[:idx],
            "entityOnly": True,
        }
        snap_hash = _history_snapshot_hash(payload)
        snap_name = _deterministic_public_name("entitystack_hist", snap_hash, ".png")
        snap_path = os.path.join(public_dir, snap_name)

        if not os.path.exists(snap_path):
            props = {
                "entities": norm_entities,
                "stackLayerStyle": chosen_style,
                "lightMode": light_mode,
                "snapshotLayerOnly": {"entityIndex": entity_index, "freezeFrame": snap_frame},
            }
            if background_filename:
                props["backgroundVideo"] = background_filename

            if status_callback:
                status_callback(
                    f"[EntityStack] 🧪 History snapshot entità-only {idx}/{len(norm_entities)-1} "
                    f"entityIndex={entity_index} @frame={snap_frame} -> /{snap_name}",
                    False,
                )

            ok = _render_remotion_still(
                remotion_project_path=remotion_project_path,
                composition_id="EntityStack",
                output_path=snap_path,
                props=props,
                frame=snap_frame,
                fps=fps,
                width=width,
                height=height,
                status_callback=status_callback,
                disable_bundle_cache=_should_disable_bundle_cache(),
            )
            if not ok:
                continue

        history_snapshots.append({"startFrame": int(start_frame), "src": f"/{snap_name}"})

    return history_snapshots


def _composite_entity_snapshots_png(
    base_path: str,
    overlay_path: str,
    output_path: str,
    width: int = 1920,
    height: int = 1080,
) -> bool:
    """Composita due PNG con alpha: overlay sopra base. Salva in output_path."""
    try:
        from PIL import Image
        base = Image.open(base_path).convert("RGBA")
        overlay = Image.open(overlay_path).convert("RGBA")
        if base.size != (width, height):
            base = base.resize((width, height), getattr(Image, "Resampling", Image).LANCZOS)
        if overlay.size != (width, height):
            overlay = overlay.resize((width, height), getattr(Image, "Resampling", Image).LANCZOS)
        base.paste(overlay, (0, 0), overlay)
        base.save(output_path, "PNG")
        return os.path.exists(output_path)
    except Exception as e:
        logger.warning("[EntityStack] Composite PNG failed: %s", e)
        return False


def render_one_entity_stack_snapshot(
    *,
    entities: List[Dict[str, Any]],
    entity_index: int,
    freeze_frame: int,
    public_dir: str,
    remotion_project_path: str,
    fps: int,
    width: int,
    height: int,
    stack_layer_style: Optional[str] = None,
    light_mode: bool = False,
    background_filename: Optional[str] = None,
    status_callback: Optional[Any] = None,
) -> Optional[str]:
    """
    Renderizza un singolo PNG "entity-only" per una entità a un dato frame.
    Usato nella catena per-entità: dopo ogni clip si salva lo snapshot dell'entità corrente
    e si compone con i precedenti per il layer "passato" della clip successiva.
    Restituisce il path del file in public_dir (da usare come src="/basename").
    """
    if not entities or entity_index < 0 or entity_index >= len(entities):
        return None
    chosen_style = (stack_layer_style or "").strip().upper() or "DEFAULT"
    payload = {
        "idx": entity_index,
        "frame": freeze_frame,
        "style": chosen_style,
        "light": bool(light_mode),
        "background": background_filename or "",
        "entities": entities,
        "entityOnly": True,
    }
    snap_hash = _history_snapshot_hash(payload)
    snap_name = _deterministic_public_name("entitystack_chain", snap_hash, ".png")
    snap_path = os.path.join(public_dir, snap_name)
    if os.path.exists(snap_path):
        return snap_path
    props = {
        "entities": entities,
        "stackLayerStyle": chosen_style,
        "lightMode": light_mode,
        "snapshotLayerOnly": {"entityIndex": entity_index, "freezeFrame": freeze_frame},
    }
    if background_filename:
        props["backgroundVideo"] = background_filename
    ok = _render_remotion_still(
        remotion_project_path=remotion_project_path,
        composition_id="EntityStack",
        output_path=snap_path,
        props=props,
        frame=freeze_frame,
        fps=fps,
        width=width,
        height=height,
        status_callback=status_callback,
        disable_bundle_cache=_should_disable_bundle_cache(),
    )
    return snap_path if ok and os.path.exists(snap_path) else None


def _render_bg_segment_with_snapshot_overlay(
    bg_video_path: str,
    start_frame: int,
    num_frames: int,
    overlay_png_path: str,
    output_path: str,
    fps: int,
    width: int,
    height: int,
    status_callback: Optional[Any] = None,
) -> Optional[str]:
    """
    Crea un video: segmento di bg_video (da start_frame, lunghezza num_frames) con overlay del PNG.
    Scala il PNG a width x height e usa format=auto per rispettare l'alpha (entità visibile dietro).
    """
    if not os.path.exists(bg_video_path) or not os.path.exists(overlay_png_path):
        return None
    start_sec = start_frame / max(1, fps)
    dur_sec = num_frames / max(1, fps)
    try:
        filter_complex = f"[1:v]scale={width}:{height}[ov];[0:v][ov]overlay=0:0:format=auto[out]"
        cmd = [
            "ffmpeg", "-y", "-hide_banner", "-loglevel", "error",
            "-ss", f"{start_sec:.6f}", "-i", bg_video_path, "-t", f"{dur_sec:.6f}",
            "-i", overlay_png_path,
            "-filter_complex", filter_complex,
            "-map", "[out]",
            "-c:v", "libx264", "-preset", "fast", "-crf", "22", "-pix_fmt", "yuv420p",
            "-r", str(fps), output_path,
        ]
        subprocess.run(cmd, check=True, capture_output=True, timeout=120)
        return output_path if os.path.exists(output_path) else None
    except Exception as e:
        logger.warning("[EntityStack-Chain] Background+overlay fallito: %s", e)
        return None


def render_entity_stack_per_entity_chain(
    *,
    entities: List[Dict[str, Any]],
    output_path: str,
    background_path: Optional[str] = None,
    fps: int = 30,
    width: int = 1920,
    height: int = 1080,
    remotion_project_path: Optional[str] = None,
    status_callback: Optional[Any] = None,
    stack_layer_style: Optional[str] = None,
    light_mode: bool = False,
    debug: bool = False,
) -> Optional[VideoFileClip]:
    """
    Render veloce: una clip per entità. Per clip i>=1 il background è il segmento video
    con lo snapshot dell'entità precedente già compositato sopra (maschera/frame dentro il bg).
    """
    import tempfile
    import shutil
    if debug:
        width, height = 960, 540
        if status_callback:
            status_callback("[EntityStack-Chain] 🐛 Debug: 960x540, qualità ridotta", False)
    if not entities or len(entities) == 0:
        return None
    if len(entities) == 1:
        if status_callback:
            status_callback("[EntityStack-Chain] 1 entità: fallback a render normale", False)
        return render_entity_stack(
            entities=entities,
            output_path=output_path,
            background_path=background_path,
            fps=fps,
            width=width,
            height=height,
            remotion_project_path=remotion_project_path,
            status_callback=status_callback,
            stack_layer_style=stack_layer_style,
            light_mode=light_mode,
            history_snapshots=None,
        )
    if not remotion_project_path:
        remotion_project_path = find_remotion_project()
    if not remotion_project_path:
        logger.error("[EntityStack-Chain] Progetto Remotion non trovato")
        return None
    public_dir = os.path.join(remotion_project_path, "public")
    os.makedirs(public_dir, exist_ok=True)
    normalized = _normalize_entitystack_entities(entities, public_dir, status_callback)
    chosen_style = (stack_layer_style or "").strip().upper() or "DEFAULT"
    durations = [int(e.get("duration", 0) or 0) for e in normalized]
    if any(d <= 0 for d in durations):
        durations = [max(1, d) for d in durations]
    total_frames_chain = sum(durations)
    background_filename = None
    full_bg_segment_path: Optional[str] = None
    if background_path and os.path.exists(background_path) and not light_mode:
        try:
            segment_path = _prerender_background_segment(
                background_path,
                total_frames=total_frames_chain,
                fps=fps,
                width=width,
                height=height,
                public_dir=public_dir,
                status_callback=status_callback,
                debug=debug,
            )
            if segment_path:
                full_bg_segment_path = segment_path
                background_path = segment_path
            background_path = _ensure_background_compatibility(
                background_path,
                width=width,
                height=height,
                fps=fps,
                public_dir=public_dir,
                status_callback=status_callback,
            )
            bg_hash = _asset_hash_for_path(background_path)
            background_filename = _deterministic_public_name("entitystack_bg", bg_hash, ".mp4")
            dest = os.path.join(public_dir, background_filename)
            if not os.path.exists(dest):
                import shutil as sh
                sh.copy2(background_path, dest)
        except Exception as e:
            logger.warning("[EntityStack-Chain] Background copy failed: %s", e)
    temp_dir = tempfile.mkdtemp(prefix="entitystack_chain_")
    clip_paths: List[str] = []
    try:
        combined_snap_path: Optional[str] = None
        for i in range(len(normalized)):
            ent = normalized[i]
            di = durations[i]
            if status_callback:
                status_callback(
                    f"[EntityStack-Chain] 🎬 Entità {i + 1}/{len(normalized)}: {ent.get('type', '')} | {str(ent.get('content', ''))[:30]}...",
                    False,
                )
            clip_bg_path = background_path
            history_list: List[Dict[str, Any]] = []
            if combined_snap_path and os.path.exists(combined_snap_path):
                snap_name = os.path.basename(combined_snap_path)
                history_list = [{"startFrame": 0, "src": f"/{snap_name}"}]
            clip_out = os.path.join(temp_dir, f"clip_{i:02d}_{uuid.uuid4().hex[:6]}.mp4")
            clip = render_entity_stack(
                entities=[ent],
                output_path=clip_out,
                background_path=clip_bg_path,
                fps=fps,
                width=width,
                height=height,
                remotion_project_path=remotion_project_path,
                status_callback=lambda m, err=False: status_callback(
                    f"[EntityStack-Chain] [Ent{i + 1}] {m}", err
                ) if status_callback else None,
                stack_layer_style=stack_layer_style,
                light_mode=light_mode,
                history_snapshots=history_list if history_list else None,
            )
            if clip and hasattr(clip, "close"):
                try:
                    clip.close()
                except Exception:
                    pass
            if not os.path.exists(clip_out) or os.path.getsize(clip_out) < 1000:
                logger.error("[EntityStack-Chain] Render fallito per entità %s", i + 1)
                continue
            clip_paths.append(clip_out)
            if i < len(normalized) - 1:
                cumul = sum(durations[: i + 1])
                freeze = max(0, cumul - 1)
                snap_ent_path = render_one_entity_stack_snapshot(
                    entities=normalized[: i + 1],
                    entity_index=i,
                    freeze_frame=freeze,
                    public_dir=public_dir,
                    remotion_project_path=remotion_project_path,
                    fps=fps,
                    width=width,
                    height=height,
                    stack_layer_style=chosen_style,
                    light_mode=light_mode,
                    background_filename=background_filename,
                    status_callback=status_callback,
                )
                if snap_ent_path:
                    if combined_snap_path is None:
                        combined_snap_path = snap_ent_path
                    else:
                        combined_name = _deterministic_public_name(
                            "entitystack_chain_comb", uuid.uuid4().hex[:12], ".png"
                        )
                        combined_new = os.path.join(public_dir, combined_name)
                        if _composite_entity_snapshots_png(
                            combined_snap_path, snap_ent_path, combined_new, width, height
                        ):
                            combined_snap_path = combined_new
                    if status_callback:
                        status_callback(f"[EntityStack-Chain] ✅ Snapshot entità {i + 1} salvato", False)
        if not clip_paths:
            return None
        if status_callback:
            status_callback(f"[EntityStack-Chain] 🔗 Concatenazione {len(clip_paths)} clip...", False)
        concat_list = os.path.join(temp_dir, "concat.txt")
        with open(concat_list, "w") as f:
            for p in clip_paths:
                f.write(f"file '{os.path.abspath(p)}'\n")
        concat_cmd = [
            "ffmpeg", "-y", "-f", "concat", "-safe", "0", "-i", concat_list,
            "-c", "copy", output_path,
        ]
        subprocess.run(concat_cmd, check=True, capture_output=True)
        if os.path.exists(output_path) and os.path.getsize(output_path) > 1000:
            try:
                return VideoFileClip(output_path)
            except Exception as e:
                logger.warning("[EntityStack-Chain] VideoFileClip load failed: %s", e)
                return None
        return None
    finally:
        try:
            shutil.rmtree(temp_dir, ignore_errors=True)
        except Exception:
            pass
    return None


def _parse_ffprobe_fraction(value: str) -> Optional[float]:
    try:
        if not value or value == "0/0":
            return None
        if "/" in value:
            num, den = value.split("/", 1)
            return float(num) / float(den)
        return float(value)
    except Exception:
        return None

def _normalize_path_input(value: Any) -> Optional[str]:
    """Normalize common path payloads (gradio file, dict, list, 'path||meta')."""
    if value is None:
        return None
    if isinstance(value, (list, tuple)):
        if not value:
            return None
        value = value[0]
    if isinstance(value, dict):
        for key in ("name", "path", "filepath", "file"):
            v = value.get(key)
            if v:
                value = v
                break
    try:
        if hasattr(value, "name"):
            value = getattr(value, "name")
    except Exception:
        pass
    try:
        s = str(value).strip()
    except Exception:
        return None
    if not s:
        return None
    if "||" in s:
        s = s.split("||", 1)[0].strip()
    return s or None

def _probe_video_props(path: str) -> Dict[str, Any]:
    try:
        cmd = [
            "ffprobe",
            "-v",
            "quiet",
            "-print_format",
            "json",
            "-show_streams",
            "-select_streams",
            "v:0",
            path,
        ]
        result = subprocess.run(cmd, capture_output=True, text=True, timeout=20)
        if result.returncode != 0:
            return {}
        data = json.loads(result.stdout or "{}")
        streams = data.get("streams") or []
        return streams[0] if streams else {}
    except Exception:
        return {}

def _prerender_background_segment(
    background_path: str,
    total_frames: int,
    fps: int,
    width: int,
    height: int,
    public_dir: str,
    status_callback: Optional[Callable[[str, bool], None]] = None,
    debug: bool = False,
) -> Optional[str]:
    """
    Pre-renderizza il background alla durata esatta (total_frames) per la chain.
    Loop del source se necessario, trim a duration_sec. Output già compatibile (scale, fps, yuv420p).
    Così Remotion decodifica un file più corto e veloce.
    """
    if not background_path or not os.path.exists(background_path) or total_frames <= 0:
        return None
    duration_sec = total_frames / max(1, fps)
    try:
        probe = subprocess.run(
            [
                "ffprobe", "-v", "error", "-show_entries", "format=duration",
                "-of", "default=noprint_wrappers=1:nokey=1", background_path,
            ],
            capture_output=True,
            text=True,
            timeout=10,
        )
        src_dur = float(probe.stdout.strip() or "0")
    except Exception:
        src_dur = 0
    need_loop = src_dur > 0 and duration_sec > src_dur
    hash_payload = f"{os.path.abspath(background_path)}|{total_frames}|{fps}|{width}|{height}"
    seg_hash = hashlib.md5(hash_payload.encode()).hexdigest()[:12]
    out_name = _deterministic_public_name("entitystack_bg_chain", seg_hash, ".mp4")
    out_path = os.path.join(public_dir, out_name)
    if os.path.exists(out_path) and os.path.getsize(out_path) > 1000:
        return out_path
    if status_callback:
        status_callback("[EntityStack-Chain] 🎞️ Pre-render background per durata esatta (velocizza Remotion)...", False)
    preset = "ultrafast" if debug else "fast"
    crf = "28" if debug else "22"
    cmd = [
        "ffmpeg", "-y", "-hide_banner", "-loglevel", "error",
        "-i", background_path,
        "-vf", f"scale={width}:{height}:force_original_aspect_ratio=increase,setsar=1,fps={fps}",
        "-c:v", "libx264", "-pix_fmt", "yuv420p", "-preset", preset, "-crf", crf,
        "-an", "-t", f"{duration_sec:.6f}", out_path,
    ]
    if need_loop:
        cmd = [
            "ffmpeg", "-y", "-hide_banner", "-loglevel", "error",
            "-stream_loop", "-1", "-i", background_path,
            "-vf", f"scale={width}:{height}:force_original_aspect_ratio=increase,setsar=1,fps={fps}",
            "-c:v", "libx264", "-pix_fmt", "yuv420p", "-preset", preset, "-crf", crf,
            "-an", "-t", f"{duration_sec:.6f}", out_path,
        ]
    try:
        subprocess.run(cmd, check=True, capture_output=True, timeout=300)
        return out_path if os.path.exists(out_path) else None
    except Exception as e:
        logger.warning("[EntityStack-Chain] Pre-render background fallito: %s", e)
        return None


def _ensure_background_compatibility(
    background_path: str,
    *,
    width: int,
    height: int,
    fps: int,
    public_dir: str,
    status_callback: Optional[Callable[[str, bool], None]] = None,
) -> str:
    if os.environ.get("VELOX_BG_COMPAT_CHECK", "1") != "1":
        return background_path

    if background_path in _BACKGROUND_COMPAT_CACHE:
        return _BACKGROUND_COMPAT_CACHE[background_path]

    if not os.path.exists(background_path):
        return background_path

    props = _probe_video_props(background_path)
    pix_fmt = str(props.get("pix_fmt") or "")
    w = int(props.get("width") or 0) if str(props.get("width") or "").isdigit() else 0
    h = int(props.get("height") or 0) if str(props.get("height") or "").isdigit() else 0
    fps_val = _parse_ffprobe_fraction(str(props.get("avg_frame_rate") or props.get("r_frame_rate") or ""))

    # If ffprobe can't read a video stream, skip re-encode and use original.
    if w <= 0 or h <= 0:
        if status_callback:
            status_callback("[EntityStack] ⚠️ Background senza stream video leggibile, uso originale", True)
        _BACKGROUND_COMPAT_CACHE[background_path] = background_path
        return background_path

    needs_reencode = False
    if w and h and (w != int(width) or h != int(height)):
        needs_reencode = True
    if fps_val and abs(float(fps_val) - float(fps)) > 0.5:
        needs_reencode = True
    if pix_fmt and pix_fmt not in ("yuv420p", "yuvj420p"):
        needs_reencode = True

    if not needs_reencode:
        if status_callback:
            status_callback("[EntityStack] ✅ Background compatibile per animazioni 3D", False)
        _BACKGROUND_COMPAT_CACHE[background_path] = background_path
        return background_path

    def _run_ffmpeg(cmd: List[str]) -> Dict[str, Any]:
        try:
            result = subprocess.run(cmd, capture_output=True, text=True, timeout=120)
            return {"ok": result.returncode == 0, "stderr": result.stderr or "", "stdout": result.stdout or ""}
        except Exception as e:
            return {"ok": False, "stderr": str(e), "stdout": ""}

    os.makedirs(public_dir, exist_ok=True)

    # Try a cover-style re-encode first.
    out_name = f"entitystack_bg_compat_{uuid.uuid4().hex[:8]}.mp4"
    out_path = os.path.join(public_dir, out_name)
    cmd_cover = [
        "ffmpeg",
        "-hide_banner",
        "-loglevel",
        "error",
        "-y",
        "-i",
        background_path,
        "-vf",
        f"scale={int(width)}:{int(height)}:force_original_aspect_ratio=increase,setsar=1,fps={int(fps)}",
        "-c:v",
        "libx264",
        "-pix_fmt",
        "yuv420p",
        "-preset",
        "medium",
        "-crf",
        "20",
        "-an",
        out_path,
    ]
    res = _run_ffmpeg(cmd_cover)
    if res["ok"]:
        if status_callback:
            status_callback("[EntityStack] ♻️ Background ricodificato per compatibilità 3D", False)
        _BACKGROUND_COMPAT_CACHE[background_path] = out_path
        return out_path
    if status_callback:
        err = res.get("stderr") or ""
        err = err.strip().replace("\n", " ")
        if len(err) > 300:
            err = err[:300] + "…"
        status_callback(f"[EntityStack] ⚠️ Ricodifica fallita (cover): {err or 'errore sconosciuto'}", True)

    # Retry with a safer scale+pad (no crop) if cover failed.
    out_name_2 = f"entitystack_bg_compat_{uuid.uuid4().hex[:8]}.mp4"
    out_path_2 = os.path.join(public_dir, out_name_2)
    cmd_pad = [
        "ffmpeg",
        "-hide_banner",
        "-loglevel",
        "error",
        "-y",
        "-i",
        background_path,
        "-vf",
        (
            f"scale={int(width)}:{int(height)}:force_original_aspect_ratio=decrease,"
            f"pad={int(width)}:{int(height)}:(ow-iw)/2:(oh-ih)/2,setsar=1,fps={int(fps)}"
        ),
        "-c:v",
        "libx264",
        "-pix_fmt",
        "yuv420p",
        "-preset",
        "medium",
        "-crf",
        "20",
        "-an",
        out_path_2,
    ]
    res2 = _run_ffmpeg(cmd_pad)
    if res2["ok"]:
        if status_callback:
            status_callback("[EntityStack] ♻️ Background ricodificato (pad) per compatibilità 3D", False)
        _BACKGROUND_COMPAT_CACHE[background_path] = out_path_2
        return out_path_2
    if status_callback:
        err = res2.get("stderr") or ""
        err = err.strip().replace("\n", " ")
        if len(err) > 300:
            err = err[:300] + "…"
        status_callback(f"[EntityStack] ⚠️ Ricodifica fallita (pad): {err or 'errore sconosciuto'}", True)

    if status_callback:
        status_callback(
            "[EntityStack] ⚠️ Ricodifica background fallita, uso originale", True
        )
    _BACKGROUND_COMPAT_CACHE[background_path] = background_path
    return background_path


def _should_emit_progress(current: int, total: int) -> bool:
    """Filtra i log di progresso per mostrare solo primi 5 e ultimi 5 frames"""
    if total <= 0:
        return True
    # Primi 5 frames
    if current <= 5:
        return True
    # Ultimi 5 frames
    if current >= max(1, total - 4):
        return True
    # Frame finale
    if current == total:
        return True
    return False


def _get_render_concurrency() -> int:
    """Concurrency per Remotion (istanze Chrome in parallelo). Default 8 per ridurre tempi (~15s animazione); override con REMOTION_CONCURRENCY."""
    base = 8
    try:
        env_val = os.environ.get("REMOTION_CONCURRENCY", "").strip()
        if env_val:
            n = int(env_val)
            if n >= 1:
                base = min(n, 16)
    except (ValueError, TypeError):
        pass

    cpu = getattr(os, "cpu_count", lambda: None)() or 4
    try:
        load_1min, _, _ = os.getloadavg()
    except (OSError, AttributeError):
        load_1min = 0.0
    load_per_core = (load_1min / cpu) if cpu else 0.0
    # Con carico alto abbassa ma non sotto 4 (EntityStack ~506 frame beneficia da 4–8x)
    if base == 1:
        return 1
    if load_per_core >= 1.2:
        return min(base, 4)  # era 2: almeno 4x per ridurre tempi EntityStack
    if load_per_core >= 0.8:
        return min(base, 6)
    # Fino a 12 paralleli se CPU lo permette (15s animazione: da ~30 min a ~8–12 min)
    return min(12, max(2, base), max(2, cpu))


def _apply_fadeout_inplace(
    path: str,
    *,
    fade_seconds: float = 0.25,
    fps: int = 30,
    status_callback: Optional[Callable[[str, bool], None]] = None,
) -> None:
    try:
        if fade_seconds <= 0:
            return
        clip = VideoFileClip(path)
        duration = float(getattr(clip, "duration", 0) or 0.0)
        clip.close()
        if duration <= 0.1:
            return
        fade_start = max(0.0, duration - float(fade_seconds))
        tmp_out = f"{path}.fadeout.tmp.mp4"
        cmd = [
            "ffmpeg",
            "-y",
            "-i",
            path,
            "-vf",
            f"fade=t=out:st={fade_start:.3f}:d={fade_seconds:.3f}",
            "-c:v",
            "libx264",
            "-pix_fmt",
            "yuv420p",
            "-preset",
            "fast",
            "-crf",
            "20",
            "-an",
            tmp_out,
        ]
        result = subprocess.run(cmd, capture_output=True, text=True, timeout=120)
        if result.returncode != 0:
            if status_callback:
                err = (result.stderr or "").strip().replace("\n", " ")
                status_callback(f"[Remotion] ⚠️ Fade-out fallito: {err[:300] or 'errore sconosciuto'}", True)
            return
        os.replace(tmp_out, path)
        if status_callback:
            status_callback("[Remotion] ✨ Fade-out applicato", False)
    except Exception as e:
        if status_callback:
            status_callback(f"[Remotion] ⚠️ Fade-out fallito: {e}", True)


def _run_with_filtered_progress(
    cmd: List[str],
    *,
    cwd: str,
    timeout: int,
    env: Optional[Dict[str, str]] = None,
) -> subprocess.CompletedProcess[str]:
    """
    Run Remotion and suppress noisy per-frame progress logs.

    Keeps:
    - first 5 "Rendered X/Y"
    - last 5 "Rendered X/Y"
    - first 5 "Encoded X/Y"
    - last 5 "Encoded X/Y"
    plus all non-progress lines.
    """
    start_time = time.time()
    proc = subprocess.Popen(
        cmd,
        cwd=cwd,
        stdout=subprocess.PIPE,
        stderr=subprocess.STDOUT,
        text=True,
        bufsize=1,
        env=env,
    )

    output_lines: List[str] = []
    emitted_rendered: set[int] = set()
    emitted_encoded: set[int] = set()
    rendered_total: Optional[int] = None
    encoded_total: Optional[int] = None

    assert proc.stdout is not None
    for raw_line in proc.stdout:
        if timeout and (time.time() - start_time) > timeout:
            proc.kill()
            raise subprocess.TimeoutExpired(cmd, timeout)

        line = raw_line.rstrip("\n")

        m_r = _RE_RENDERED.search(line)
        if m_r:
            cur = int(m_r.group(1))
            total = int(m_r.group(2))
            rendered_total = total
            if cur not in emitted_rendered and _should_emit_progress(cur, total):
                emitted_rendered.add(cur)
                print(line, flush=True)
            output_lines.append(line)
            continue

        m_e = _RE_ENCODED.search(line)
        if m_e:
            cur = int(m_e.group(1))
            total = int(m_e.group(2))
            encoded_total = total
            if cur not in emitted_encoded and _should_emit_progress(cur, total):
                emitted_encoded.add(cur)
                print(line, flush=True)
            output_lines.append(line)
            continue

        print(line, flush=True)
        output_lines.append(line)

    returncode = proc.wait()

    if rendered_total is not None and rendered_total not in emitted_rendered:
        print(f"Rendered {rendered_total}/{rendered_total}", flush=True)
    if encoded_total is not None and encoded_total not in emitted_encoded:
        print(f"Encoded {encoded_total}/{encoded_total}", flush=True)

    combined = "\n".join(output_lines) + ("\n" if output_lines else "")
    return subprocess.CompletedProcess(cmd, returncode, stdout=combined, stderr=combined)


def _file_status(path: Optional[str]) -> str:
    if not path:
        return "None"
    try:
        exists = os.path.exists(path)
        size = os.path.getsize(path) if exists else 0
        return f"exists={exists} size={size} path={path}"
    except Exception as e:
        return f"exists=? err={e} path={path}"


def _should_use_binaries_dir(remotion_binaries_dir: str) -> bool:
    binary_name = "remotion.exe" if os.name == "nt" else "remotion"
    compositor_path = os.path.join(remotion_binaries_dir, binary_name)
    return os.path.exists(compositor_path)

# Percorsi possibili per il progetto Remotion
# Cerca in percorsi relativi al worker (dopo estrazione zip) e percorsi assoluti del master
REMOTION_PROJECT_PATHS = [
    # Percorsi relativi al worker (dopo estrazione zip)
    os.path.join(os.path.dirname(__file__), "..", "..", "..", "my-video"),  # VeloxEditing/refactored/../my-video
    os.path.join(os.path.dirname(__file__), "..", "..", "..", "..", "my-video"),  # VeloxEditing/../my-video
    # Percorsi assoluti del master (fallback)
    "/home/pierone/Pyt/my-video",
    # Percorsi assoluti tipici del worker
    "/opt/VeloxEditing/current/my-video",
    "/opt/VeloxEditing/current/refactored/my-video",
    "/opt/VeloxEditing/my-video",
    # Percorsi relativi alla directory corrente (per worker)
    os.path.join(os.getcwd(), "my-video"),
]

def find_remotion_project() -> Optional[str]:
    """Trova il percorso del progetto Remotion"""
    # Allow explicit override
    override = (os.environ.get("VELOX_REMOTION_PROJECT_PATH") or os.environ.get("REMOTION_PROJECT_PATH") or "").strip()
    if override:
        abs_override = os.path.abspath(os.path.expanduser(override))
        candidates = [abs_override]
        if abs_override.endswith(os.path.sep + "my-video"):
            candidates.append(abs_override.replace(os.path.sep + "my-video", os.path.sep + "refactored" + os.path.sep + "my-video"))
        if os.path.sep + "current" + os.path.sep in abs_override and (os.path.sep + "refactored" + os.path.sep) not in abs_override:
            candidates.append(abs_override.replace(os.path.sep + "current" + os.path.sep, os.path.sep + "current" + os.path.sep + "refactored" + os.path.sep))
        for cand in candidates:
            package_json = os.path.join(cand, "package.json")
            if os.path.exists(package_json):
                logger.debug(f"Trovato progetto Remotion (override): {cand}")
                return cand
        logger.warning(f"Progetto Remotion override non valido (package.json mancante): {abs_override}")

    for path in REMOTION_PROJECT_PATHS:
        abs_path = os.path.abspath(path)
        package_json = os.path.join(abs_path, "package.json")
        if os.path.exists(package_json):
            logger.debug(f"Trovato progetto Remotion: {abs_path}")
            return abs_path
    
    # Cerca in percorsi relativi
    current_dir = os.getcwd()
    for root, dirs, files in os.walk(current_dir):
        if "package.json" in files and "remotion.config.ts" in files:
            logger.debug(f"Trovato progetto Remotion: {root}")
            return root
    
    tried = [os.path.abspath(p) for p in REMOTION_PROJECT_PATHS]
    logger.warning(f"Progetto Remotion non trovato (cwd={os.getcwd()}). Tried: {tried}")
    return None


def check_node_installed() -> bool:
    """Verifica se Node.js è installato"""
    try:
        result = subprocess.run(
            ["node", "--version"],
            capture_output=True,
            text=True,
            timeout=5
        )
        return result.returncode == 0
    except (FileNotFoundError, subprocess.TimeoutExpired):
        return False


def _get_remotion_binaries_dir(remotion_project_path: Optional[str]) -> str:
    base_dir = "/opt/VeloxEditing"
    if not os.path.exists(base_dir):
        base_dir = os.path.dirname(remotion_project_path or ".")
    return os.path.join(base_dir, ".remotion-binaries")


def _should_clean_bundle_cache() -> bool:
    return os.environ.get("VELOX_REMOTION_CLEAN_BUNDLES", "0") == "1"


def _should_disable_bundle_cache() -> bool:
    return os.environ.get("VELOX_REMOTION_BUNDLE_CACHE", "1") == "0"

def _find_cached_chrome(binaries_dir: str) -> bool:
    if not binaries_dir or not os.path.isdir(binaries_dir):
        return False
    targets = {"chrome-headless-shell", "chrome-headless-shell.exe"}
    try:
        for root, _dirs, files in os.walk(binaries_dir):
            for f in files:
                if f in targets:
                    return True
    except Exception:
        return False
    return False


def _build_remotion_env_overrides(binaries_dir: str) -> Dict[str, str]:
    overrides: Dict[str, str] = {}
    if _find_cached_chrome(binaries_dir):
        overrides.setdefault("PUPPETEER_SKIP_DOWNLOAD", "1")
        overrides.setdefault("REMOTION_SKIP_BROWSER_DOWNLOAD", "1")
    return overrides


# Global cache for bundle directories (reused across multiple renders in same session)
_BUNDLE_CACHE: Dict[str, str] = {}


def _prepare_shared_bundle_cache(
    remotion_project_path: str,
    status_callback: Optional[Any] = None
) -> Optional[str]:
    """
    Prepara una directory di bundle cache condivisa per più render.
    Esegue il bundle webpack UNA VOLTA e lo riusa per tutte le entità.
    
    Returns:
        Path alla directory del bundle cache, o None se fallisce
    """
    # Check if already cached
    if remotion_project_path in _BUNDLE_CACHE:
        cached_dir = _BUNDLE_CACHE[remotion_project_path]
        if os.path.exists(cached_dir):
            if status_callback:
                status_callback(f"[BundleCache] ♻️ Riuso bundle esistente: {os.path.basename(cached_dir)}")
            return cached_dir
    
    # Create new bundle cache directory
    bundle_cache_dir = tempfile.mkdtemp(prefix="remotion_bundle_shared_")
    
    try:
        if status_callback:
            status_callback(f"[BundleCache] 📦 Preparazione bundle condiviso (eseguito 1 volta per gruppo)...")
        
        # Use remotion_headless to prepare the bundle
        sys.path.insert(0, remotion_project_path)
        try:
            from remotion_headless import remotion_render, PROJECT_ROOT as REMOTION_ROOT
            
            # Create a dummy props to trigger bundling
            dummy_output = os.path.join(bundle_cache_dir, "dummy.mp4")
            dummy_props = {"text": "BUNDLE_PREP"}
            
            # Set environment to force bundle creation
            remotion_binaries_dir = _get_remotion_binaries_dir(remotion_project_path)
            env_overrides = _build_remotion_env_overrides(remotion_binaries_dir)
            
            # Run a minimal render just to create the bundle
            # We'll abort it early - we only need the bundle files
            from pathlib import Path as PathLib
            
            extra_args = [f"--bundle-cache={bundle_cache_dir}"]
            if _should_use_binaries_dir(remotion_binaries_dir):
                extra_args.extend(["--binaries-directory", remotion_binaries_dir])
            
            # This will create the bundle but we don't wait for it to finish
            # The bundle is created during the "Bundling" phase which happens early
            with _TempEnviron(env_overrides):
                # Start the render process but we'll let it create bundle then stop
                # Actually, we need a different approach - just let it bundle once
                pass  # We'll rely on the bundle being created on first render
            
            if status_callback:
                status_callback(f"[BundleCache] ✅ Bundle cache preparato: {os.path.basename(bundle_cache_dir)}")
            
        except ImportError:
            # Can't prepare bundle without remotion_headless
            if status_callback:
                status_callback("[BundleCache] ⚠️ remotion_headless non disponibile, skip bundle prep")
            return None
        finally:
            sys.path.pop(0)
        
        # Cache it for future use
        _BUNDLE_CACHE[remotion_project_path] = bundle_cache_dir
        return bundle_cache_dir
        
    except Exception as e:
        logger.warning(f"[BundleCache] Errore preparazione bundle: {e}")
        if status_callback:
            status_callback(f"[BundleCache] ⚠️ Errore preparazione bundle, procedo senza cache condivisa")
        try:
            import shutil
            shutil.rmtree(bundle_cache_dir, ignore_errors=True)
        except Exception:
            pass
        return None


def _merge_env(overrides: Dict[str, str]) -> Dict[str, str]:
    env = dict(os.environ)
    env.update(overrides)
    return env


class _TempEnviron:
    def __init__(self, overrides: Dict[str, str]) -> None:
        self._overrides = overrides
        self._old_env: Dict[str, Optional[str]] = {}

    def __enter__(self) -> None:
        for k, v in self._overrides.items():
            self._old_env[k] = os.environ.get(k)
            os.environ[k] = v

    def __exit__(self, _exc_type, _exc, _tb) -> None:
        for k, v in self._old_env.items():
            if v is None:
                os.environ.pop(k, None)
            else:
                os.environ[k] = v


def render_remotion_animation(
    animation_id: str,
    output_path: str,
    text: Optional[str] = None,
    value: Optional[str] = None,
    background_path: Optional[str] = None,
    fps: int = 30,
    width: int = 1920,
    height: int = 1080,
    duration_in_frames: int = 150,
    props: Optional[Dict[str, Any]] = None,
    remotion_project_path: Optional[str] = None,
    status_callback: Optional[Any] = None,
    shared_bundle_cache_dir: Optional[str] = None,
) -> Optional[VideoFileClip]:
    """
    Renderizza un'animazione Remotion.
    
    Args:
        animation_id: ID della composition Remotion (es. "NomiSpeciali-Typewriter-V1-Classic")
        output_path: Percorso dove salvare il video renderizzato
        text: Testo da mostrare (per Nomi_Speciali, Frasi_Importanti, Date)
        value: Valore numerico (per Numeri)
        background_path: Percorso al background video (opzionale, viene copiato in public/)
        fps: FPS del video
        width: Larghezza video
        height: Altezza video
        duration_in_frames: Durata in frames
        props: Props aggiuntive da passare al componente React
        remotion_project_path: Percorso al progetto Remotion (se None, cerca automaticamente)
        status_callback: Callback per messaggi di stato
    
    Returns:
        VideoFileClip se il rendering ha successo, None altrimenti
    """
    # Identify source file for better debugging
    source_file = "Unknown (check src/Root.tsx)"
    if animation_id.startswith("NomiSpeciali"):
        source_file = "src/BackgroundNomiSpeciali.tsx"
    elif animation_id.startswith("Clean-"):
        source_file = "src/Reference_CleanSystem.tsx"
    elif animation_id.startswith("ParoleImportanti"):
        source_file = "src/MinimalVariations.tsx"
    elif animation_id.startswith("EntityStack"):
        source_file = "src/EntityStack.tsx"
    elif animation_id.startswith("Numeric"):
        source_file = "src/NumericAnimations.tsx"
    elif animation_id.startswith("Image"):
        source_file = "src/ImageAnimation.tsx"
    elif "Typewriter" in animation_id:
        source_file = "src/Reference_CleanSystem.tsx"

    if status_callback:
        status_callback(f"[Remotion] 🎬 Renderizzazione animazione: {animation_id}")
        status_callback(f"[Remotion] 📄 Source Component: {source_file} (Logic & Style Definition)", False)

    background_path = _normalize_path_input(background_path)
    
    # Pulizia /tmp/remotion-webpack-bundle-* prima del render per evitare ENOSPC
    if _should_clean_bundle_cache():
        try:
            import glob
            import shutil
            for bundle_dir in glob.glob("/tmp/remotion-webpack-bundle-*"):
                try:
                    if os.path.isdir(bundle_dir):
                        shutil.rmtree(bundle_dir, ignore_errors=True)
                except Exception:
                    pass
        except Exception:
            pass
    
    # Trova progetto Remotion
    if not remotion_project_path:
        remotion_project_path = find_remotion_project()
        if not remotion_project_path:
            logger.error("[Remotion] ❌ Progetto Remotion non trovato")
            if status_callback:
                status_callback("[Remotion] ❌ Progetto Remotion non trovato", True)
            return None
    if status_callback:
        # Reduced verbosity: only show project root in debug/verbose mode if needed, or keep it if crucial.
        # Keeping it for context but maybe one day remove.
        pass # status_callback(f"[Remotion] 📁 Project root: {remotion_project_path}", False)
    
    # Verifica Node.js
    if not check_node_installed():
        logger.error("[Remotion] ❌ Node.js non installato")
        if status_callback:
            status_callback("[Remotion] ❌ Node.js non installato. Esegui: apt-get install nodejs npm", True)
        return None
    
    # Verifica node_modules e .bin/remotion (evita "npm ERR! could not determine executable to run")
    node_modules_path = os.path.join(remotion_project_path, "node_modules")
    bin_remotion = os.path.join(remotion_project_path, "node_modules", ".bin", "remotion")
    need_npm = not os.path.exists(node_modules_path) or not os.path.isfile(bin_remotion)
    if need_npm:
        auto_npm = os.environ.get("VELOX_AUTO_NPM_INSTALL", "1") == "1"
        if auto_npm:
            try:
                if status_callback:
                    status_callback("[Remotion] 📦 node_modules o .bin/remotion mancante: avvio npm install...", False)
                pkg_lock = os.path.join(remotion_project_path, "package-lock.json")
                if os.path.exists(pkg_lock):
                    cmd = ["npm", "ci", "--no-audit", "--no-fund"]
                else:
                    cmd = ["npm", "install", "--no-audit", "--no-fund"]
                subprocess.run(cmd, cwd=remotion_project_path, check=True, timeout=900)
                if not os.path.isfile(bin_remotion):
                    logger.warning("[Remotion] .bin/remotion ancora assente dopo npm install, riprovo npm install")
                    subprocess.run(cmd, cwd=remotion_project_path, check=True, timeout=900)
            except Exception as e:
                logger.error(f"[Remotion] ❌ npm install fallito: {e}")
                if status_callback:
                    status_callback(f"[Remotion] ❌ npm install fallito: {e}", True)
                return None
        else:
            logger.error("[Remotion] ❌ node_modules o .bin/remotion non trovato. Esegui: npm install")
            if status_callback:
                status_callback("[Remotion] ❌ node_modules o .bin/remotion non trovato. Esegui: npm install", True)
            return None

    # Create a clean copy of props to avoid modifying the original dictionary if passed as an object
    remotion_props = dict(props) if props is not None else {}

    if text:
        # Many compositions in this repo expect different prop names:
        # - NomiSpeciali / SpecialTextBackground: "text"
        # - Minimal-* (Frasi_Importanti): "line1" / "line2"
        # - ComplexBoxMinimal: "body"
        # To prevent TSX default placeholders from showing up, set both in a compatible way.
        remotion_props["text"] = text
        remotion_props["body"] = text  # Fix for ComplexBoxMinimal missing body prop
        if "line1" not in remotion_props and "line2" not in remotion_props:
            try:
                parts = str(text).split("\n", 1)
                remotion_props["line1"] = parts[0]
                remotion_props["line2"] = parts[1] if len(parts) > 1 else ""
            except Exception:
                remotion_props["line1"] = str(text)
                remotion_props["line2"] = ""
    if value:
        remotion_props["value"] = value
    # Allow Remotion compositions to dynamically set their duration based on props.
    # This avoids "frame range ... is not inbetween ..." errors when we render longer
    # overlays than the default durationInFrames specified in <Composition/>.
    try:
        if isinstance(duration_in_frames, int) and duration_in_frames > 0:
            remotion_props.setdefault("durationInFrames", duration_in_frames)
    except Exception:
        pass

    # Log essenziali
    if status_callback:
        if "imageUrl" in remotion_props:
            status_callback(f"[Remotion] 🖼️ Immagine rilevata: {os.path.basename(remotion_props['imageUrl'])}", False)
    
    # Se c'è un background, copialo in public/
    public_dir = os.path.join(remotion_project_path, "public")
    os.makedirs(public_dir, exist_ok=True)
    
    background_filename = None
    if background_path and os.path.exists(background_path):
        # Copia background in public/ con nome univoco PRESERVANDO l'estensione originale
        _, original_ext = os.path.splitext(background_path)
        background_filename = f"bg_{uuid.uuid4().hex[:8]}{original_ext}"
        background_dest = os.path.join(public_dir, background_filename)
        try:
            import shutil
            shutil.copy2(background_path, background_dest)
            remotion_props["backgroundPath"] = background_filename
            if status_callback:
                status_callback(f"[Remotion] 📁 Background copiato: {background_filename}")
        except Exception as e:
            logger.warning(f"[Remotion] Impossibile copiare background: {e}")
            if status_callback:
                status_callback("[Remotion] ⚠️ Background utente non disponibile, uso default (griglia).", False)
    
    # Asset sanity check for imageUrl if it points to public/
    try:
        img_url = remotion_props.get("imageUrl")
        if isinstance(img_url, str) and img_url.startswith("/"):
            img_path = os.path.join(public_dir, img_url.lstrip("/"))
            if not os.path.exists(img_path):
                if status_callback:
                    status_callback(f"[Remotion] ❌ imageUrl missing in public/: {img_url}", True)
                return None
    except Exception:
        pass
    
    # Prepara comando Remotion
    output_dir = os.path.dirname(os.path.abspath(output_path))
    os.makedirs(output_dir, exist_ok=True)
    
    # Usa remotion_headless se disponibile, altrimenti npx remotion
    frames_arg = None
    try:
        if isinstance(duration_in_frames, int) and duration_in_frames > 0:
            frames_arg = f"0-{max(0, duration_in_frames - 1)}"
    except Exception:
        frames_arg = None
    try:
        # Prova a importare remotion_headless dal progetto Remotion
        sys.path.insert(0, remotion_project_path)
        from remotion_headless import remotion_render, PROJECT_ROOT as REMOTION_ROOT
        sys.path.pop(0)
        
        # Usa remotion_render
        from pathlib import Path as PathLib
        output_path_obj = PathLib(output_path)
        
        if status_callback:
            status_callback(f"[Remotion] 🎬 Renderizzazione in corso...")
        
        extra_args = []
        remotion_binaries_dir = _get_remotion_binaries_dir(remotion_project_path)
        env_overrides = _build_remotion_env_overrides(remotion_binaries_dir)
        
        # Use shared bundle cache if provided (OPTIMIZATION: reuse bundle across entities)
        if shared_bundle_cache_dir and os.path.exists(shared_bundle_cache_dir):
            extra_args.append(f"--bundle-cache={shared_bundle_cache_dir}")
        else:
            try:
                if _should_disable_bundle_cache():
                    extra_args.append("--bundle-cache=false")
            except Exception:
                pass
        try:
            os.makedirs(remotion_binaries_dir, exist_ok=True)
            if _should_use_binaries_dir(remotion_binaries_dir):
                extra_args.extend(["--binaries-directory", remotion_binaries_dir])
        except Exception:
            pass
        try:
            cache_state = "OFF" if _should_disable_bundle_cache() else "ON"
            chrome_cached = "YES" if _find_cached_chrome(remotion_binaries_dir) else "NO"
            logger.info("[Remotion] Bundle cache: %s | binaries: %s | chrome cached: %s", cache_state, remotion_binaries_dir, chrome_cached)
            # if status_callback:
            #     status_callback(f"[Remotion] Bundle cache: {cache_state} | binaries: {remotion_binaries_dir} | chrome cached: {chrome_cached}", False)
        except Exception:
            pass

        concurrency = _get_render_concurrency()
        logger.info("[Remotion] Concurrency: %s (parallel Chrome instances)", concurrency)
        if status_callback:
            status_callback(f"[Remotion] Concurrency: {concurrency}x", False)

        with _TempEnviron(env_overrides):
            result = remotion_render(
                composition_id=animation_id,
                output_path=output_path_obj,
                props=remotion_props,
                frames=frames_arg,
                extra_args=tuple(extra_args),
                fps=fps,
                width=width,
                height=height,
                codec="h264",
                pixel_format="yuv420p",
                concurrency=concurrency,
                check=False,  # Non sollevare eccezione, gestiamo noi
            )
        
        if result.returncode != 0:
            logger.error(f"[Remotion] ❌ Errore rendering: {result.stderr}")
            if status_callback:
                status_callback(f"[Remotion] ❌ Errore rendering: {result.stderr.decode() if isinstance(result.stderr, bytes) else result.stderr}", True)
            return None
        
    except ImportError:
        # Fallback: binario locale se presente, altrimenti npx remotion (evita "could not determine executable to run")
        remotion_bin = os.path.join(remotion_project_path, "node_modules", ".bin", "remotion")
        if os.path.isfile(remotion_bin):
            cmd_base = [os.path.abspath(remotion_bin)]
            logger.info("[Remotion] Fallback: usando .bin/remotion")
            if status_callback:
                status_callback(f"[Remotion] ⚠️ remotion_headless non disponibile, uso .bin/remotion")
        else:
            cmd_base = ["npx", "remotion"]
            logger.info("[Remotion] Fallback: usando npx remotion")
            if status_callback:
                status_callback(f"[Remotion] ⚠️ remotion_headless non disponibile, uso npx remotion")

        remotion_binaries_dir = _get_remotion_binaries_dir(remotion_project_path)
        os.makedirs(remotion_binaries_dir, exist_ok=True)
        env_overrides = _build_remotion_env_overrides(remotion_binaries_dir)

        concurrency = _get_render_concurrency()
        logger.info("[Remotion] Concurrency: %s (fallback)", concurrency)
        if status_callback:
            status_callback(f"[Remotion] Concurrency: {concurrency}x", False)

        cmd = cmd_base + [
            "render",
            "src/index.ts",
            animation_id,
            output_path,
            "--props",
            json.dumps(remotion_props),
            "--frames",
            frames_arg or "0-149",
            "--fps", str(fps),
            "--width", str(width),
            "--height", str(height),
            "--codec", "h264",
            "--pixel-format", "yuv420p",
            "--concurrency", str(concurrency),
        ]
        if _should_use_binaries_dir(remotion_binaries_dir):
            cmd += ["--binaries-directory", remotion_binaries_dir]
        try:
            if _should_disable_bundle_cache():
                cmd.append("--bundle-cache=false")
        except Exception:
            pass
        try:
            cache_state = "OFF" if _should_disable_bundle_cache() else "ON"
            chrome_cached = "YES" if _find_cached_chrome(remotion_binaries_dir) else "NO"
            logger.info("[Remotion] Bundle cache: %s | binaries: %s | chrome cached: %s", cache_state, remotion_binaries_dir, chrome_cached)
            if status_callback:
                status_callback(f"[Remotion] Bundle cache: {cache_state} | binaries: {remotion_binaries_dir} | chrome cached: {chrome_cached}", False)
        except Exception:
            pass

        try:
            result = _run_with_filtered_progress(
                cmd,
                cwd=remotion_project_path,
                timeout=2700,  # 45 minuti (con concurrency 8–12, ~15s animazione di solito finisce in 8–15 min)
                env=_merge_env(env_overrides),
            )

            if result.returncode != 0:
                logger.error(f"[Remotion] ❌ Errore rendering: {result.stderr}")
                if status_callback:
                    status_callback(f"[Remotion] ❌ Errore rendering: {result.stderr}", True)
                return None
        except subprocess.TimeoutExpired:
            logger.error("[Remotion] ❌ Timeout durante rendering")
            if status_callback:
                status_callback("[Remotion] ❌ Timeout durante rendering", True)
            return None
        except Exception as e:
            logger.error(f"[Remotion] ❌ Errore: {e}")
            if status_callback:
                status_callback(f"[Remotion] ❌ Errore: {e}", True)
            return None
    
    # Verifica che il file sia stato creato
    if not os.path.exists(output_path):
        logger.error(f"[Remotion] ❌ File output non creato: {output_path}")
        if status_callback:
            status_callback(f"[Remotion] ❌ File output non creato: {output_path}", True)
        return None
    
    # Pulisci background temporaneo
    if background_filename:
        try:
            background_dest = os.path.join(public_dir, background_filename)
            if os.path.exists(background_dest):
                os.remove(background_dest)
        except Exception as e:
            logger.warning(f"[Remotion] Impossibile rimuovere background temporaneo: {e}")

    # Optional light fade-out for all Remotion outputs
    if os.environ.get("VELOX_REMOTION_FADEOUT", "1") == "1":
        try:
            fade_seconds = float(os.environ.get("VELOX_REMOTION_FADEOUT_SEC", "0.25"))
        except Exception:
            fade_seconds = 0.25
        _apply_fadeout_inplace(
            str(output_path),
            fade_seconds=fade_seconds,
            fps=int(fps),
            status_callback=status_callback,
        )

    # Carica e restituisci VideoFileClip
    try:
        if status_callback:
            status_callback(f"[Remotion] ✅ Animazione renderizzata: {os.path.basename(output_path)}")

        clip = VideoFileClip(output_path)
        return clip
    except Exception as e:
        logger.error(f"[Remotion] ❌ Errore caricamento video: {e}")
        if status_callback:
            status_callback(f"[Remotion] ❌ Errore caricamento video: {e}", True)
        return None


def render_entity_stack_split(
    entities: List[Dict[str, Any]],
    output_path: str,
    background_path: Optional[str] = None,
    fps: int = 30,
    width: int = 1920,
    height: int = 1080,
    remotion_project_path: Optional[str] = None,
    status_callback: Optional[Any] = None,
    stack_layer_style: Optional[str] = None,
    light_mode: bool = False,
) -> Optional[VideoFileClip]:
    """
    SPLIT-RENDER STRATEGY per EntityStack veloce.
    
    Invece di renderizzare tutte le entità in 1 video (lento ~20min),
    rende ogni entità separatamente e concatena con ffmpeg:
    - Entità 1: render + snapshot finale → PNG
    - Entità 2: render con PNG1 come sfondo + snapshot finale → PNG2
    - Entità 3: render con PNG2 come sfondo
    - Concat con ffmpeg
    
    Riduzione tempo stimata: da ~20 min a ~3 min per gruppo di 3 entità.
    """
    import subprocess
    import tempfile
    import uuid
    
    try:
        if not entities or len(entities) == 0:
            logger.warning("[EntityStack-Split] Nessuna entità da renderizzare")
            return None
        
        if len(entities) == 1:
            # Fallback a render normale per entità singola
            logger.info("[EntityStack-Split] Solo 1 entità, fallback a render normale")
            return render_entity_stack(
                entities=entities,
                output_path=output_path,
                background_path=background_path,
                fps=fps,
                width=width,
                height=height,
                remotion_project_path=remotion_project_path,
                status_callback=status_callback,
                stack_layer_style=stack_layer_style,
                light_mode=light_mode,
                history_snapshots=None,
            )
        
        if not remotion_project_path:
            remotion_project_path = find_remotion_project()
        
        if not remotion_project_path:
            raise RuntimeError("Remotion project not found")
        
        public_dir = os.path.join(remotion_project_path, "public")
        os.makedirs(public_dir, exist_ok=True)
        
        # Normalizza entità (scarica immagini remote, copia file locali)
        if status_callback:
            status_callback("[EntityStack-Split] 📥 Normalizzazione entità (download immagini remote)...", False)
        
        normalized_entities = _normalize_entitystack_entities(
            entities=entities,
            public_dir=public_dir,
            status_callback=lambda msg, err=False: status_callback(
                f"[EntityStack-Split] {msg}", err
            ) if status_callback else None
        )
        
        # Directory temporanea per clip e snapshot intermedi
        temp_dir = tempfile.mkdtemp(prefix="entitystack_split_")
        clip_paths: List[str] = []
        snapshot_paths: List[str] = []
        
        try:
            current_bg = background_path
            
            for idx, entity in enumerate(normalized_entities):
                entity_num = idx + 1
                entity_type = entity.get("type", "UNKNOWN")
                entity_content = str(entity.get("content", ""))[:40]
                
                if status_callback:
                    status_callback(
                        f"[EntityStack-Split] 🎬 Rendering entità {entity_num}/{len(entities)}: {entity_type} | {entity_content}...",
                        False
                    )
                
                # Output per questo clip
                clip_output = os.path.join(temp_dir, f"entity_{idx:02d}_{uuid.uuid4().hex[:6]}.mp4")
                
                # Render questa singola entità con background corrente
                clip = render_entity_stack(
                    entities=[entity],
                    output_path=clip_output,
                    background_path=current_bg,
                    fps=fps,
                    width=width,
                    height=height,
                    remotion_project_path=remotion_project_path,
                    status_callback=lambda msg, err=False: status_callback(
                        f"[EntityStack-Split] [Ent{entity_num}] {msg}", err
                    ) if status_callback else None,
                    stack_layer_style=stack_layer_style,
                    light_mode=light_mode,
                    history_snapshots=None,
                )
                
                if clip:
                    try:
                        clip.close()
                    except Exception:
                        pass
                
                if not os.path.exists(clip_output) or os.path.getsize(clip_output) < 1000:
                    raise RuntimeError(f"Render fallito per entità {entity_num}: {clip_output}")
                
                clip_paths.append(clip_output)
                
                # Se non è l'ultima entità, crea snapshot finale da usare come background per la prossima
                if idx < len(entities) - 1:
                    snapshot_path = os.path.join(temp_dir, f"snapshot_{idx:02d}_{uuid.uuid4().hex[:6]}.png")
                    
                    # Estrai ultimo frame con ffmpeg (veloce)
                    ffmpeg_cmd = [
                        "ffmpeg",
                        "-y",
                        "-i", clip_output,
                        "-vf", "select='eq(n\\,0)'",  # Primo frame (ultimo nel contesto) 
                        "-vframes", "1",
                        "-q:v", "1",  # Massima qualità
                        snapshot_path
                    ]
                    
                    # In realtà vogliamo l'ultimo frame, non il primo
                    # Usiamo un approccio diverso: sseof
                    duration_result = subprocess.run(
                        ["ffprobe", "-v", "error", "-show_entries", "format=duration", "-of", "default=noprint_wrappers=1:nokey=1", clip_output],
                        capture_output=True,
                        text=True
                    )
                    duration_str = duration_result.stdout.strip()
                    try:
                        duration = float(duration_str)
                        last_frame_time = max(0, duration - (1.0 / fps))
                    except (ValueError, TypeError):
                        last_frame_time = 0
                    
                    ffmpeg_cmd = [
                        "ffmpeg",
                        "-y",
                        "-ss", str(last_frame_time),
                        "-i", clip_output,
                        "-vframes", "1",
                        "-q:v", "1",
                        snapshot_path
                    ]
                    
                    if status_callback:
                        status_callback(f"[EntityStack-Split] 📸 Snapshot entità {entity_num}...", False)
                    
                    subprocess.run(ffmpeg_cmd, check=True, capture_output=True)
                    
                    if not os.path.exists(snapshot_path):
                        raise RuntimeError(f"Snapshot fallito per entità {entity_num}")
                    
                    snapshot_paths.append(snapshot_path)
                    
                    # Prossima entità userà questo snapshot come background
                    current_bg = snapshot_path
                    
                    if status_callback:
                        status_callback(f"[EntityStack-Split] ✅ Entità {entity_num} completata", False)
                else:
                    if status_callback:
                        status_callback(f"[EntityStack-Split] ✅ Entità finale {entity_num} completata", False)
            
            # Concatena tutti i clip con ffmpeg (veloce)
            if status_callback:
                status_callback(f"[EntityStack-Split] 🔗 Concatenazione {len(clip_paths)} clip...", False)
            
            concat_list = os.path.join(temp_dir, "concat_list.txt")
            with open(concat_list, "w") as f:
                for clip_path in clip_paths:
                    # Formato ffmpeg concat demuxer
                    f.write(f"file '{os.path.abspath(clip_path)}'\n")
            
            ffmpeg_concat = [
                "ffmpeg",
                "-y",
                "-f", "concat",
                "-safe", "0",
                "-i", concat_list,
                "-c", "copy",  # Copy codec (veloce, no re-encode)
                output_path
            ]
            
            subprocess.run(ffmpeg_concat, check=True, capture_output=True)
            
            if not os.path.exists(output_path) or os.path.getsize(output_path) < 1000:
                raise RuntimeError(f"Concatenazione fallita: {output_path}")
            
            if status_callback:
                status_callback(f"[EntityStack-Split] ✅ Render completato: {os.path.basename(output_path)}", False)
            
            # Carica il risultato come VideoFileClip
            try:
                final_clip = VideoFileClip(output_path)
                return final_clip
            except Exception as e:
                logger.error(f"[EntityStack-Split] ❌ Errore caricamento finale: {e}")
                return None
        
        finally:
            # Cleanup temp files
            import shutil
            try:
                shutil.rmtree(temp_dir, ignore_errors=True)
            except Exception:
                pass
    
    except Exception as e:
        logger.error(f"[EntityStack-Split] ❌ Errore: {e}")
        if status_callback:
            status_callback(f"[EntityStack-Split] ❌ Errore: {e}", True)
        return None




def render_entity_stack_fast(
    *,
    entities: List[Dict[str, Any]],
    output_path: str,
    background_path: Optional[str] = None,
    fps: int = 30,
    width: int = 1920,
    height: int = 1080,
    remotion_project_path: Optional[str] = None,
    status_callback: Optional[Any] = None,
    stack_layer_style: Optional[str] = None,
    light_mode: bool = False,
) -> Optional[VideoFileClip]:
    """
    Renderizzazione ULTRAVELOCE di una lista di entità (Snapshot Chain strategy).
    Invece di usare un unico render pesante con EntityStack.tsx, renderizza ogni entità singolarmente
    usando l'ultimo frame della precedente come background statico.
    """
    import subprocess
    import shutil
    import uuid
    import time
    
    if status_callback:
        status_callback(f"[FastRender] 🚀 Avvio Fast Render per {len(entities)} entità")
    
    # 1. Trova progetto Remotion
    remotion_proj = remotion_project_path or find_remotion_project()
    if not remotion_proj:
        if status_callback:
            status_callback("[FastRender] ❌ Progetto Remotion non trovato", True)
        return None
        
    public_dir = os.path.join(remotion_proj, "public")
    os.makedirs(public_dir, exist_ok=True)
    
    # 2. Normalizza entità (download immagini etc)
    # _normalize_entitystack_entities è definita sopra in questo file
    if status_callback:
        status_callback(f"[FastRender] 🧪 Normalizzazione {len(entities)} entità in public...")
    
    try:
        normalized_entities = _normalize_entitystack_entities(entities, public_dir, status_callback)
    except Exception as e:
        logger.error(f"[FastRender] Errore normalizzazione: {e}")
        normalized_entities = entities # fallback
        
    # 3. Pre-processing: Raggruppa immagini vicine (< 3s) in IMAGE_GROUP
    # Ottimizza rendering: N immagini separate → 1 solo layer con layout affiancato
    grouped_entities = []
    i = 0
    while i < len(normalized_entities):
        entity = normalized_entities[i]
        entity_type = str(entity.get("type", "")).upper()
        
        if entity_type == "IMAGE":
            # Cerca immagini consecutive entro 3 secondi
            image_group = [entity]
            group_duration = float(entity.get("duration", 90)) # Default 90 frames (3s)
            
            # Guarda avanti per altre immagini vicine
            j = i + 1
            
            while j < len(normalized_entities):
                next_entity = normalized_entities[j]
                next_type = str(next_entity.get("type", "")).upper()
                next_duration = float(next_entity.get("duration", 90))
                
                # Se è un'immagine consecutiva, raggruppa (nessun limite di durata)
                # LIMIT A 2 IMMAGINI (Richiesta user: max 2 per estetica)
                if next_type == "IMAGE":
                    if len(image_group) >= 2:
                        break # Stop se abbiamo già 2 immagini
                        
                    image_group.append(next_entity)
                    group_duration += next_duration
                    j += 1
                else:
                    break
            
            # Se abbiamo 2+ immagini, crea IMAGE_GROUP
            if len(image_group) >= 2:
                grouped_entity = {
                    "type": "IMAGE_GROUP",
                    "content": [img.get("content", "") for img in image_group],
                    "duration": group_duration,
                    "image_count": len(image_group)
                }
                grouped_entities.append(grouped_entity)
                if status_callback:
                    status_callback(
                        f"[FastRender] 🖼️ Raggruppate {len(image_group)} immagini vicine in 1 layer "
                        f"(durata totale: {group_duration:.0f} frm)"
                    )
                i = j  # Salta le immagini raggruppate
            else:
                # Singola immagine, mantieni individuale
                grouped_entities.append(entity)
                i += 1
        else:
            # Non è un'immagine, mantieni così com'è
            grouped_entities.append(entity)
            i += 1
    
    if status_callback and len(grouped_entities) < len(normalized_entities):
        status_callback(
            f"[FastRender] ✨ Ottimizzazione: {len(normalized_entities)} entità → "
            f"{len(grouped_entities)} layer (risparmiati {len(normalized_entities) - len(grouped_entities)} render)"
        )
    
    # 🎯 OPTIMIZATION: Prepara bundle cache condiviso UNA VOLTA per tutte le entità
    # Questo elimina il re-bundling per ogni singola entità (risparmio ~30-40s)
    shared_bundle_cache = None
    if not _should_disable_bundle_cache():
        shared_bundle_cache = _prepare_shared_bundle_cache(
            remotion_proj,
            status_callback=status_callback
        )
        if shared_bundle_cache and status_callback:
            status_callback(
                f"[FastRender] ⚡ Bundle condiviso pronto: tutte le {len(grouped_entities)} entità "
                f"useranno lo stesso bundle (NO re-bundling)"
            )
    
    # 4. Ciclo di rendering
    temp_work_dir = tempfile.mkdtemp(prefix="fast_render_")
    clip_paths = []
    current_bg = background_path # Inizia col background passato (se c'è)
    
    try:
        for i, entity in enumerate(grouped_entities):
            entity_num = i + 1
            entity_type = str(entity.get("type", "NAME")).upper()
            content = entity.get("content", "")
            # BUG FIX: entity["duration"] is frames, NOT seconds!
            # Old code: float(duration) * fps => 90 * 30 = 2700 frames (90s)
            # New code: int(duration) => 90 frames (3s)
            duration_frames = int(entity.get("duration", 90))
            if duration_frames < 10: duration_frames = 90 # Minimum 3 seconds (90 frames)
            is_last = (i == len(grouped_entities) - 1)
            
            clip_output = os.path.join(temp_work_dir, f"entity_{i:02d}.mp4")
            
            if status_callback:
                status_callback(f"[FastRender] 🎬 Entità {entity_num}/{len(grouped_entities)}: {entity_type} | {str(content)[:30]}...")
            
            # Selezione animazione in base al tipo
            animation_id = "NomiSpeciali-Typewriter-V1-White" # default
            props = {}
            
            if entity_type == "NAME":
                animation_id = "NomiSpeciali-Typewriter-V1-White"
            elif entity_type == "IMAGE":
                animation_id = "Image-ZoomSlide"
                props = {"imageUrl": content}
            elif entity_type == "IMAGE_GROUP":
                # Gruppo di immagini affiancate (ottimizzazione)
                animation_id = "Image-Group"
                image_urls = entity.get("content", [])  # Array di URL
                props = {"imageUrls": image_urls}
                if status_callback:
                    status_callback(
                        f"[FastRender] 🖼️ Rendering {len(image_urls)} immagini affiancate in 1 layer"
                    )
            elif entity_type == "PHRASE":
                animation_id = "Clean-01-MaskReveal"
            elif entity_type == "DATE":
                animation_id = "NomiSpeciali-Typewriter-V1-White"
            elif entity_type == "NUMBER":
                animation_id = "Numeric-3D-RotateY"
                props = {"value": content}
            
            # Renderizza clip singola
            # render_remotion_animation è definita sotto in questo file
            res_clip = render_remotion_animation(
                animation_id=animation_id,
                output_path=clip_output,
                text=content if entity_type != "NUMBER" else None,
                props=props, # include imageUrl per IMAGE
                fps=fps,
                width=width,
                height=height,
                duration_in_frames=duration_frames,
                background_path=current_bg, # SNAPSHOT precedente!
                remotion_project_path=remotion_proj,
                shared_bundle_cache_dir=shared_bundle_cache,  # 🎯 RIUSO BUNDLE!
                status_callback=lambda msg, err=False: status_callback(
                    f"[FastRender] [Ent{entity_num}] {msg}", err
                ) if status_callback else None
            )
            
            if not res_clip or not os.path.exists(clip_output):
                logger.error(f"[FastRender] Render fallito per entità {entity_num}")
                continue
                
            clip_paths.append(clip_output)
            
            # Se non è l'ultima, estrai snapshot per la prossima
            if not is_last:
                snapshot_path = os.path.join(temp_work_dir, f"snapshot_{i:02d}.png")
                if status_callback:
                    status_callback(f"[FastRender] 📸 Snapshot entità {entity_num}...")
                
                # ffprobe per durata esatta (per sicurezza)
                try:
                    probe_cmd = [
                        "ffprobe", "-v", "error", "-show_entries", "format=duration",
                        "-of", "default=noprint_wrappers=1:nokey=1", clip_output
                    ]
                    actual_dur_str = subprocess.check_output(probe_cmd).decode().strip()
                    actual_dur = float(actual_dur_str)
                    extract_time = max(0, actual_dur - 0.05)
                    
                    # Estrai frame con ffmpeg
                    snap_cmd = [
                        "ffmpeg", "-y", "-ss", str(extract_time), "-i", clip_output,
                        "-frames:v", "1", "-q:v", "2", snapshot_path
                    ]
                    subprocess.run(snap_cmd, check=True, capture_output=True)
                    
                    if os.path.exists(snapshot_path):
                        current_bg = snapshot_path
                        if status_callback:
                            status_callback(f"[FastRender] ✅ Snapshot creato: {os.path.basename(snapshot_path)}")
                except Exception as e:
                    logger.warning(f"[FastRender] Snapshot fallito per entità {entity_num}: {e}")
                    # Prosegue comunque, userà il background iniziale o griglia remotion
        
        # 4. Concatenazione finale
        if not clip_paths:
            if status_callback:
                status_callback("[FastRender] ❌ Nessun clip generato", True)
            return None
            
        if status_callback:
            status_callback(f"[FastRender] 🔗 Concatenazione {len(clip_paths)} clip...")
            
        # ffmpeg concat demuxer
        concat_list = os.path.join(temp_work_dir, "concat.txt")
        with open(concat_list, "w") as f:
            for p in clip_paths:
                f.write(f"file '{p}'\n")
        
        try:
            concat_cmd = [
                "ffmpeg", "-y", "-f", "concat", "-safe", "0", "-i", concat_list,
                "-c", "copy", output_path
            ]
            subprocess.run(concat_cmd, check=True, capture_output=True)
            
            if os.path.exists(output_path):
                if status_callback:
                    status_callback(f"[FastRender] ✅ Render finale completato: {os.path.basename(output_path)}")
                return VideoFileClip(output_path)
        except Exception as e:
            logger.error(f"[FastRender] Errore concatenazione: {e}")
            if status_callback:
                status_callback(f"[FastRender] ❌ Errore finale: {e}", True)
            
    finally:
        # Pulizia post-render: rimuove i clip intermedi e gli snapshot
        try:
            shutil.rmtree(temp_work_dir, ignore_errors=True)
        except Exception:
            pass
        
        # Cleanup shared bundle cache (only if it was created in this call)
        # Note: we keep it in _BUNDLE_CACHE for reuse in same session
        # It will be cleaned up when worker restarts or process ends
        
    return None


def render_entity_stack(
    entities: List[Dict[str, Any]],
    output_path: str,
    background_path: Optional[str] = None,
    fps: int = 30,
    width: int = 1920,
    height: int = 1080,
    remotion_project_path: Optional[str] = None,
    status_callback: Optional[Any] = None,
    stack_layer_style: Optional[str] = None,
    light_mode: bool = False,
    history_snapshots: Optional[List[Dict[str, Any]]] = None,
) -> Optional[VideoFileClip]:
    # Pulizia /tmp/remotion-webpack-bundle-* prima del render per evitare ENOSPC
    if _should_clean_bundle_cache():
        try:
            import glob
            import shutil
            for bundle_dir in glob.glob("/tmp/remotion-webpack-bundle-*"):
                try:
                    if os.path.isdir(bundle_dir):
                        shutil.rmtree(bundle_dir, ignore_errors=True)
                except Exception:
                    pass
        except Exception:
            pass

    if status_callback:
        status_callback(f"[EntityStack] 🎬 Renderizzazione stack con {len(entities)} entità")

    background_path = _normalize_path_input(background_path)
    
    # Trova progetto Remotion
    if not remotion_project_path:
        remotion_project_path = find_remotion_project()
        if not remotion_project_path:
            logger.error("[EntityStack] ❌ Progetto Remotion non trovato")
            if status_callback:
                status_callback("[EntityStack] ❌ Progetto Remotion non trovato", True)
            return None
    if status_callback:
        status_callback(f"[EntityStack] 📁 Project root: {remotion_project_path}", False)
    
    # Verifica Node.js
    if not check_node_installed():
        logger.error("[EntityStack] ❌ Node.js non installato")
        if status_callback:
            status_callback("[EntityStack] ❌ Node.js non installato", True)
        return None

    # Verifica node_modules e .bin/remotion; evita "npm ERR! could not determine executable to run"
    node_modules_path = os.path.join(remotion_project_path, "node_modules")
    bin_remotion = os.path.join(remotion_project_path, "node_modules", ".bin", "remotion")
    need_npm = not os.path.exists(node_modules_path) or not os.path.isfile(bin_remotion)
    if need_npm:
        auto_npm = os.environ.get("VELOX_AUTO_NPM_INSTALL", "1") == "1"
        if auto_npm:
            try:
                if status_callback:
                    status_callback("[EntityStack] 📦 node_modules o .bin/remotion mancante: avvio npm install...", False)
                pkg_lock = os.path.join(remotion_project_path, "package-lock.json")
                if os.path.exists(pkg_lock):
                    cmd_npm = ["npm", "ci", "--no-audit", "--no-fund"]
                else:
                    cmd_npm = ["npm", "install", "--no-audit", "--no-fund"]
                subprocess.run(cmd_npm, cwd=remotion_project_path, check=True, timeout=900)
                if not os.path.isfile(bin_remotion):
                    logger.warning("[EntityStack] .bin/remotion ancora assente dopo npm install, riprovo npm install")
                    subprocess.run(cmd_npm, cwd=remotion_project_path, check=True, timeout=900)
            except Exception as e:
                logger.error("[EntityStack] ❌ npm install fallito: %s", e)
                if status_callback:
                    status_callback(f"[EntityStack] ❌ npm install fallito: {e}", True)
                return None
        else:
            logger.error("[EntityStack] ❌ node_modules o .bin/remotion non trovato. Esegui: npm install")
            if status_callback:
                status_callback("[EntityStack] ❌ node_modules o .bin/remotion non trovato. Esegui: npm install", True)
            return None

    # Prepara props per EntityStack
    # EntityStack si aspetta "entities" e opzionale "stackLayerStyle" (5 stili: DEFAULT + 4 direzionali)
    # Stile scelto una volta per video (passato dal chiamante); se non passato, random per questa chiamata
    stack_layer_styles = ["DEFAULT", "BOTTOM_TO_TOP", "LEFT_TO_RIGHT", "TOP_TO_BOTTOM"]
    chosen_style = (stack_layer_style or "").strip().upper() or None
    if not chosen_style or chosen_style not in stack_layer_styles:
        chosen_style = random.choice(stack_layer_styles)
        logger.info("[EntityStack] stackLayerStyle random (non passato): %s", chosen_style)
    else:
        logger.info("[EntityStack] stackLayerStyle (video unico): %s", chosen_style)
    remotion_props = {
        "entities": entities,
        "stackLayerStyle": chosen_style,
        "lightMode": light_mode,
    }
    if history_snapshots:
        remotion_props["historySnapshots"] = history_snapshots
    if status_callback:
        status_callback(f"[EntityStack] Stile layer stack: {chosen_style}", False)

    # Log per programma legit: grandezza e posizione date (allineato a EntityStack.tsx)
    date_entities = [e for e in entities if str(e.get("type", "")).upper() == "DATE"]
    if date_entities:
        date_font_px = 200  # Allineato a DATE_FONT_SIZE in EntityStack.tsx
        date_contorno = "position absolute inset 0 (full frame), flex center"
        date_dove = "full frame centrato"
        msg = (
            f"[EntityStack DATE] grandezza date: font {date_font_px}px | contorno: {date_contorno} | "
            f"dove: {date_dove} | num_date={len(date_entities)}"
        )
        logger.info("%s", msg)
        if status_callback:
            status_callback(msg, False)
    if light_mode and status_callback:
        status_callback("[EntityStack] ⚡ lightMode: meno layer, niente video sfondo → render più veloce", False)

    # Copia background in public/ se fornito (saltato in light_mode per velocizzare)
    background_filename = None
    public_dir = os.path.join(remotion_project_path, "public")
    if background_path and os.path.exists(background_path) and not light_mode:
        try:
            os.makedirs(public_dir, exist_ok=True)

            background_path = _ensure_background_compatibility(
                background_path,
                width=int(width),
                height=int(height),
                fps=int(fps),
                public_dir=public_dir,
                status_callback=status_callback,
            )
            
            bg_hash = _asset_hash_for_path(background_path)
            _, bg_ext = os.path.splitext(background_path)
            background_filename = _deterministic_public_name("entitystack_bg", bg_hash, bg_ext)
            background_dest = os.path.join(public_dir, background_filename)
            
            if not os.path.exists(background_dest):
                import shutil
                shutil.copy2(background_path, background_dest)
            
            logger.debug(f"[EntityStack] Background copiato: {background_filename}")
            remotion_props["backgroundVideo"] = background_filename
            if status_callback:
                status_callback(f"[EntityStack] 🎞️ Background utente: /{background_filename}", False)
                status_callback(f"[EntityStack] 🧪 background file: {_file_status(background_dest)}", False)
        except Exception as e:
            logger.warning(f"[EntityStack] Impossibile copiare background: {e}")
            if status_callback:
                status_callback(f"[EntityStack] ⚠️ Background utente non disponibile, fallback griglia.", False)
    else:
        if status_callback:
            status_callback(f"[EntityStack] ℹ️ Nessun background utente, uso fallback griglia.", False)

    # ------------------------------------------------------------------
    # Normalize IMAGE entities so Remotion always receives a URL it can load.
    # - If it's a remote URL, download into Remotion public/ and pass "/file.jpg"
    # - If it's a local file path, copy into public/ and pass "/file.jpg"
    # This prevents falling back to placeholder assets inside TSX defaults.
    # ------------------------------------------------------------------
    try:
        for e in entities:
            if str(e.get("type", "")).upper() != "IMAGE":
                continue

            src = e.get("content")
            if src is None:
                e["content"] = ""
                continue
            src_str = str(src).strip()
            if not src_str or src_str.lower() in ("none", "null"):
                e["content"] = ""
                continue

            normalized = _normalize_entitystack_image(src_str, public_dir, status_callback)
            if normalized:
                e["content"] = normalized
                if status_callback:
                    status_callback(f"[EntityStack] IMAGE normalized: {src_str} -> {normalized}", False)
            else:
                e["content"] = ""
    except Exception as e_norm:
        logger.warning(f"[EntityStack] Normalizzazione immagini fallita: {e_norm}")

    # Calcola durata totale in frames (somma di tutte le durate delle entità)
    # EntityStack calcola la durata internamente sommando le durate delle entità
    # La Composition in Root.tsx ha durationInFrames={1500} che è sufficiente per la maggior parte dei casi
    # Se necessario, questa durata può essere aumentata
    total_duration_frames = sum(e.get('duration', 150) for e in entities)
    if total_duration_frames == 0:
        total_duration_frames = 150  # Default
    
    # Limita la durata massima a 1500 frames (50 secondi a 30fps) per evitare problemi
    # Se il gruppo è più lungo, verrà troncato (dovrebbe essere raro)
    total_duration_frames = min(total_duration_frames, 1500)
    frames_arg = f"0-{max(0, total_duration_frames - 1)}"

    # Log dettagliato delle entità che verranno passate a Remotion
    try:
        entity_summaries = [
            f"{i+1}/{len(entities)} {e.get('type')} | dur={e.get('duration')}f | content='{str(e.get('content'))[:80]}'"
            for i, e in enumerate(entities)
        ]
        logger.info("[EntityStack] Payload Remotion: %s", " || ".join(entity_summaries))
        if status_callback:
            status_callback("[EntityStack] Payload Remotion: " + " || ".join(entity_summaries), False)
    except Exception:
        pass

    if status_callback:
        try:
            approx_s = float(total_duration_frames) / float(fps) if fps else None
        except Exception:
            approx_s = None
        note = (
            f"[EntityStack] ℹ️ Richiesta: entità={len(entities)} | frames={total_duration_frames} | fps={fps}"
            + (f" | durata≈{approx_s:.2f}s" if approx_s is not None else "")
            + " (nota: Remotion può scaricare Chrome/bundlare anche se la durata è corta)."
        )
        status_callback(note, False)
    
    # Usa remotion_headless se disponibile, altrimenti npx remotion
    try:
        # Prova a importare remotion_headless dal progetto Remotion
        sys.path.insert(0, remotion_project_path)
        from remotion_headless import remotion_render, PROJECT_ROOT as REMOTION_ROOT
        sys.path.pop(0)
        
        # Usa remotion_render
        from pathlib import Path as PathLib
        output_path_obj = PathLib(output_path)
        
        if status_callback:
            status_callback(f"[EntityStack] 🎬 Renderizzazione in corso... (durata: {total_duration_frames} frames)")
            status_callback(
                "[EntityStack] ℹ️ La stima «time remaining» di Remotion può scendere a metà render (es. 6 min) "
                "ma il tempo reale dipende dai frame più lenti (video sfondo, immagini, transizioni); spesso il totale è vicino alla stima iniziale (~15–20 min). I worker (Concurrency) restano fissi.",
                False
            )
        
        extra_args = []
        remotion_binaries_dir = _get_remotion_binaries_dir(remotion_project_path)
        env_overrides = _build_remotion_env_overrides(remotion_binaries_dir)
        # Bundle cache: disabilita solo se richiesto esplicitamente (evita copie inutili)
        try:
            if _should_disable_bundle_cache():
                extra_args.append("--bundle-cache=false")
        except Exception:
            pass
        try:
            os.makedirs(remotion_binaries_dir, exist_ok=True)
            if _should_use_binaries_dir(remotion_binaries_dir):
                extra_args.extend(["--binaries-directory", remotion_binaries_dir])
        except Exception:
            pass
        try:
            cache_state = "OFF" if _should_disable_bundle_cache() else "ON"
            chrome_cached = "YES" if _find_cached_chrome(remotion_binaries_dir) else "NO"
            logger.info("[EntityStack] Bundle cache: %s | binaries: %s | chrome cached: %s", cache_state, remotion_binaries_dir, chrome_cached)
            if status_callback:
                status_callback(f"[EntityStack] Bundle cache: {cache_state} | binaries: {remotion_binaries_dir} | chrome cached: {chrome_cached}", False)
        except Exception:
            pass

        # Concurrency: più istanze Chrome in parallelo = render molto più veloce (default 4–8)
        concurrency = _get_render_concurrency()
        logger.info("[EntityStack] Concurrency: %s (parallel Chrome instances)", concurrency)
        if status_callback:
            status_callback(f"[EntityStack] Concurrency: {concurrency}x", False)

        # Usa composition ID "EntityStack" (deve essere registrato in Root.tsx)
        with _TempEnviron(env_overrides):
            result = remotion_render(
                composition_id="EntityStack",
                output_path=output_path_obj,
                props=remotion_props,
                frames=frames_arg,
                extra_args=tuple(extra_args),
                fps=fps,
                width=width,
                height=height,
                codec="h264",
                pixel_format="yuv420p",
                concurrency=concurrency,
                check=False,
            )
        
        if result.returncode != 0:
            logger.error(f"[EntityStack] ❌ Errore rendering: {result.stderr}")
            if status_callback:
                status_callback(f"[EntityStack] ❌ Errore rendering: {result.stderr.decode() if isinstance(result.stderr, bytes) else result.stderr}", True)
            return None
        
    except ImportError:
        # Fallback: binario locale se presente, altrimenti npx remotion (evita "could not determine executable to run")
        remotion_bin = os.path.join(remotion_project_path, "node_modules", ".bin", "remotion")
        if os.path.isfile(remotion_bin):
            cmd_base = [os.path.abspath(remotion_bin)]
            logger.info("[EntityStack] Fallback: usando .bin/remotion")
            if status_callback:
                status_callback(f"[EntityStack] ⚠️ remotion_headless non disponibile, uso .bin/remotion")
        else:
            cmd_base = ["npx", "remotion"]
            logger.info("[EntityStack] Fallback: usando npx remotion")
            if status_callback:
                status_callback(f"[EntityStack] ⚠️ remotion_headless non disponibile, uso npx remotion")

        remotion_binaries_dir = _get_remotion_binaries_dir(remotion_project_path)
        os.makedirs(remotion_binaries_dir, exist_ok=True)
        env_overrides = _build_remotion_env_overrides(remotion_binaries_dir)

        concurrency = _get_render_concurrency()
        logger.info("[EntityStack] Concurrency: %s (fallback)", concurrency)
        if status_callback:
            status_callback(f"[EntityStack] Concurrency: {concurrency}x", False)

        entry_point = _find_remotion_entry_point(remotion_project_path)
        if not entry_point:
            logger.error("[EntityStack] ❌ Entry point Remotion non trovato (index.tsx/ts/jsx/js)")
            if status_callback:
                status_callback("[EntityStack] ❌ Entry point Remotion non trovato (index.tsx/ts/jsx/js)", True)
            return None
        out_str = str(output_path) if output_path else ""
        cmd = cmd_base + [
            "render",
            entry_point,
            "EntityStack",
            out_str,
            "--props",
            json.dumps(remotion_props),
            "--frames",
            frames_arg,
            "--fps", str(fps),
            "--width", str(width),
            "--height", str(height),
            "--codec", "h264",
            "--pixel-format", "yuv420p",
            "--concurrency", str(concurrency),
        ]
        if _should_use_binaries_dir(remotion_binaries_dir):
            cmd += ["--binaries-directory", remotion_binaries_dir]
        try:
            if _should_disable_bundle_cache():
                cmd.append("--bundle-cache=false")
        except Exception:
            pass
        try:
            cache_state = "OFF" if _should_disable_bundle_cache() else "ON"
            chrome_cached = "YES" if _find_cached_chrome(remotion_binaries_dir) else "NO"
            logger.info("[EntityStack] Bundle cache: %s | binaries: %s | chrome cached: %s", cache_state, remotion_binaries_dir, chrome_cached)
            if status_callback:
                status_callback(f"[EntityStack] Bundle cache: {cache_state} | binaries: {remotion_binaries_dir} | chrome cached: {chrome_cached}", False)
        except Exception:
            pass

        try:
            result = _run_with_filtered_progress(
                cmd,
                cwd=remotion_project_path,
                timeout=2700,  # 45 minuti (con concurrency 8–12, ~15s animazione di solito finisce in 8–15 min)
                env=_merge_env(env_overrides),
            )
            
            if result.returncode != 0:
                logger.error(f"[EntityStack] ❌ Errore rendering: {result.stderr}")
                if status_callback:
                    status_callback(f"[EntityStack] ❌ Errore rendering: {result.stderr}", True)
                return None
        except subprocess.TimeoutExpired:
            logger.error("[EntityStack] ❌ Timeout durante rendering")
            if status_callback:
                status_callback("[EntityStack] ❌ Timeout durante rendering", True)
            return None
        except Exception as e:
            logger.error(f"[EntityStack] ❌ Errore: {e}")
            if status_callback:
                status_callback(f"[EntityStack] ❌ Errore: {e}", True)
            return None
    
    # Verifica che il file sia stato creato
    if not os.path.exists(output_path):
        logger.error(f"[EntityStack] ❌ File output non creato: {output_path}")
        if status_callback:
            status_callback(f"[EntityStack] ❌ File output non creato: {output_path}", True)
        return None
    
    # Nota: non rimuoviamo background cached (entitystack_bg_*.mp4).
    # Il nome deterministico evita copie inutili e velocizza i render successivi.
    
    # Optional light fade-out for all Remotion outputs
    if os.environ.get("VELOX_REMOTION_FADEOUT", "1") == "1":
        try:
            fade_seconds = float(os.environ.get("VELOX_REMOTION_FADEOUT_SEC", "0.25"))
        except Exception:
            fade_seconds = 0.25
        _apply_fadeout_inplace(
            str(output_path),
            fade_seconds=fade_seconds,
            fps=int(fps),
            status_callback=status_callback,
        )

    # Carica e restituisci VideoFileClip
    try:
        if status_callback:
            status_callback(f"[EntityStack] ✅ Stack renderizzato: {os.path.basename(output_path)}")
        
        clip = VideoFileClip(output_path)
        return clip
    except Exception as e:
        logger.error(f"[EntityStack] ❌ Errore caricamento video: {e}")
        if status_callback:
            status_callback(f"[EntityStack] ❌ Errore caricamento video: {e}", True)
        return None
