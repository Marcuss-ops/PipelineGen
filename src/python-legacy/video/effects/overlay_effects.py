import logging
import os
import random
import shutil
import subprocess
from typing import Optional

logger = logging.getLogger(__name__)

DEFAULT_EFFECTS_FOLDER = "/home/pierone/Pyt/VeloxEditing/refactored/effects/EffettiVisiv"


def get_random_effect(effects_folder: str = DEFAULT_EFFECTS_FOLDER) -> Optional[str]:
    if not os.path.exists(effects_folder):
        logger.warning(f"Effects folder not found: {effects_folder}")
        return None
    effect_files = [
        name for name in os.listdir(effects_folder)
        if name.endswith((".mp4", ".mov", ".webm"))
    ]
    if not effect_files:
        logger.warning(f"No effect files found in {effects_folder}")
        return None
    chosen = random.choice(effect_files)
    effect_path = os.path.join(effects_folder, chosen)
    logger.info(f"Selected random effect: {chosen}")
    return effect_path


def apply_overlay_effect(
    clip_path: str,
    effect_path: str,
    output_path: str,
    opacity: float = 0.3,
    blend_mode: str = "overlay",
) -> bool:
    ffmpeg = shutil.which("ffmpeg") or "ffmpeg"
    ffprobe = shutil.which("ffprobe") or "ffprobe"
    cmd_probe = [
        ffprobe, "-v", "error", "-show_entries", "format=duration",
        "-of", "default=noprint_wrappers=1:nokey=1",
        clip_path,
    ]
    try:
        result = subprocess.run(cmd_probe, capture_output=True, text=True, timeout=10)
        clip_duration = float(result.stdout.strip()) if result.stdout.strip() else 5
    except Exception:
        clip_duration = 5
    if not os.path.exists(effect_path):
        logger.error(f"Effect file not found: {effect_path}")
        return False
    filter_complex = (
        f"[0:v]scale=1920:1080:force_original_aspect_ratio=decrease,"
        f"pad=1920:1080:(ow-iw)/2:(oh-ih)/2:black[base];"
        f"[1:v]scale=1920:1080:force_original_aspect_ratio=decrease,"
        f"pad=1920:1080:(ow-iw)/2:(oh-ih)/2:black,"
        f"loop={int(clip_duration)+1}:1:0[eff];"
        f"[base][eff]blend={blend_mode}:all_mode={blend_mode}:all_opacity={opacity}"
    )
    cmd = [
        ffmpeg, "-y", "-i", clip_path, "-i", effect_path,
        "-filter_complex", filter_complex,
        "-t", str(clip_duration),
        "-c:v", "libx264", "-preset", "fast", "-crf", "18",
        "-an", output_path,
    ]
    try:
        result = subprocess.run(cmd, capture_output=True, timeout=120)
        if result.returncode != 0:
            simple_filter = (
                f"[0:v]scale=1920:1080[base];"
                f"[1:v]scale=1920:1080,loop={int(clip_duration)+1}:1:0,"
                f"format=rgba,colorchannelmixer=rr=0.5:rg=0.5:rb=0.5:ra={opacity}[eff];"
                f"[base][eff]overlay=0:0:format=auto"
            )
            cmd_simple = [
                ffmpeg, "-y", "-i", clip_path, "-i", effect_path,
                "-filter_complex", simple_filter,
                "-t", str(clip_duration),
                "-c:v", "libx264", "-preset", "fast", "-crf", "18",
                "-an", output_path,
            ]
            result = subprocess.run(cmd_simple, capture_output=True, timeout=120)
            if result.returncode != 0:
                logger.error(f"Overlay effect failed: {result.stderr.decode()[:200]}")
                return False
        return os.path.exists(output_path) and os.path.getsize(output_path) > 0
    except Exception as exc:
        logger.error(f"Error applying overlay effect: {exc}")
        return False
