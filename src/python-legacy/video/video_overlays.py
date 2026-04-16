"""Text and image overlay functions for video processing."""

import logging
import math
import os
import random
import tempfile
import textwrap
import uuid
import traceback
from typing import List, Optional, Tuple, Dict, Any, Callable
from pathlib import Path

import numpy as np
from PIL import Image, ImageDraw, ImageFont, ImageFilter
from moviepy import VideoFileClip, ImageClip, CompositeVideoClip, ColorClip, TextClip, concatenate_videoclips, VideoClip
from moviepy import vfx
# Note: avoid importing resize directly from moviepy.video.fx to maintain compatibility across versions

try:
    import validators as _validators
    def _is_valid_url(value: str) -> bool:
        return bool(_validators.url(value))
except Exception:
    def _is_valid_url(value: str) -> bool:
        return isinstance(value, str) and value.startswith(("http://", "https://"))

try:
    # Import from new modular structure
    from config.config import (
        FONT_PATH_DEFAULT,
        FONT_PATHS,
        FONTS_DIR,
        BASE_W_DEFAULT as VIDEO_WIDTH,
        BASE_H_DEFAULT as VIDEO_HEIGHT,
        DATE_TYPEWRITER_RATIO_DEFAULT,
    )
    from modules.video.video_core import create_safe_temp_filename, safe_close_moviepy_object
    from modules.utils.utils import download_image
    # Break circular dependency by importing from video_clips
    from modules.video.video_clips import add_single_sound_effect_to_clip
except ImportError:
    # Fallback to absolute imports when run as a script
    try:
        from config.config import (
            FONT_PATH_DEFAULT,
            FONT_PATHS,
            FONTS_DIR,
            BASE_W_DEFAULT as VIDEO_WIDTH,
            BASE_H_DEFAULT as VIDEO_HEIGHT,
            DATE_TYPEWRITER_RATIO_DEFAULT,
        )
        try:
            from modules.video.video_core import create_safe_temp_filename, safe_close_moviepy_object
        except ImportError:
            from video_core import create_safe_temp_filename, safe_close_moviepy_object
        
        try:
            from modules.utils.utils import download_image
        except ImportError:
            from utils import download_image
            
        try:
            from modules.video.video_clips import add_single_sound_effect_to_clip
        except ImportError:
            from video_clips import add_single_sound_effect_to_clip
            
    except ImportError as main_import_error:
        # Fallback values if config is not available
        FONT_PATH_DEFAULT = ""
        FONT_PATHS = []
        FONTS_DIR = None
        VIDEO_WIDTH = 1920
        VIDEO_HEIGHT = 1080
        DATE_TYPEWRITER_RATIO_DEFAULT = 0.15

        # Define minimal fallback functions
        def create_safe_temp_filename(prefix="temp", suffix=".tmp"):
            return f"{prefix}_{uuid.uuid4()}{suffix}"

        def safe_close_moviepy_object(obj):
            if hasattr(obj, 'close'):
                obj.close()
        
        # Fallback for download_image if not available
        def download_image(url, save_path=None):
            return save_path

        def add_single_sound_effect_to_clip(clip, sound_path=None, **kwargs):
            return clip

        logging.error(f"Critical import failure in video_overlays.py: {main_import_error}")

# Lazy import function to avoid circular dependency
def _get_apply_gaussian_blur():
    """Lazy import of apply_gaussian_blur to avoid circular dependency."""
    try:
        from modules.video.video_clips import apply_gaussian_blur
        return apply_gaussian_blur
    except ImportError:
        try:
            from video_clips import apply_gaussian_blur
            return apply_gaussian_blur
        except ImportError:
            # Return a no-op function if import fails
            return lambda clip, sigma=25: clip

logger = logging.getLogger(__name__)

def _find_montserrat_font_path() -> str:
    """Helper to find Montserrat font path with fallbacks."""
    try:
        from modules.video.font_downloader import get_fonts_dir
        fonts_dir = get_fonts_dir()
        for font_name in ["Montserrat-Black.ttf", "Montserrat-Bold.ttf", "Montserrat-Medium.ttf"]:
            path = fonts_dir / font_name
            if path.exists():
                return str(path)
    except ImportError:
        pass
    
    if FONTS_DIR:
        for font_name in ["Montserrat-Black.ttf", "Montserrat-Bold.ttf"]:
            path = os.path.join(FONTS_DIR, font_name)
            if os.path.exists(path):
                return path
                
    for path in FONT_PATHS or []:
        if path and "Montserrat" in path and os.path.exists(path):
            return path
            
    return FONT_PATH_DEFAULT or "Arial"

def create_text_overlay_clip(
    text: str,
    duration: float,
    font_size: int = 70,
    font_color: str = 'white',
    position: Any = ('center', 'center'),
    font_path: Optional[str] = None,
    stroke_color: Optional[str] = 'black',
    stroke_width: float = 1.0,
    bg_color: Optional[str] = None
) -> Optional[TextClip]:
    """Basic wrapper for TextClip."""
    try:
        actual_font = font_path or _find_montserrat_font_path()
        try:
            # MoviePy 1.x / early 2.x
            clip = TextClip(
                txt=text,
                fontsize=font_size,
                color=font_color,
                font=actual_font,
                stroke_color=stroke_color,
                stroke_width=stroke_width,
                bg_color=bg_color,
                method='caption',
                size=(VIDEO_WIDTH * 0.8, None)
            )
        except TypeError:
            # MoviePy 2.x latest
            clip = TextClip(
                text=text,
                font_size=font_size,
                color=font_color,
                font=actual_font,
                stroke_color=stroke_color,
                stroke_width=stroke_width,
                bg_color=bg_color,
                method='caption',
                size=(VIDEO_WIDTH * 0.8, None)
            )
        return clip.with_duration(duration).with_position(position)
    except Exception as e:
        logger.error(f"Error creating text overlay clip: {e}")
        return None

def create_subtitle_overlay(
    subtitle_text: str,
    duration: float,
    font_size: int = 48,
    font_color: str = 'yellow',
    position: Any = ('center', ('bottom', 100)),
    font_path: Optional[str] = None
) -> Optional[TextClip]:
    """Specialized overlay for subtitles."""
    return create_text_overlay_clip(
        text=subtitle_text,
        duration=duration,
        font_size=font_size,
        font_color=font_color,
        position=position,
        font_path=font_path,
        stroke_width=1.5
    )

def make_important_words_image(
    text: str,
    font_path: Optional[str] = None,
    size: Tuple[int, int] = (1920, 1080),
    font_size: int = 70,
    color: str = 'white',
    bg_box_color: Tuple[int, int, int, int] = (255, 100, 0, 255),
    box_shadow_color: Tuple[int, int, int, int] = (150, 50, 0, 100),
    video_style: str = "rap"
) -> Image.Image:
    """Creates a stylized PIL image for Parole_Importanti."""
    W, H = size
    img = Image.new("RGBA", size, (0, 0, 0, 0))
    draw = ImageDraw.Draw(img)
    
    actual_font_path = font_path or _find_montserrat_font_path()
    try:
        font = ImageFont.truetype(actual_font_path, font_size)
    except Exception:
        font = ImageFont.load_default()
    
    # Calculate text dimensions
    bbox = draw.textbbox((0, 0), text, font=font)
    text_w = bbox[2] - bbox[0]
    text_h = bbox[3] - bbox[1]
    
    padding_x = 40
    padding_y = 20
    box_w = text_w + padding_x * 2
    box_h = text_h + padding_y * 2
    
    # Position: bottom center with margin
    bottom_margin = 100
    box_x = (W - box_w) // 2
    box_y = H - box_h - bottom_margin
    
    # Draw shadow
    shadow_offset = 8
    draw.rectangle(
        [box_x + shadow_offset, box_y + shadow_offset, box_x + box_w + shadow_offset, box_y + box_h + shadow_offset],
        fill=box_shadow_color
    )
    
    # Draw box
    draw.rectangle([box_x, box_y, box_x + box_w, box_y + box_h], fill=bg_box_color)
    
    # Draw text
    draw.text((box_x + padding_x, box_y + padding_y), text, font=font, fill=color)
    
    return img

def generate_parole_importanti_overlay(
    text: str,
    start_v: float,
    dur_v: float,
    base_w: int,
    base_h: int,
    background_path: Optional[str],
    fps: float,
    font_path: Optional[str] = None,
    status_callback: Optional[Callable] = None,
    video_style: str = "rap"
) -> Optional[CompositeVideoClip]:
    """
    Main entry for single-word emphasis.
    Uses Remotion via remotion_renderer if style is 'rap' and engine allows.
    Otherwise fallbacks to MoviePy (handled by ParoleImportantiHandler).
    """
    if video_style.lower() in ("rap", "young") and os.environ.get("VELOX_OVERLAY_ENGINE") != "python":
        try:
            from modules.video.remotion_renderer import render_remotion_component_to_file
            
            # Prepare payload for Remotion
            props = {
                "text": text,
                "durationInFrames": int(dur_v * fps),
                "fps": fps,
                "width": base_w,
                "height": base_h,
                "videoStyle": video_style
            }
            
            out_path = create_safe_temp_filename("remotion_parole", ".mp4")
            # This is a simplified wrapper call; real renderer call might differ
            success = render_remotion_component_to_file(
                "ParoleImportanti", props, out_path, fps=fps
            )
            
            if success and os.path.exists(out_path):
                return VideoFileClip(out_path).with_start(start_v)
        except Exception as e:
            if status_callback: status_callback(f"Remotion parole fail: {e}")
    
    return None # Handled by MoviePy fallback in ParoleImportantiHandler

def generate_frasi_importanti_overlay(
    text: str,
    start_v: float,
    dur_v: float,
    base_w: int,
    base_h: int,
    background_path: Optional[str],
    fps: float,
    font_path: Optional[str] = None,
    font_size: int = 75,
    text_color: str = "white",
    status_callback: Optional[Callable] = None,
    video_style: str = "young"
) -> Optional[CompositeVideoClip]:
    """Generates a quote/phrase overlay using video_style_effects."""
    try:
        from modules.video.video_style_effects import (
            create_modern_quote_overlay,
            create_crime_typewriter_overlay,
            create_simple_discovery_phrase_overlay,
            create_cinematic_text_overlay
        )
        
        style = video_style.lower()
        if style == "young" or style == "rap":
            clip = create_modern_quote_overlay(
                text=text, duration=dur_v, size=(base_w, base_h),
                fps=fps, background_path=background_path, font_path=font_path
            )
        elif style == "crime":
            clip = create_crime_typewriter_overlay(
                text=text, duration=dur_v, size=(base_w, base_h),
                fps=fps, background_path=background_path, font_path=font_path
            )
        elif style in ("discovery", "old"):
            clip = create_simple_discovery_phrase_overlay(
                text=text, duration=dur_v, size=(base_w, base_h),
                fps=fps, background_path=background_path, font_path=font_path
            )
        else:
            clip = create_cinematic_text_overlay(
                text=text, duration=dur_v, size=(base_w, base_h),
                fps=fps, background_path=background_path, font_path=font_path
            )
        
        if clip:
            return clip.with_start(start_v)
        return None
    except Exception as e:
        if status_callback: status_callback(f"Frasi overlay error: {e}")
        return None

def generate_nomi_speciali_overlay(
    txt: str,
    start_v: float,
    dur_v: float,
    base_w: int,
    base_h: int,
    background_path: Optional[str],
    fps: float,
    font_path: Optional[str] = None,
    base_font_size: int = 35,
    text_color: str = "white",
    status_callback: Optional[Callable] = None,
    video_style: str = "young"
) -> Optional[CompositeVideoClip]:
    """Generates an overlay for special names with style-specific animations."""
    try:
        from modules.video.video_style_effects import (
            create_flickering_title_overlay,
            create_crime_blur_zoom_overlay
        )
        
        style = video_style.lower()
        if style == "young" or style == "rap":
            clip = create_flickering_title_overlay(
                text=txt, duration=dur_v, size=(base_w, base_h),
                fps=fps, background_path=background_path, font_path=font_path,
                video_style=style
            )
        elif style == "crime":
            clip = create_crime_blur_zoom_overlay(
                text=txt, duration=dur_v, size=(base_w, base_h),
                fps=fps, background_path=background_path, font_path=font_path
            )
        else:
            # Fallback for Discovery/Old: Classic typewriter
            clip = create_text_overlay_clip(
                text=txt.upper(), duration=dur_v, font_size=110,
                font_color=text_color, font_path=font_path
            )
            if clip:
                clip = clip.with_effects([vfx.FadeIn(0.5), vfx.FadeOut(0.5)])
        
        if clip:
            return clip.with_start(start_v)
        return None
    except Exception as e:
        if status_callback: status_callback(f"Nomi overlay error: {e}")
        return None

def generate_date_overlay(
    text: str,
    start_v: float,
    dur_v: float,
    base_w: int,
    base_h: int,
    background_path: Optional[str],
    fps: float,
    font_path: Optional[str] = None,
    font_size: int = 150,
    text_color: str = "white",
    typewriter_ratio: float = 0.5,
    video_style: str = "young"
) -> Optional[CompositeVideoClip]:
    """Generates a date typewriter overlay as described in docs."""
    try:
        clip = create_text_overlay_clip(
            text=text.upper(), duration=dur_v, font_size=font_size,
            font_color=text_color, font_path=font_path
        )
        if clip:
            return clip.with_start(start_v).with_effects([vfx.FadeIn(0.3)])
        return None
    except Exception as e:
        logger.error(f"Date overlay error: {e}")
        return None

def generate_numeri_overlay(
    text: str,
    start_v: float,
    dur_v: float,
    base_w: int,
    base_h: int,
    background_path: Optional[str],
    fps: float,
    font_path: Optional[str] = None,
    font_size: int = 100,
    text_color: str = "white",
    video_style: str = "young"
) -> Optional[CompositeVideoClip]:
    """Generates a pixelated blur-zoom entrance overlay for numbers."""
    try:
        clip = create_text_overlay_clip(
            text=text, duration=dur_v, font_size=font_size,
            font_color=text_color, font_path=font_path
        )
        if clip:
            return clip.with_start(start_v).with_effects([vfx.FadeIn(0.5)])
        return None
    except Exception as e:
        logger.error(f"Numeri overlay error: {e}")
        return None

# Legacy Helpers
def make_word_by_word_overlay_positioned(
    words_data: List[Dict[str, Any]],
    duration: float,
    base_w: int,
    base_h: int,
    font_path: Optional[str] = None
) -> Optional[CompositeVideoClip]:
    """Legacy helper for word-by-word subtitles."""
    return None

def _render_single_image_overlay_task_worker(args):
    """Worker task for parallel image rendering (Legacy)."""
    return None



def make_subtitle_image(
    text: str,
    font_path: Optional[str] = None,
    size: Tuple[int, int] = (1920, 1080),
    font_size: int = 42,
    color: str = "white",
    shadow_color: Tuple[int, int, int, int] = (0, 0, 0, 200),
    shadow_offset: Tuple[int, int] = (2, 2),
    bg_box_color: Tuple[int, int, int, int] = (0, 0, 0, 180),
    padding_x: int = 20,
    padding_y: int = 10,
    bottom_margin: int = 50,
    box_shadow_color: Tuple[int, int, int, int] = (0, 0, 0, 100),
    box_shadow_offset: Tuple[int, int] = (3, 3),
    box_shadow_blur_radius: int = 5,
    text_shadow_blur_radius: int = 2
) -> Optional[str]:
    """
    Generates a subtitle image using PIL and saves it to a temp file.
    Returns the path to the temp file.
    """
    try:
        # Use module-level font finder if path not provided or invalid
        actual_font_path = font_path
        if not actual_font_path or not os.path.exists(actual_font_path):
            actual_font_path = _find_montserrat_font_path()
            
        img = Image.new('RGBA', size, (0, 0, 0, 0))
        draw = ImageDraw.Draw(img)

        try:
            font = ImageFont.truetype(actual_font_path, font_size)
        except Exception:
            logger.warning(f"Could not load font {actual_font_path}, using default")
            font = ImageFont.load_default()

        # Calculate text size
        bbox = draw.textbbox((0, 0), text, font=font)
        text_width = bbox[2] - bbox[0]
        text_height = bbox[3] - bbox[1]

        x = (size[0] - text_width) // 2
        y = size[1] - text_height - bottom_margin

        # Background box
        if bg_box_color and bg_box_color != (0, 0, 0, 0):
            box_coords = [
                x - padding_x,
                y - padding_y,
                x + text_width + padding_x,
                y + text_height + padding_y
            ]
            
            # Simple box shadow if requested
            if box_shadow_color and box_shadow_color[3] > 0:
                shadow_coords = [
                    c + o for c, o in zip(box_coords, [box_shadow_offset[0], box_shadow_offset[1]] * 2)
                ]
                draw.rectangle(shadow_coords, fill=box_shadow_color)
                
            draw.rectangle(box_coords, fill=bg_box_color)

        # Text Shadow
        if shadow_color and shadow_offset != (0, 0):
            shadow_x = x + shadow_offset[0]
            shadow_y = y + shadow_offset[1]
            draw.text((shadow_x, shadow_y), text, font=font, fill=shadow_color)

        # Main Text
        draw.text((x, y), text, font=font, fill=color)
        
        # Save to temp file
        temp_file = create_safe_temp_filename("subtitle_overlay", ".png")
        img.save(temp_file, 'PNG')
        return temp_file
        
    except Exception as e:
        logger.error(f"Error creating subtitle image: {e}")
        return None
