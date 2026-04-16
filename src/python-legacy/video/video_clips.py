"""Video clip management functions for concatenation, resizing, and looping."""

import gc
import logging
import os
import random
import shutil
import subprocess
import threading
import time
import traceback
import uuid
from typing import Any, Dict, List, Optional, Callable, Tuple

import pysrt
from modules.video.translation_fallback import translate_texts_google_with_fallback
from moviepy import (AudioFileClip, ColorClip, CompositeVideoClip,
                     ImageClip, VideoFileClip, concatenate_audioclips,
                     concatenate_videoclips, vfx)

# Initialize logger early to use in import error handling
logger = logging.getLogger(__name__)


def _fallback_make_subtitle_image(
    text: str,
    font_path: str,
    size: Tuple[int, int],
    font_size: int,
    color: str,
    shadow_color: Tuple[int, int, int, int],
    shadow_offset: Tuple[int, int],
    bg_box_color: Tuple[int, int, int, int],
    padding_x: int,
    padding_y: int,
    bottom_margin: int,
    box_shadow_color: Tuple[int, int, int, int],
    box_shadow_offset: Tuple[int, int],
    box_shadow_blur_radius: int,
    text_shadow_blur_radius: int
) -> Optional[str]:
    """
    Lightweight subtitle image generator used when the advanced overlay module
    is unavailable. Returns the path to a temporary PNG file.
    """
    try:
        from PIL import Image, ImageDraw, ImageFont
        import tempfile

        img = Image.new('RGBA', size, (0, 0, 0, 0))
        draw = ImageDraw.Draw(img)

        try:
            if font_path and os.path.exists(font_path):
                font = ImageFont.truetype(font_path, font_size)
            else:
                font = ImageFont.load_default()
        except Exception:
            font = ImageFont.load_default()

        bbox = draw.textbbox((0, 0), text, font=font)
        text_width = bbox[2] - bbox[0]
        text_height = bbox[3] - bbox[1]

        x = (size[0] - text_width) // 2
        y = size[1] - text_height - bottom_margin

        if bg_box_color and bg_box_color != (0, 0, 0, 0):
            box_coords = [
                x - padding_x,
                y - padding_y,
                x + text_width + padding_x,
                y + text_height + padding_y
            ]
            draw.rectangle(box_coords, fill=bg_box_color)

        if shadow_color and shadow_offset != (0, 0):
            shadow_x = x + shadow_offset[0]
            shadow_y = y + shadow_offset[1]
            draw.text((shadow_x, shadow_y), text, font=font, fill=shadow_color)

        draw.text((x, y), text, font=font, fill=color)

        temp_file = tempfile.NamedTemporaryFile(suffix='.png', delete=False)
        img.save(temp_file.name, 'PNG')
        temp_file.close()
        return temp_file.name
    except Exception as exc:
        logging.getLogger(__name__).warning(
            "Fallback subtitle image creation failed: %s", exc
        )
        return None

# Absolute imports moved to try/except block below

from config.config import (
    CATEGORIZED_SOUND_EFFECTS, FONT_PATH_DEFAULT,
    SUBTITLE_BURNIN_TIMEOUT_DEFAULT, TRANSITION_PATHS
)

from modules.video.video_core import (
    aggressive_memory_cleanup, hard_cleanup,
    safe_close_moviepy_object, get_clip_duration
)

try:
    from utils import safe_remove
except ImportError:
    def safe_remove(path):
        try:
            if os.path.exists(path):
                os.remove(path)
        except:
            pass

try:
    from video_audio import detect_clip_language_safe
except ImportError:
    def detect_clip_language_safe(*args, **kwargs):
        return None

try:
    from transition_downloader import get_all_available_transitions
except ImportError:
    def get_all_available_transitions():
        return []

try:
    from video_ffmpeg import run_ffmpeg_command
except ImportError:
    def run_ffmpeg_command(cmd, stage_desc, status_callback=print, progress=False, timeout=None):
        import subprocess
        try:
            if status_callback:
                status_callback(f"Starting {stage_desc}...")
            result = subprocess.run(cmd, capture_output=True, text=True, timeout=timeout)
            if result.returncode == 0:
                if status_callback:
                    status_callback(f"{stage_desc} completed successfully")
            else:
                if status_callback:
                    status_callback(f"{stage_desc} failed")
            return result
        except Exception as e:
            if status_callback:
                status_callback(f"{stage_desc} error: {e}")
            raise

# Define transition and sound effect functions as stubs (they may not exist in video_effects due to circular import)
def create_transition_clip(*args, **kwargs):
    """Stub function for transition creation."""
    return None

def create_random_transition_with_sound(*args, **kwargs):
    """Stub function for random transition with sound."""
    return None

def add_single_sound_effect_to_clip(clip, sound_path, volume=1.0, position='center'):
    """Stub function for adding sound effects - returns clip unchanged."""
    return clip

# Import transcribe_clip_audio separately to ensure it's always available
try:
    from modules.audio.audio_processing import transcribe_clip_audio
    logger.info("✅ Successfully imported transcribe_clip_audio from audio_processing")
except ImportError as e:
    logger.error(f"❌ Failed to import transcribe_clip_audio: {e}")
    def transcribe_clip_audio(clip_path, status_callback, label, processing_params=None):
        # Fallback: return empty transcription if import failed
        if status_callback:
            status_callback(f"[{label}] ❌ Trascrizione non disponibile (import fallito)", True)
        logger.error("transcribe_clip_audio fallback called - audio_processing import failed")
        return []
    
    # Use fallback subtitle image when import fails
    make_subtitle_image = _fallback_make_subtitle_image

else:
    _external_make_subtitle_image = None
    try:
        from video_overlays import make_subtitle_image as _external_make_subtitle_image
    except ImportError:
        try:
            from refactored.video_overlays import make_subtitle_image as _external_make_subtitle_image  # type: ignore
        except ImportError:
            _external_make_subtitle_image = None

    if _external_make_subtitle_image:
        make_subtitle_image = _external_make_subtitle_image
    else:
        make_subtitle_image = _fallback_make_subtitle_image

# Logger already defined at top of file

def format_timestamp(seconds):
    """Formatta un tempo in secondi nel formato SRT (hh:mm:ss,mmm)."""
    hours = int(seconds // 3600)
    minutes = int((seconds % 3600) // 60)
    secs = int(seconds % 60)
    milliseconds = int((seconds - int(seconds)) * 1000)
    return f"{hours:02d}:{minutes:02d}:{secs:02d},{milliseconds:03d}"


def concatenate_video_clips(
    video_paths: List[str], 
    output_path: str, 
    status_callback: Optional[Callable[[str], None]] = None,
    use_transitions: bool = False,
    transition_duration: float = 1.0
) -> bool:
    """Concatenate video clips with optional transitions."""
    clips = []
    transition_clips = []
    
    try:
        if status_callback:
            status_callback("Loading video clips for concatenation...")
        
        # Load all video clips
        for i, video_path in enumerate(video_paths):
            if not os.path.exists(video_path):
                logger.warning(f"Video file not found: {video_path}")
                continue
            
            try:
                clip = VideoFileClip(video_path)
                clips.append(clip)
                
                if status_callback:
                    status_callback(f"Loaded clip {i+1}/{len(video_paths)}: {os.path.basename(video_path)}")
                
                # Add transition between clips (except for the last clip)
                if use_transitions and i < len(video_paths) - 1:
                    transition = create_transition_clip(transition_duration)
                    if transition:
                        transition_clips.append(transition)
                
            except Exception as e:
                logger.error(f"Error loading video clip {video_path}: {e}")
                if status_callback:
                    status_callback(f"Error loading {os.path.basename(video_path)}: {str(e)}")
                continue
        
        if not clips:
            logger.error("No valid video clips to concatenate")
            return False
        
        # Prepare clips for concatenation
        final_clips = []
        for i, clip in enumerate(clips):
            final_clips.append(clip)
            
            # Add transition after each clip (except the last)
            if use_transitions and i < len(transition_clips):
                final_clips.append(transition_clips[i])
        
        if status_callback:
            status_callback("Concatenating video clips...")
        
        # Concatenate all clips
        final_video = concatenate_videoclips(final_clips, method="compose")
        
        if status_callback:
            status_callback("Writing concatenated video...")
        
        # Assicura che output_path sia assoluto e che la directory esista
        output_path = os.path.abspath(output_path)
        output_dir = os.path.dirname(output_path) or os.getcwd()
        os.makedirs(output_dir, exist_ok=True)
        
        # Cambia directory per i file temporanei
        original_cwd = os.getcwd()
        try:
            os.chdir(output_dir)
            # Write final video
            final_video.write_videofile(
                output_path,
                fps=30,
                codec='libx264',
                audio_codec='aac'
            )
        finally:
            os.chdir(original_cwd)
        
        # Cleanup
        safe_close_moviepy_object(final_video, "final_video")
        for clip in clips:
            safe_close_moviepy_object(clip, "clip")
        for transition in transition_clips:
            safe_close_moviepy_object(transition, "transition")
        
        aggressive_memory_cleanup()
        
        if status_callback:
            status_callback(f"Video concatenation completed: {output_path}")
        
        return os.path.exists(output_path)
        
    except Exception as e:
        logger.error(f"Error concatenating video clips: {e}")
        if status_callback:
            status_callback(f"Concatenation error: {str(e)}")
        
        # Cleanup on error
        for clip in clips:
            safe_close_moviepy_object(clip, "clip")
        for transition in transition_clips:
            safe_close_moviepy_object(transition, "transition")
        
        return False


def resize_video_to_standard(video_clip: VideoFileClip) -> VideoFileClip:
    """Resize video clip to standard dimensions (1920x1080)."""
    try:
        target_width, target_height = 1920, 1080
        current_width, current_height = video_clip.size
        
        # Calculate scaling to fit within target dimensions
        scale_w = target_width / current_width
        scale_h = target_height / current_height
        scale = min(scale_w, scale_h)
        
        # Resize video
        new_width = int(current_width * scale)
        new_height = int(current_height * scale)
        
        resized_clip = video_clip.resize((new_width, new_height))
        
        # Center the video if it doesn't fill the entire frame
        if new_width != target_width or new_height != target_height:
            from moviepy import ColorClip, CompositeVideoClip
            
            # Create black background
            background = ColorClip(
                size=(target_width, target_height),
                color=(0, 0, 0),
                duration=video_clip.duration
            )
            
            # Center the resized video
            x_offset = (target_width - new_width) // 2
            y_offset = (target_height - new_height) // 2
            
            final_clip = CompositeVideoClip([
                background,
                resized_clip.set_position((x_offset, y_offset))
            ])
            
            safe_close_moviepy_object(background, "background")
            safe_close_moviepy_object(resized_clip, "resized_clip")
            
            return final_clip
        else:
            return resized_clip
            
    except Exception as e:
        logger.error(f"Error resizing video: {e}")
        return video_clip


def loop_video_clip(video_clip: VideoFileClip, target_duration: float) -> VideoFileClip:
    """Loop video clip to reach target duration."""
    try:
        if video_clip.duration >= target_duration:
            return video_clip.subclip(0, target_duration)
        
        # Calculate how many loops we need
        loops_needed = int(target_duration / video_clip.duration) + 1
        
        # Create looped clips
        looped_clips = []
        remaining_duration = target_duration
        
        for i in range(loops_needed):
            if remaining_duration <= 0:
                break
            
            if remaining_duration >= video_clip.duration:
                looped_clips.append(video_clip)
                remaining_duration -= video_clip.duration
            else:
                # Partial clip for the remaining duration
                partial_clip = video_clip.subclip(0, remaining_duration)
                looped_clips.append(partial_clip)
                remaining_duration = 0
        
        if len(looped_clips) == 1:
            return looped_clips[0]
        else:
            final_clip = concatenate_videoclips(looped_clips)
            
            # Cleanup partial clips
            for clip in looped_clips[1:]:  # Don't close the original clip
                if clip != video_clip:
                    safe_close_moviepy_object(clip, "partial_clip")
            
            return final_clip
            
    except Exception as e:
        logger.error(f"Error looping video clip: {e}")
        return video_clip


def loop_audio_clip(audio_clip, target_duration: float):
    """Loop audio clip to reach target duration."""
    try:
        if audio_clip.duration >= target_duration:
            return audio_clip.subclip(0, target_duration)
        
        # Calculate how many loops we need
        loops_needed = int(target_duration / audio_clip.duration) + 1
        
        # Create looped clips
        looped_clips = []
        remaining_duration = target_duration
        
        for i in range(loops_needed):
            if remaining_duration <= 0:
                break
            
            if remaining_duration >= audio_clip.duration:
                looped_clips.append(audio_clip)
                remaining_duration -= audio_clip.duration
            else:
                # Partial clip for the remaining duration
                partial_clip = audio_clip.subclip(0, remaining_duration)
                looped_clips.append(partial_clip)
                remaining_duration = 0
        
        if len(looped_clips) == 1:
            return looped_clips[0]
        else:
            final_clip = concatenate_audioclips(looped_clips)
            
            # Cleanup partial clips
            for clip in looped_clips[1:]:  # Don't close the original clip
                if clip != audio_clip:
                    safe_close_moviepy_object(clip, "partial_audio_clip")
            
            return final_clip
            
    except Exception as e:
        logger.error(f"Error looping audio clip: {e}")
        return audio_clip



    
def apply_gaussian_blur(clip, sigma=25):
    import cv2
    import numpy as np
    import logging

    # Validate clip
    if clip is None:
        logging.error("[BlurFX] Input clip is None. Cannot apply blur.")
        return None
    if hasattr(clip, 'reader') and clip.reader is None:
        logging.error("[BlurFX] Clip reader is None. Cannot apply blur.")
        return None
    if not hasattr(clip, 'get_frame'):
        logging.error("[BlurFX] Clip lacks get_frame method. Cannot apply blur.")
        return None
    if not hasattr(clip, 'duration') or clip.duration is None or clip.duration <= 0:
        logging.error("[BlurFX] Clip has invalid duration: {}. Cannot apply blur.".format(getattr(clip, 'duration', 'None')))
        return None

    sigma = max(0.1, sigma)
    k_size = int(sigma * 4 + 1)
    kernel_size = (k_size if k_size % 2 != 0 else k_size + 1,
                   k_size if k_size % 2 != 0 else k_size + 1)

    def ensure_valid_blur_input(frame):
        if frame is None:
            logging.error("[BlurFX] Frame is None. Cannot process.")
            return None
        if not isinstance(frame, np.ndarray):
            logging.warning(f"[BlurFX] Frame type {type(frame)} not np.ndarray. Attempting conversion.")
            try:
                frame = np.array(frame)
            except Exception as conv_err:
                logging.error(f"[BlurFX] Failed to convert frame to np array: {conv_err}")
                return None
        if frame.ndim == 2:
            frame = cv2.cvtColor(frame, cv2.COLOR_GRAY2RGB)
        elif frame.ndim == 3:
            if frame.shape[2] == 1:
                frame = cv2.cvtColor(frame, cv2.COLOR_GRAY2RGB)
            elif frame.shape[2] == 4:
                frame = cv2.cvtColor(frame, cv2.COLOR_RGBA2RGB)
            elif frame.shape[2] != 3:
                logging.error(f"[BlurFX] Unexpected channel count: {frame.shape[2]}")
                return None
        else:
            logging.error(f"[BlurFX] Unexpected frame dimensions: {frame.ndim} (shape: {frame.shape})")
            return None
        if frame.dtype != np.uint8:
            if np.issubdtype(frame.dtype, np.floating):
                if frame.max() <= 1.0 and frame.min() >= 0.0:
                    frame = (frame * 255).astype(np.uint8)
                elif frame.max() <= 255.0 and frame.min() >= 0.0:
                    frame = frame.astype(np.uint8)
                else:
                    logging.warning(f"[BlurFX] Float frame (min:{frame.min()}, max:{frame.max()}) non normalizzato, conversione a uint8 potrebbe essere errata.")
                    frame = np.clip(frame, 0, 255).astype(np.uint8)
            else:
                try:
                    frame = frame.astype(np.uint8)
                except (ValueError, TypeError) as e_astype:
                    logging.error(f"[BlurFX] Could not convert frame dtype {frame.dtype} to uint8 safely: {e_astype}")
                    return None
        return frame

    def blur_frame_func(get_frame, t):
        try:
            original_frame_data = get_frame(t)
            if original_frame_data is None:
                logging.error(f"[BlurFX] get_frame({t}) returned None.")
                clip_w, clip_h = getattr(clip, 'size', (100, 100))
                return np.zeros((clip_h, clip_w, 3), dtype=np.uint8)

            frame_to_process = ensure_valid_blur_input(original_frame_data)
            if frame_to_process is None:
                logging.error(f"[BlurFX] Frame at t={t} invalid for blurring. Returning black frame.")
                clip_w, clip_h = getattr(clip, 'size', (100, 100))
                return np.zeros((clip_h, clip_w, 3), dtype=np.uint8)

            if frame_to_process.shape[0] == 0 or frame_to_process.shape[1] == 0:
                logging.error(f"[BlurFX] Frame at t={t} has zero dimension (shape: {frame_to_process.shape}). Returning black frame.")
                clip_w, clip_h = getattr(clip, 'size', (100, 100))
                return np.zeros((clip_h, clip_w, 3), dtype=np.uint8)

            return cv2.GaussianBlur(frame_to_process, kernel_size, sigma)

        except Exception as e_blur_generic:
            logging.error(f"[BlurFX] Generic error in blur_frame_func at t={t}: {e_blur_generic}", exc_info=True)
            # Avoid referencing original_frame_data if not set
            clip_w, clip_h = getattr(clip, 'size', (100, 100))
            return np.zeros((clip_h, clip_w, 3), dtype=np.uint8)

    return clip.transform(blur_frame_func)



def process_single_middle_clip_ffmpeg_first(
    clip_path: str,
    sound_effects_list: List[str],
    temp_dir: str,
    clip_index: int,
    target_language: Optional[str] = None,
    base_w: int = 1920,
    base_h: int = 1080,
    base_fps: float = 30.0,
    status_callback: Callable[[str, bool], None] = lambda m, e=False: None,
    raise_translation_errors: bool = False,
    ffmpeg_preset_moviepy: str = "medium",
    ffmpeg_preset_final: str = "medium",
    crf_final: int = 22,
    timeout_translation: float = 5.0,
    timeout_ffmpeg: float = 60.0,
    timeout_burnin: float = SUBTITLE_BURNIN_TIMEOUT_DEFAULT,
    processing_params: Optional[Dict[str, Any]] = None
) -> Optional[str]:
    """
    FFmpeg-first version of process_single_middle_clip to avoid MoviePy freezes.
    Uses direct FFmpeg commands for resize, effects, and sound mixing.
    Now adds TWO random sound effects: one at the beginning and one at the end with transition.
    """
    label = f"MiddleClip#{clip_index+1}:{os.path.basename(clip_path)}"
    status_callback(f"--- [{label}] Inizio FFmpeg-first process ---", False)

    if not os.path.exists(clip_path):
        status_callback(f"[{label}] ❌ Clip non trovata: {clip_path}", True)
        return None
    
    # Assicura che temp_dir sia assoluto e che esista
    temp_dir = os.path.abspath(temp_dir)
    os.makedirs(temp_dir, exist_ok=True)

    uid = uuid.uuid4().hex[:8]
    sfx_path = os.path.abspath(os.path.join(temp_dir, f"mid_{clip_index}_{uid}_sfx.mp4"))
    subbed_path = os.path.abspath(os.path.join(temp_dir, f"mid_{clip_index}_{uid}_subbed.mp4"))
    orig_srt = os.path.abspath(os.path.join(temp_dir, f"mid_sub_orig_{clip_index}_{uid}.srt"))
    trans_srt = os.path.abspath(os.path.join(temp_dir, f"mid_sub_trans_{target_language or 'na'}_{clip_index}_{uid}.srt"))
    final_path = os.path.abspath(os.path.join(temp_dir, f"mid_{clip_index}_{uid}_final.mp4"))
    temp_files = [sfx_path, subbed_path, orig_srt, trans_srt]

    try:
        # Phase 1: FFmpeg-first resize + effects (NO MoviePy)
        status_callback(f"[{label}] Phase 1: FFmpeg resize + effects with TWO sound effects", False)

        # Build FFmpeg command for resize + fade + TWO sound effects
        ffmpeg_cmd = [
            "ffmpeg", "-y", "-hide_banner", "-loglevel", "warning",
            "-i", clip_path
        ]

        # Add TWO sound effects if available
        sound_effect_inputs = []
        try:
            # Get sound effects for general category:
            # 1) prefer explicit list passed to the function
            # 2) fallback to config categorized effects
            general_effects = sound_effects_list or CATEGORIZED_SOUND_EFFECTS.get("general", [])
            
            # Filter to only existing files
            existing_general_effects = [effect for effect in general_effects if os.path.exists(effect)]

            # If none are available, try auto-download (keeps logs minimal)
            if len(existing_general_effects) == 0:
                try:
                    from modules.video.sound_effect_downloader import (
                        check_and_download_sound_effects_with_logging,
                        get_sound_effects_dir,
                    )

                    status_callback(f"[{label}] ⬇️ Scarico effetti sonori mancanti (se necessario)...", False)
                    downloaded_paths = check_and_download_sound_effects_with_logging(
                        status_callback=status_callback,
                        label=f"[{label}][SFX]",
                        quiet=True,
                    )

                    # Prefer returned paths; if empty, scan the standard directory.
                    existing_general_effects = [p for p in downloaded_paths if os.path.exists(p)]
                    if len(existing_general_effects) == 0:
                        try:
                            sfx_dir = get_sound_effects_dir()
                            allowed_ext = {".wav", ".mp3", ".m4a", ".aac", ".ogg"}
                            existing_general_effects = sorted(
                                str(p) for p in sfx_dir.iterdir()
                                if p.is_file() and p.suffix.lower() in allowed_ext and p.stat().st_size > 0
                            )
                        except Exception:
                            # Directory scan is best-effort; we'll fall back to no SFX.
                            existing_general_effects = []
                except Exception as e_dl:
                    status_callback(f"[{label}] ⚠️ Download automatico effetti sonori fallito: {e_dl}", True)
            
            if len(existing_general_effects) >= 2:
                # Select TWO different random effects
                selected_effects = random.sample(existing_general_effects, 2)
                for effect_file in selected_effects:
                    ffmpeg_cmd.extend(["-i", effect_file])
                    sound_effect_inputs.append(effect_file)
                    status_callback(f"[{label}] 🔊 Aggiunto effetto: {os.path.basename(effect_file)}", False)
            elif len(existing_general_effects) == 1:
                # If only one effect available, use it for both
                effect_file = existing_general_effects[0]
                ffmpeg_cmd.extend(["-i", effect_file, "-i", effect_file])  # Add twice
                sound_effect_inputs = [effect_file, effect_file]
                status_callback(f"[{label}] 🔊 Aggiunto stesso effetto x2: {os.path.basename(effect_file)}", False)
            else:
                status_callback(f"[{label}] ⚠️ Effetti sonori insufficienti", True)
        except Exception as e:
            status_callback(f"[{label}] ⚠️ Errore selezione effetti sonori: {e}", True)
            sound_effect_inputs = []

        # Video filter: resize + fade
        video_filter = f"scale={base_w}:{base_h}:force_original_aspect_ratio=decrease,pad={base_w}:{base_h}:(ow-iw)/2:(oh-ih)/2,fade=t=in:st=0:d=0.2"

        # Audio filter: mix with TWO sound effects if present
        if len(sound_effect_inputs) == 2:
            # Mix original audio with two sound effects
            # First effect at start (0 seconds), second effect at end (duration-3 seconds)
            # Use adelay to position the second effect near the end
            audio_filter = "[0:a][1:a]amix=inputs=2:duration=first:weights=1 0.6[delayed_start];[2:a]adelay=3000:all=1,atrim=0:3[delayed_end];[delayed_start][delayed_end]amix=inputs=2:duration=first:weights=1 0.8[aout]"
            ffmpeg_cmd.extend([
                "-filter_complex", f"[0:v]{video_filter}[vout];{audio_filter}",
                "-map", "[vout]", "-map", "[aout]"
            ])
            status_callback(f"[{label}] 🎵 Mixing 3 audio streams: original + start effect + end effect", False)
        elif len(sound_effect_inputs) == 1:
            # Fallback: mix with one sound effect
            audio_filter = "[0:a][1:a]amix=inputs=2:duration=first:weights=1 0.6[aout]"
            ffmpeg_cmd.extend([
                "-filter_complex", f"[0:v]{video_filter}[vout];{audio_filter}",
                "-map", "[vout]", "-map", "[aout]"
            ])
            status_callback(f"[{label}] 🎵 Mixing 2 audio streams: original + single effect", False)
        else:
            ffmpeg_cmd.extend([
                "-vf", video_filter,
                "-map", "0:v", "-map", "0:a"
            ])
            status_callback(f"[{label}] 🎵 No sound effects to mix", False)

        # Encoding settings
        ffmpeg_cmd.extend([
            "-c:v", "libx264", "-preset", "fast", "-crf", "23",
            "-c:a", "aac", "-b:a", "128k",
            "-r", str(base_fps), "-pix_fmt", "yuv420p",
            sfx_path
        ])

        # Execute FFmpeg command with timeout
        try:
            result = subprocess.run(
                ffmpeg_cmd,
                check=True,
                capture_output=True,
                text=True,
                encoding='utf-8',
                errors='replace',
                timeout=timeout_ffmpeg
            )
            status_callback(f"[{label}] ✅ FFmpeg resize+effects with dual sound effects completato", False)
        except subprocess.TimeoutExpired:
            status_callback(f"[{label}] ❌ FFmpeg timeout dopo {timeout_ffmpeg}s", True)
            return None
        except subprocess.CalledProcessError as e:
            status_callback(f"[{label}] ❌ FFmpeg errore: {e.stderr}", True)
            return None

        # Verify output
        if not os.path.exists(sfx_path) or os.path.getsize(sfx_path) == 0:
            status_callback(f"[{label}] ❌ File SFX non creato correttamente", True)
            return None

        # Phase 2: Trascrizione (unchanged)
        status_callback(f"[{label}] Phase 2: Trascrizione e SRT", False)
        segments = []
        try:
            # Debug: check which function is being used
            logger.info(f"[{label}] 🔍 transcribe_clip_audio function: {transcribe_clip_audio.__module__}.{transcribe_clip_audio.__name__}")
            logger.info(f"[{label}] 🔍 Audio path exists: {os.path.exists(sfx_path)}, path: {sfx_path}")
            
            segments = transcribe_clip_audio(
                clip_path=sfx_path,
                status_callback=status_callback,
                label=label,
                processing_params=processing_params
            )
            logger.info(f"[{label}] 🔍 Segments returned: {len(segments) if segments else 0}")
            if not segments:
                logger.warning("La trascrizione non ha prodotto segmenti - genero SRT di fallback")
                try:
                    clip_duration = get_clip_duration(sfx_path)
                except Exception:
                    try:
                        with VideoFileClip(sfx_path) as _tmp_clip:
                            clip_duration = _tmp_clip.duration
                    except Exception:
                        clip_duration = 0.0
                # Crea un segmento placeholder che copre tutta la durata
                segments = [type('Segment', (), {'start': 0.0, 'end': clip_duration, 'text': '[Audio]'})()]
                status_callback(f"[{label}] ⚠️ Nessuna trascrizione, uso segmento placeholder per sottotitoli", False)

            # Write SRT
            with open(orig_srt, 'w', encoding='utf-8') as f:
                for i, seg in enumerate(segments):
                    start_time = format_timestamp(seg.start)
                    end_time = format_timestamp(seg.end)
                    text = getattr(seg, 'text', '[Audio]').strip() or '[Audio]'
                    f.write(f"{i+1}\n{start_time} --> {end_time}\n{text}\n\n")
            status_callback(f"[{label}] ✅ SRT scritto", False)
            
            # Phase 3: Traduzione (se necessaria)
            status_callback(f"[{label}] Phase 3: Gestione sottotitoli", False)
            if target_language and os.path.exists(orig_srt):
                try:
                    blocks = open(orig_srt, 'r', encoding='utf-8').read().strip().split("\n\n")
                    texts = []
                    lines_by_block = []
                    for block in blocks:
                        lines = block.splitlines()
                        text_in = ' '.join(lines[2:]) if len(lines) >= 3 else ''
                        texts.append(text_in)
                        lines_by_block.append(lines)

                    translated_texts = translate_texts_google_with_fallback(
                        texts=texts,
                        source_language='auto',
                        target_language=target_language,
                        status_callback=lambda m: status_callback(f"[{label}] {m}", False),
                    )

                    out_blocks = []
                    for idx, lines in enumerate(lines_by_block, 1):
                        text_out = translated_texts[idx - 1] if idx - 1 < len(translated_texts) else ''
                        if len(lines) >= 2:
                            if text_out.strip():
                                out_blocks.append(f"{lines[0]}\n{lines[1]}\n{text_out}")
                            else:
                                out_blocks.append("\n".join(lines))
                        else:
                            out_blocks.append("\n".join(lines))
                    
                    with open(trans_srt, 'w', encoding='utf-8') as f:
                        f.write("\n\n".join(out_blocks))
                    status_callback(f"[{label}] ✅ SRT tradotto: {os.path.basename(trans_srt)}", False)
                except Exception as e_trans_global:
                    status_callback(f"[{label}] ❌ Errore traduzione SRT: {e_trans_global}", True)
                    if raise_translation_errors:
                        raise
                    # Se la traduzione fallisce, usa l'SRT originale
                    shutil.copy(orig_srt, trans_srt)
                    status_callback(f"[{label}] ⚠️ Usando SRT originale senza traduzione", True)
            else:
                # Se non è richiesta traduzione, usa l'SRT originale come SRT finale
                shutil.copy(orig_srt, trans_srt)
                status_callback(f"[{label}] ℹ️ Usando SRT originale (nessuna traduzione richiesta)", False)
            
            # Phase 4: burn-in
            burn_input = sfx_path
            if os.path.exists(trans_srt):
                status_callback(f"[{label}] Phase 4: Burn-in sottotitoli", False)
                try:
                    # Memory cleanup before intensive operation
                    gc.collect()
                    
                    base = VideoFileClip(sfx_path)
                    subs = pysrt.open(trans_srt)
                    txt_clips = []
                    for sub in subs:
                        img = make_subtitle_image(
                            text=sub.text, 
                            font_path=FONT_PATH_DEFAULT, 
                            size=(base_w, base_h), 
                            font_size=42,
                            color="white",
                            shadow_color=(0, 0, 0, 200),
                            shadow_offset=(2, 2),
                            bg_box_color=(0, 0, 0, 180),
                            padding_x=20,
                            padding_y=10,
                            bottom_margin=50,
                            box_shadow_color=(0, 0, 0, 100),
                            box_shadow_offset=(3, 3),
                            box_shadow_blur_radius=5,
                            text_shadow_blur_radius=2
                        )
                        txt_clips.append(
                            CompositeVideoClip([ImageClip(img)], size=(base_w, base_h))
                                .with_start(sub.start.ordinal / 1000)
                                .with_duration(sub.duration.ordinal / 1000)
                                .with_fps(base_fps)
                                .with_position(('center', 'bottom'))
                        )
                    comp = CompositeVideoClip([base] + txt_clips, size=(base_w, base_h)).with_duration(base.duration)
                    
                    # Threading-based timeout for burn-in rendering (Windows compatible)
                    import threading
                    timeout_occurred = threading.Event()
                    render_exception = [None]
                    
                    def render_with_timeout():
                        try:
                            # Cambia directory a temp_dir per assicurarsi che i file temporanei siano creati lì
                            original_cwd = os.getcwd()
                            try:
                                os.chdir(temp_dir)
                                comp.write_videofile(
                                    subbed_path,  
                                    codec="libx264",  
                                    audio_codec="aac",  
                                    fps=base_fps,  
                                    preset=ffmpeg_preset_moviepy,  
                                    threads=max(1, os.cpu_count()),  
                                    logger=None  
                                )
                            finally:
                                os.chdir(original_cwd)
                        except Exception as e:
                            render_exception[0] = e
                    
                    render_thread = threading.Thread(target=render_with_timeout)
                    render_thread.daemon = True
                    render_thread.start()
                    render_thread.join(timeout=timeout_burnin)  # Use configurable timeout
                    
                    if render_thread.is_alive():
                        timeout_occurred.set()
                        status_callback(f"[{label}] ❌ Timeout durante burn-in sottotitoli ({timeout_burnin:.0f}s). Continuo senza sottotitoli.", True)
                        # Force cleanup
                        try:
                            base.close()
                            comp.close()
                            for clip in txt_clips:
                                if hasattr(clip, 'close'):
                                    clip.close()
                        except Exception:
                            pass
                        gc.collect()
                        # Continue with original video without subtitles instead of stopping
                        burn_input = sfx_path
                        status_callback(f"[{label}] ⚠️ Procedo con video senza sottotitoli", False)
                    else:
                        if render_exception[0]:
                            status_callback(f"[{label}] ❌ Errore durante burn-in: {render_exception[0]}. Continuo senza sottotitoli.", True)
                            burn_input = sfx_path
                        else:
                            base.close(); comp.close()
                            # Cleanup text clips
                            for clip in txt_clips:
                                if hasattr(clip, 'close'):
                                    try:
                                        clip.close()
                                    except Exception:
                                        pass
                            # Hard cleanup after burn-in processing
                            hard_cleanup(base, comp)
                            gc.collect()
                            # Garbage collection after burn-in completion
                            gc.collect()
                            burn_input = subbed_path
                            status_callback(f"[{label}] ✅ Burn-in completato", False)
                except Exception as e:
                    status_callback(f"[{label}] ⚠️ Burn-in fallito: {e}. Continuo senza sottotitoli.", True)
                    # Continue with original video without subtitles
                    burn_input = sfx_path
            else:
                # No translated SRT file, continue with original video
                burn_input = sfx_path

            # Phase 5: final FFmpeg
            status_callback(f"[{label}] Phase 5: FFmpeg final pass", False)
            cmd = [
                "ffmpeg", "-y", "-i", burn_input,
                "-vf", f"scale={base_w}:{base_h}:force_original_aspect_ratio=decrease,pad={base_w}:{base_h}:(ow-iw)/2:(oh-ih)/2,fps={base_fps}",
                "-c:v", "libx264", "-preset", ffmpeg_preset_final, "-crf", str(crf_final),
                "-c:a", "aac", "-b:a", "192k", "-pix_fmt", "yuv420p", final_path
            ]
            try:
                run_ffmpeg_command(cmd, f"[{label}] Final Pass", status_callback, timeout=timeout_ffmpeg)
                status_callback(f"[{label}] ✅ Final clip creato: {os.path.basename(final_path)}", False)
            except (subprocess.TimeoutExpired, Exception) as e:
                status_callback(f"[{label}] ❌ FFmpeg error/timeout in final pass: {e}", True)
                return None

            # Cleanup
            for f in temp_files:
                if f and os.path.exists(f):
                    try:
                        os.remove(f)
                    except:
                        pass
            # Final hard cleanup and garbage collection after middle clip processing
            hard_cleanup()
            gc.collect()
            status_callback(f"[{label}] ✅ Process middle clip completato", False)
            return final_path
        except Exception as e:
            status_callback(f"[{label}] ❌ Trascrizione fallita: {e}", True)
            # Continue without subtitles
            segments = []

        # Return the SFX file as final result if everything else fails
        return sfx_path
        
    except Exception as e:
        status_callback(f"[{label}] ❌ Errore generale: {e}", True)
        return None
    finally:
        # Aggressive cleanup
        gc.collect()
        status_callback(f"[{label}] 🧹 Cleanup memoria completato", False)


# Alias function to maintain compatibility
def process_single_middle_clip(
    clip_path: str,
    sound_effects_list: List[str],
    temp_dir: str,
    clip_index: int,
    target_language: Optional[str] = None,
    base_w: int = 1920,
    base_h: int = 1080,
    base_fps: float = 30.0,
    status_callback: Callable[[str, bool], None] = lambda m, e=False: None,
    raise_translation_errors: bool = False,
    ffmpeg_preset_moviepy: str = "medium",
    ffmpeg_preset_final: str = "medium",
    crf_final: int = 22,
    timeout_translation: float = 5.0,
    timeout_ffmpeg: float = 60.0,
    timeout_burnin: float = SUBTITLE_BURNIN_TIMEOUT_DEFAULT,
    processing_params: Optional[Dict[str, Any]] = None
) -> Optional[str]:
    """
    Alias for process_single_middle_clip_ffmpeg_first to maintain compatibility.
    """
    return process_single_middle_clip_ffmpeg_first(
        clip_path=clip_path,
        sound_effects_list=sound_effects_list,
        temp_dir=temp_dir,
        clip_index=clip_index,
        target_language=target_language,
        base_w=base_w,
        base_h=base_h,
        base_fps=base_fps,
        status_callback=status_callback,
        raise_translation_errors=raise_translation_errors,
        ffmpeg_preset_moviepy=ffmpeg_preset_moviepy,
        ffmpeg_preset_final=ffmpeg_preset_final,
        crf_final=crf_final,
        timeout_translation=timeout_translation,
        timeout_ffmpeg=timeout_ffmpeg,
        timeout_burnin=timeout_burnin,
        processing_params=processing_params
    )



def _process_intro_clip_task(
    original_clip_path_from_list: str,
    temp_dir: str,
    clip_index: int,
    audio_language_for_srt_target: Optional[str],
    base_w: int,
    base_h: int,
    base_fps: float,
    status_callback: Callable[[str, bool], None],
    main_label: str,
    sound_effects_list: List[str],
    processing_params: Optional[Dict[str, Any]] = None
) -> Optional[Tuple[int, str, float]]:
    """
    Elaborazione parallela di un clip middle:
    1) Resize + re-encode mantenendo audio
    2) Rilevamento lingua
    3) process_single_middle_clip per SFX e sottotitoli

    Ritorna (indice, path_output, durata) o None se fallisce.
    """
    task_label = f"{main_label} [MidClipTask#{clip_index+1}:{os.path.basename(original_clip_path_from_list)}]"
    status_callback(f"{task_label} ▶️ Inizio elaborazione", False)

    # Verifica esistenza
    if not os.path.exists(original_clip_path_from_list):
        status_callback(f"{task_label} ❌ Clip non trovata: {original_clip_path_from_list}", True)
        return None

    # 1) Creazione raw_mid
    raw_filename = f"middle_raw_{clip_index}_{uuid.uuid4().hex}.mp4"
    # Assicurati che temp_dir sia assoluto e che esista
    temp_dir = os.path.abspath(temp_dir)
    os.makedirs(temp_dir, exist_ok=True)
    raw_mid_path = os.path.abspath(os.path.join(temp_dir, raw_filename))
    status_callback(f"{task_label} ▶️ Creazione raw_mid: {raw_mid_path}", False)

    try:
        with VideoFileClip(original_clip_path_from_list) as clip_in:
            clip_resized = clip_in.resized((base_w, base_h))
            status_callback(f"{task_label} ▶️ Resize completato, inizio encoding", False)
            # Cambia directory di lavoro a temp_dir per assicurarsi che i file temporanei siano creati lì
            original_cwd = os.getcwd()
            try:
                os.chdir(temp_dir)
                clip_resized.write_videofile(
                    raw_mid_path,
                    codec="libx264",
                    audio_codec="aac",
                    fps=base_fps,
                    threads=min(2, max(1, os.cpu_count() // 4))
                )
            finally:
                os.chdir(original_cwd)
            clip_resized.close()
        status_callback(f"{task_label} ✅ raw_mid creato: {raw_mid_path}", False)
        # Hard cleanup after intro clip processing
        hard_cleanup()
        # Garbage collection after intro clip processing
        gc.collect()
    except Exception as e:
        err = traceback.format_exc()
        status_callback(f"{task_label} ❌ Errore raw_mid: {e}\n{err}", True)
        return None

    # 2) Rilevamento lingua
    status_callback(f"{task_label} ▶️ Rilevamento lingua...", False)
    try:
        clip_native_lang = detect_clip_language_safe(
            original_clip_path_from_list,
            status_callback,
            task_label
        )
        status_callback(f"{task_label} ✅ Lingua rilevata: {clip_native_lang}", False)
    except Exception:
        status_callback(f"{task_label} ⚠️ Lingua non rilevata, proseguo senza traduzioni", False)
        clip_native_lang = None

    # Determina lingua per sottotitoli
    if clip_native_lang and audio_language_for_srt_target and clip_native_lang != audio_language_for_srt_target:
        target_lang = audio_language_for_srt_target
        status_callback(f"{task_label} ▶️ Traduzione SRT: {clip_native_lang} → {target_lang}", False)
    else:
        # Anche se non è necessaria una traduzione, impostiamo comunque la lingua target per generare i sottotitoli
        target_lang = audio_language_for_srt_target
        status_callback(f"{task_label} ▶️ Generazione SRT nella stessa lingua: {audio_language_for_srt_target}", False)

    # 3) process_single_middle_clip
    status_callback(f"{task_label} ▶️ Avvio process_single_middle_clip", False)
    try:
        # Estrai timeout dai processing_params
        timeout_burnin = float(SUBTITLE_BURNIN_TIMEOUT_DEFAULT)  # Default from config
        if processing_params and "burnin_timeout_seconds" in processing_params:
            timeout_burnin = float(processing_params["burnin_timeout_seconds"])
            
        output_path = process_single_middle_clip(
            clip_path=raw_mid_path,
            sound_effects_list=sound_effects_list,
            temp_dir=temp_dir,
            clip_index=clip_index,
            target_language=target_lang,
            base_w=base_w,
            base_h=base_h,
            base_fps=base_fps,
            status_callback=status_callback,
            raise_translation_errors=False,
            timeout_burnin=timeout_burnin,
            processing_params=processing_params
        )
    except Exception as e:
        err = traceback.format_exc()
        status_callback(f"{task_label} ❌ Errore process_single_middle_clip: {e}\n{err}", True)
        return None

    if not output_path or not os.path.exists(output_path):
        status_callback(f"{task_label} ❌ Nessun output valido da process_single_middle_clip", True)
        return None

    # 4) Aggiunta transizione casuale con effetto sonoro all'ultimo secondo
    try:
        with VideoFileClip(output_path) as clip_out:
            dur = clip_out.duration
        if dur <= 0:
            status_callback(f"{task_label} ⚠️ Durata 0s per {output_path}", True)
            return None
            
        # Aggiungi transizione all'ultimo secondo se la clip è abbastanza lunga
        if dur > 1.5:  # Solo se la clip è più lunga di 1.5 secondi
            status_callback(f"{task_label} ▶️ Aggiunta transizione all'ultimo secondo", False)
            try:
                # Crea transizione casuale CON effetto sonoro
                transition_file_path = create_random_transition_with_sound(
                    duration=0.2,  # Transizione di 0.2 secondi (massimo richiesto)
                    status_callback=status_callback,
                    add_sound_effect=True  # Abilita effetto sonoro per la transizione
                )
                
                if transition_file_path and os.path.exists(transition_file_path):
                    # Carica la clip originale e la transizione
                    with VideoFileClip(output_path) as original_clip:
                        with VideoFileClip(transition_file_path) as transition_clip:
                            # Concatena la clip originale con la transizione
                            final_clip = concatenate_videoclips([original_clip, transition_clip], method="compose")
                            
                            # Salva il risultato finale
                            final_output_path = os.path.abspath(output_path.replace('.mp4', '_with_transition.mp4'))
                            # Assicura che la directory esista
                            final_output_dir = os.path.dirname(final_output_path) or os.getcwd()
                            os.makedirs(final_output_dir, exist_ok=True)
                            # Cambia directory per i file temporanei
                            original_cwd = os.getcwd()
                            try:
                                os.chdir(final_output_dir)
                                final_clip.write_videofile(
                                    final_output_path,
                                    codec="libx264",
                                    audio_codec="aac",
                                    fps=base_fps,
                                    threads=min(2, max(1, os.cpu_count() // 4))
                                )
                            finally:
                                os.chdir(original_cwd)
                            final_clip.close()
                            
                            # Aggiorna durata e path
                            dur = dur + 0.2  # Aggiungi 0.2 secondi per la transizione
                            
                            # Rimuovi il file originale e rinomina quello finale
                            safe_remove(output_path)
                            os.rename(final_output_path, output_path)
                            
                    # Cleanup del file di transizione temporaneo
                    safe_remove(transition_file_path)
                    status_callback(f"{task_label} ✅ Transizione aggiunta con successo", False)
                else:
                    status_callback(f"{task_label} ⚠️ Impossibile creare transizione, proseguo senza", False)
                    
            except Exception as e:
                status_callback(f"{task_label} ⚠️ Errore nell'aggiunta transizione: {e}, proseguo senza", False)
                logger.warning(f"Errore nell'aggiunta transizione per {task_label}: {e}")
        else:
            status_callback(f"{task_label} ⚠️ Clip troppo corta ({dur:.2f}s) per aggiungere transizione", False)
            
        status_callback(f"{task_label} ✅ Clip processata: {output_path} (durata {dur:.2f}s)", False)
        # Hard cleanup and garbage collection after intro clip completion
        hard_cleanup()
        gc.collect()
        return clip_index, output_path, dur
    except Exception as e:
        err = traceback.format_exc()
        status_callback(f"{task_label} ❌ Errore lettura durata: {e}\n{err}", True)
        return None




def _process_middle_clip_task(
    original_clip_path_from_list: str, # Questo è il path fornito nella lista middle_clips_paths
    temp_dir: str,
    clip_index: int, # Indice originale dalla lista
    audio_language_for_srt_target: Optional[str], # Lingua target per i sottotitoli (es. 'es')
    base_w: int,
    base_h: int,
    base_fps: float,
    status_callback: Callable[[str], None],
    main_label: str, # Etichetta principale del task genitore (es. [GenVidPar:...])
    sound_effects_list: List[str],
    processing_params: Optional[Dict[str, Any]] = None  # Parametri di processing inclusi timeout
) -> Optional[Tuple[int, str, float]]: # Ritorna (indice originale, path processato, durata)
    
    task_label = f"{main_label} [MidClipTask#{clip_index+1}:{os.path.basename(original_clip_path_from_list)}]"
    status_callback(f"{task_label} --- Inizio task elaborazione middle clip ---")

    if not os.path.exists(original_clip_path_from_list):
        status_callback(f"{task_label} ❌ File clip originale non trovato: {original_clip_path_from_list}")
        return None

    # Step 1: Creazione del "raw_mid" (resize e re-encode iniziale)
    # Questo file sarà l'input per process_single_middle_clip
    raw_mid_filename = f"middle_raw_{clip_index}_{uuid.uuid4().hex}.mp4"
    # Assicurati che temp_dir sia assoluto e che esista
    temp_dir = os.path.abspath(temp_dir)
    os.makedirs(temp_dir, exist_ok=True)
    raw_mid_path = os.path.abspath(os.path.join(temp_dir, raw_mid_filename))
    status_callback(f"{task_label} Creazione raw_mid: {raw_mid_path} da {original_clip_path_from_list}")

    try:
        # Cambia directory di lavoro a temp_dir per assicurarsi che i file temporanei siano creati lì
        original_cwd = os.getcwd()
        try:
            os.chdir(temp_dir)
            with VideoFileClip(original_clip_path_from_list) as video_clip_in:
                video_clip_in.resized(height=base_h) \
                    .write_videofile(
                        raw_mid_path,
                        codec="libx264", # Codec video standard
                        audio_codec="aac", # Codec audio standard
                        fps=base_fps,
                        audio=True, # Mantieni l'audio
                        logger=None, # Cambia in 'bar' per debug FFmpeg
                        threads=max(1, os.cpu_count() // 4) # Esempio di gestione thread MoviePy
                    )
        finally:
            os.chdir(original_cwd)
        status_callback(f"{task_label} ✅ Raw_mid creato con successo: {raw_mid_path}")
        # Hard cleanup after video processing
        hard_cleanup()
        # Garbage collection after video clip processing
        gc.collect()
    except Exception as e:
        error_details = traceback.format_exc()
        status_callback(f"{task_label} ❌ Fallimento creazione raw_mid ({raw_mid_path}): {e}\n{error_details}")
        return None # Non possiamo procedere se raw_mid fallisce

    # Step 2: Rilevamento lingua del raw_mid (o dell'originale, a seconda della logica)
    # Assumiamo di rilevare la lingua dal raw_mid o dall'originale se l'audio non è alterato significativamente.
    # Per coerenza con la tua logica originale, sembra che rilevi la lingua dal path originale.
    status_callback(f"{task_label} Rilevamento lingua per '{original_clip_path_from_list}'...")
    try:
        clip_native_lang = detect_clip_language_safe(original_clip_path_from_list, status_callback, task_label)
        status_callback(f"{task_label} Lingua rilevata per clip originale: {clip_native_lang or 'Non rilevata/Errore'}")
    except Exception as e:
        error_details = traceback.format_exc()
        status_callback(f"{task_label} ⚠️ Errore durante rilevamento lingua: {e}\n{error_details}")
        clip_native_lang = None # Procedi senza lingua rilevata o con default

    # Determina la lingua target per la traduzione dei sottotitoli, se necessaria
    target_lang_for_sub_translation = None
    if clip_native_lang and audio_language_for_srt_target and clip_native_lang != audio_language_for_srt_target:
        target_lang_for_sub_translation = audio_language_for_srt_target
        status_callback(f"{task_label} Sottotitoli saranno tradotti da '{clip_native_lang}' a '{target_lang_for_sub_translation}'")
    else:
        # Anche se non è necessaria una traduzione, impostiamo comunque la lingua target per generare i sottotitoli
        target_lang_for_sub_translation = audio_language_for_srt_target
        status_callback(f"{task_label} Sottotitoli saranno generati nella stessa lingua (lingua clip: {clip_native_lang}, lingua audio principale: {audio_language_for_srt_target})")

    # Step 3: Chiamata a process_single_middle_clip per ulteriori elaborazioni (SFX, sottotitoli, ecc.)
    # L'input a questa funzione è raw_mid_path
    status_callback(f"{task_label} Avvio process_single_middle_clip per: {raw_mid_path}")
    processed_subtitled_path: Optional[str] = None
    try:
        # Estrai timeout_ffmpeg e timeout_burnin dai processing_params
        timeout_ffmpeg = 60.0  # Default
        timeout_burnin = float(SUBTITLE_BURNIN_TIMEOUT_DEFAULT)  # Default from config
        if processing_params:
            if "ffmpeg_timeout_seconds" in processing_params:
                timeout_ffmpeg = float(processing_params["ffmpeg_timeout_seconds"])
            if "burnin_timeout_seconds" in processing_params:
                timeout_burnin = float(processing_params["burnin_timeout_seconds"])
        
        processed_subtitled_path = process_single_middle_clip(
            clip_path=raw_mid_path, # Input è il raw_mid appena creato
            sound_effects_list=sound_effects_list, # Usa il parametro passato
            temp_dir=temp_dir,
            clip_index=clip_index, # Per etichette e nomi file interni a process_single_middle_clip
            target_language=target_lang_for_sub_translation, # Lingua per traduzione, o None
            base_w=base_w,
            base_h=base_h,
            base_fps=base_fps,
            status_callback=status_callback, # Passa la callback per logging interno
            raise_translation_errors=False, # Come da tua chiamata originale
            timeout_ffmpeg=timeout_ffmpeg,  # Passa il timeout configurabile
            timeout_burnin=timeout_burnin,  # Passa il timeout burn-in configurabile
            processing_params=processing_params  # Passa i parametri di processing
        )
    except Exception as e: # Cattura esplicita anche qui, anche se process_single_middle_clip dovrebbe farlo
        error_details = traceback.format_exc()
        status_callback(f"{task_label} ❌ ECCEZIONE durante la chiamata a process_single_middle_clip: {e}\n{error_details}")
        # `processed_subtitled_path` rimarrà None

    if processed_subtitled_path and os.path.exists(processed_subtitled_path):
        status_callback(f"{task_label} ✅ process_single_middle_clip ha prodotto: {processed_subtitled_path}")
        try:
            duration = get_clip_duration(processed_subtitled_path)
            if duration == 0.0: # Potrebbe indicare un file corrotto o vuoto
                 status_callback(f"{task_label} ⚠️ Durata clip processata è 0.0s per {processed_subtitled_path}. Considero fallito.")
                 return None

            # Skip adding end sound effect since it's already added in process_single_middle_clip_ffmpeg_first
            # This prevents the sound effect from being repeated multiple times
            status_callback(f"{task_label} 🎵 Skip aggiunta effetto sonoro finale (già aggiunto in process_single_middle_clip_ffmpeg_first)", False)

            # Aggiungi transizione casuale con suono all'ultimo secondo (come per intro clips)
            if duration > 1.5:  # Solo se la clip è più lunga di 1.5 secondi
                status_callback(f"{task_label} ▶️ Aggiunta transizione all'ultimo secondo", False)
                try:
                    # Crea transizione casuale con effetto sonoro
                    transition_file_path = create_random_transition_with_sound(
                        duration=0.2,  # Transizione di 0.2 secondi (massimo richiesto)
                        status_callback=status_callback,
                        add_sound_effect=False  # Non aggiungere effetti sonori alla transizione per evitare duplicazione
                    )
                    
                    if transition_file_path and os.path.exists(transition_file_path):
                        # Carica la clip originale e la transizione
                        with VideoFileClip(processed_subtitled_path) as original_clip:
                            with VideoFileClip(transition_file_path) as transition_clip:
                                # Concatena la clip originale con la transizione
                                final_clip = concatenate_videoclips([original_clip, transition_clip], method="compose")
                                
                                # Salva il risultato finale
                                final_output_path = os.path.abspath(processed_subtitled_path.replace('.mp4', '_with_transition.mp4'))
                                # Assicura che la directory esista
                                final_output_dir = os.path.dirname(final_output_path) or os.getcwd()
                                os.makedirs(final_output_dir, exist_ok=True)
                                # Cambia directory per i file temporanei
                                original_cwd = os.getcwd()
                                try:
                                    os.chdir(final_output_dir)
                                    final_clip.write_videofile(
                                        final_output_path,
                                        codec="libx264",
                                        audio_codec="aac",
                                        fps=base_fps,
                                        threads=min(2, max(1, os.cpu_count() // 4))
                                    )
                                finally:
                                    os.chdir(original_cwd)
                                final_clip.close()
                                
                                # Aggiorna durata e path
                                duration = duration + 0.2  # Aggiungi 0.2 secondi per la transizione
                                
                                # Rimuovi il file originale e rinomina quello finale
                                safe_remove(processed_subtitled_path)
                                os.rename(final_output_path, processed_subtitled_path)
                                
                        # Cleanup del file di transizione temporaneo
                        safe_remove(transition_file_path)
                        status_callback(f"{task_label} ✅ Transizione aggiunta con successo", False)
                    else:
                        status_callback(f"{task_label} ⚠️ Impossibile creare transizione, proseguo senza", False)
                        
                except Exception as e:
                    error_details = traceback.format_exc()
                    status_callback(f"{task_label} ⚠️ Errore durante aggiunta transizione: {e}\n{error_details}", True)
                    # Continua senza transizione
            else:
                status_callback(f"{task_label} ⚠️ Clip troppo corta ({duration:.2f}s) per aggiungere transizione", False)

            status_callback(f"{task_label} ✅ Middle clip #{clip_index+1} ({os.path.basename(original_clip_path_from_list)}) processata con successo. Output: {processed_subtitled_path}, Durata: {duration:.2f}s")
            # Hard cleanup and garbage collection after middle clip completion
            hard_cleanup()
            gc.collect()
            return clip_index, processed_subtitled_path, duration
        except Exception as e:
            error_details = traceback.format_exc()
            status_callback(f"{task_label} ❌ Errore ottenimento durata da {processed_subtitled_path}: {e}\n{error_details}")
            return None # Considera fallito se non puoi ottenere la durata
    else:
        status_callback(f"{task_label} ❌ Fallita elaborazione da process_single_middle_clip per '{raw_mid_path}'. Nessun file di output valido.")
        return None


def ensure_clip_has_subtitles(
    input_clip_path: str,
    output_dir: str,
    target_language: str = "en",
    base_w: int = 1920,
    base_h: int = 1080,
    base_fps: float = 30.0,
    status_callback: Optional[Callable[[str, bool], None]] = None,
    timeout_burnin: float = 120.0
) -> Optional[str]:
    """
    Assicura che una clip abbia sottotitoli. Se non li ha, li genera e li applica.
    
    Args:
        input_clip_path: Percorso della clip di input
        output_dir: Directory per i file di output
        target_language: Lingua target per i sottotitoli (default: "en")
        base_w, base_h, base_fps: Parametri video standard
        status_callback: Callback per aggiornamenti di stato
        timeout_burnin: Timeout per il burn-in dei sottotitoli
        
    Returns:
        Percorso della clip con sottotitoli, o None se fallisce
    """
    if not status_callback:
        status_callback = lambda msg, is_error: print(f"{'ERROR: ' if is_error else ''}{msg}")
    
    if not os.path.exists(input_clip_path):
        status_callback(f"❌ File clip non trovato: {input_clip_path}", True)
        return None
    
    try:
        # Genera un nome file unico per l'output
        clip_basename = os.path.splitext(os.path.basename(input_clip_path))[0]
        output_filename = f"{clip_basename}_with_subtitles_{int(time.time())}.mp4"
        output_path = os.path.join(output_dir, output_filename)
        
        status_callback(f"🎬 Aggiunta sottotitoli a: {os.path.basename(input_clip_path)}", False)
        
        # Usa process_single_middle_clip_ffmpeg_first per aggiungere sottotitoli
        result_path = process_single_middle_clip_ffmpeg_first(
            clip_path=input_clip_path,
            sound_effects_list=[],  # Nessun effetto sonoro aggiuntivo
            temp_dir=output_dir,
            clip_index=0,
            target_language=target_language,
            base_w=base_w,
            base_h=base_h,
            base_fps=base_fps,
            status_callback=status_callback,
            raise_translation_errors=False,
            timeout_burnin=timeout_burnin
        )
        
        if result_path and os.path.exists(result_path):
            # Rinomina il file risultante con il nome desiderato
            if result_path != output_path:
                try:
                    if os.path.exists(output_path):
                        safe_remove(output_path)
                    os.rename(result_path, output_path)
                    status_callback(f"✅ Sottotitoli aggiunti con successo: {os.path.basename(output_path)}", False)
                    return output_path
                except Exception as e:
                    status_callback(f"⚠️ Errore rinominando file: {e}. Uso file originale.", True)
                    return result_path
            else:
                status_callback(f"✅ Sottotitoli aggiunti con successo: {os.path.basename(result_path)}", False)
                return result_path
        else:
            status_callback(f"❌ Fallita generazione sottotitoli per: {os.path.basename(input_clip_path)}", True)
            return None
            
    except Exception as e:
        error_details = traceback.format_exc()
        status_callback(f"❌ Errore durante aggiunta sottotitoli: {e}\n{error_details}", True)
        return None


def process_clips_with_subtitles(
    input_clips: List[str],
    output_dir: str,
    target_language: str = "en",
    base_w: int = 1920,
    base_h: int = 1080,
    base_fps: float = 30.0,
    status_callback: Optional[Callable[[str, bool], None]] = None,
    max_workers: int = 2
) -> List[str]:
    """
    Elabora una lista di clip assicurandosi che tutte abbiano sottotitoli.
    
    Args:
        input_clips: Lista dei percorsi delle clip di input
        output_dir: Directory per i file di output
        target_language: Lingua target per i sottotitoli
        base_w, base_h, base_fps: Parametri video standard
        status_callback: Callback per aggiornamenti di stato
        max_workers: Numero massimo di worker per elaborazione parallela
        
    Returns:
        Lista dei percorsi delle clip elaborate con sottotitoli
    """
    if not status_callback:
        status_callback = lambda msg, is_error: print(f"{'ERROR: ' if is_error else ''}{msg}")
    
    if not os.path.exists(output_dir):
        os.makedirs(output_dir, exist_ok=True)
    
    processed_clips = []
    
    status_callback(f"🎬 Inizio elaborazione di {len(input_clips)} clip con sottotitoli", False)
    
    # Elaborazione sequenziale per evitare problemi di memoria
    for i, clip_path in enumerate(input_clips):
        status_callback(f"📋 Elaborazione clip {i+1}/{len(input_clips)}: {os.path.basename(clip_path)}", False)
        
        result_path = ensure_clip_has_subtitles(
            input_clip_path=clip_path,
            output_dir=output_dir,
            target_language=target_language,
            base_w=base_w,
            base_h=base_h,
            base_fps=base_fps,
            status_callback=status_callback
        )
        
        if result_path:
            processed_clips.append(result_path)
            status_callback(f"✅ Clip {i+1} completata: {os.path.basename(result_path)}", False)
        else:
            status_callback(f"❌ Fallita elaborazione clip {i+1}: {os.path.basename(clip_path)}", True)
    
    status_callback(f"🎉 Elaborazione completata: {len(processed_clips)}/{len(input_clips)} clip elaborate con successo", False)
    return processed_clips
