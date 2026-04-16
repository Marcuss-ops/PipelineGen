"""Main video generation function and high-level video processing."""

import logging
from math import e
import os
import random
import shutil
import subprocess
import tempfile
import time
import traceback
import uuid
import gc
import concurrent.futures
from concurrent.futures import as_completed
from typing import List, Optional, Dict, Any, Callable, Tuple
from pathlib import Path
import json

# Try relative imports first (when used as a package)
try:
    from modules.video.video_clips import (
        _process_middle_clip_task,
        _process_intro_clip_task
    )
    from modules.video.video_ffmpeg import (
        get_audio_duration_ffmpeg,
        get_audio_codec_ffmpeg,
        run_ffmpeg_command
    )
    from modules.video.video_audio import get_clip_duration , _generate_voiced_stock_segment_task
except ImportError:
    # Fallback to absolute imports
    from video_clips import (
        _process_middle_clip_task,
        _process_intro_clip_task
    )
    try:
        from video_ffmpeg import (
            get_audio_duration_ffmpeg,
            get_audio_codec_ffmpeg,
            run_ffmpeg_command
        )
        from video_audio import get_clip_duration , _generate_voiced_stock_segment_task
    except ImportError:
        # Define fallback functions if imports fail
        def get_audio_duration_ffmpeg(path):
            """Fallback audio duration function using ffprobe."""
            try:
                cmd = ['ffprobe', '-v', 'error', '-show_entries', 'format=duration',
                       '-of', 'default=noprint_wrappers=1:nokey=1', path]
                result = subprocess.run(cmd, capture_output=True, text=True, timeout=15)
                if result.returncode == 0:
                    return float(result.stdout.strip())
            except Exception:
                pass
            return 0.0
        
        def get_audio_codec_ffmpeg(path):
            """Fallback audio codec detection function."""
            try:
                cmd = ['ffprobe', '-v', 'error', '-select_streams', 'a:0',
                       '-show_entries', 'stream=codec_name', '-of', 'csv=p=0', path]
                result = subprocess.run(cmd, capture_output=True, text=True, timeout=10)
                if result.returncode == 0:
                    return result.stdout.strip()
            except Exception:
                pass
            return "unknown"
        
        def get_clip_duration(path):
            """Fallback clip duration function using ffprobe."""
            try:
                cmd = ['ffprobe', '-v', 'error', '-show_entries', 'format=duration',
                       '-of', 'default=noprint_wrappers=1:nokey=1', path]
                result = subprocess.run(cmd, capture_output=True, text=True, timeout=15)
                if result.returncode == 0:
                    return float(result.stdout.strip())
            except Exception:
                pass
            return 0.0
        
        def run_ffmpeg_command(cmd, description, status_callback, progress=False, timeout=120):
            """Fallback FFmpeg command runner."""
            try:
                status_callback(f"Eseguendo: {description}", False)
                result = subprocess.run(cmd, capture_output=True, text=True, timeout=timeout, check=True)
                status_callback(f"Completato: {description}", False)
                return result
            except subprocess.CalledProcessError as e:
                status_callback(f"Errore FFmpeg: {e}", True)
                raise RuntimeError(f"FFmpeg failed: {e}") from e
            except subprocess.TimeoutExpired as e:
                status_callback(f"Timeout FFmpeg: {e}", True)
                raise RuntimeError(f"FFmpeg timeout: {e}") from e

        def _generate_voiced_stock_segment_task(*args, **kwargs):
            """Fallback wrapper that imports the real implementation lazily to avoid circular imports."""
            from video_audio import _generate_voiced_stock_segment_task as _real_task
            return _real_task(*args, **kwargs)

# Import cache manager with fallback
try:
    from modules.utils.cache_manager import cache_manager
except ImportError:
    try:
        from modules.utils.cache_manager import cache_manager
    except ImportError:
        # If import fails, create a mock cache manager to prevent NoneType errors
        import logging
        logging.warning("Failed to import cache_manager, using mock implementation")
        
        class MockCacheManager:
            def get_cached_stock_segment(self, *args, **kwargs):
                return None
                
            def cache_stock_segment(self, *args, **kwargs):
                pass
                
            def get_clip_duration(self, *args, **kwargs):
                return None
        
        cache_manager = MockCacheManager()

from moviepy import (
    VideoFileClip,
    ImageClip,
    CompositeVideoClip,
    concatenate_videoclips,
    TextClip,
    AudioFileClip,
    ColorClip,
    vfx,
    concatenate_audioclips,
)
from moviepy.audio.AudioClip import CompositeAudioClip
import textwrap
import numpy as np

try:
    # Prefer absolute imports for better compatibility
    from config.config import (
        BASE_W_DEFAULT as VIDEO_WIDTH,
        BASE_H_DEFAULT as VIDEO_HEIGHT,
        BASE_FPS_DEFAULT as VIDEO_FPS,
        FONT_PATH_DEFAULT,
        BACKGROUND_MUSIC_VOLUME,
        BACKGROUND_MUSIC_PATHS,
        SUBTITLE_BURNIN_TIMEOUT_DEFAULT,
        DEFAULT_SOUND_EFFECTS,
        DATE_FONT_SIZE_DEFAULT,
        DATE_TYPEWRITER_RATIO_DEFAULT,
    )
    from modules.video.video_core import (
        safe_close_moviepy_object,
        create_safe_temp_filename,
        aggressive_memory_cleanup,
        log_mem,
        force_gc_and_cleanup,
        _validate_resources,
    )
    from modules.video.video_clips import (
        concatenate_video_clips,
        resize_video_to_standard,
        apply_gaussian_blur,
    )
    from modules.video.video_effects import add_background_music, create_random_transition_with_sound, hard_cleanup, comprehensive_moviepy_cleanup
    from modules.video.video_overlays import (
        create_text_overlay_clip,
        create_subtitle_overlay,
        make_word_by_word_overlay_positioned,
        make_important_words_image,
        generate_nomi_speciali_overlay,
        generate_date_overlay,
        _render_single_image_overlay_task_worker
        # Task functions removed - logic now defined locally in this module
    )
    from modules.video.video_processing import get_year_from_date_text
    from modules.video.video_ffmpeg import run_ffmpeg_command, merge_overlays_ffmpeg, _safe_float, get_audio_duration_ffmpeg, get_audio_codec_ffmpeg, concatenate_videos_ffmpeg
    from modules.utils.utils import download_image
    from modules.video.video_audio import (
        transcribe_audio_with_whisper,
        translate_text_segments,
        create_srt_file,
        mux_stock_with_voiceover
    )
    from modules.audio.audio_processing import trascrivi_audio_parallel, slide_in
except ImportError:
    # Fallback to absolute imports when run as a script
    try:
        from config.config import (
            BASE_W_DEFAULT as VIDEO_WIDTH,
            BASE_H_DEFAULT as VIDEO_HEIGHT,
            BASE_FPS_DEFAULT as VIDEO_FPS,
            FONT_PATH_DEFAULT,
            BACKGROUND_MUSIC_VOLUME,
            BACKGROUND_MUSIC_PATHS,
            SUBTITLE_BURNIN_TIMEOUT_DEFAULT,
            DEFAULT_SOUND_EFFECTS,
            DATE_FONT_SIZE_DEFAULT,
            DATE_TYPEWRITER_RATIO_DEFAULT,
        )
    except ImportError:
        try:
            from config.config import (
                BASE_W_DEFAULT as VIDEO_WIDTH,
                BASE_H_DEFAULT as VIDEO_HEIGHT,
                BASE_FPS_DEFAULT as VIDEO_FPS,
                FONT_PATH_DEFAULT,
                BACKGROUND_MUSIC_VOLUME,
                BACKGROUND_MUSIC_PATHS,
                SUBTITLE_BURNIN_TIMEOUT_DEFAULT,
                DEFAULT_SOUND_EFFECTS,
                DATE_FONT_SIZE_DEFAULT,
                DATE_TYPEWRITER_RATIO_DEFAULT,
            )
        except ImportError:
            from config import (
            BASE_W_DEFAULT as VIDEO_WIDTH,
            BASE_H_DEFAULT as VIDEO_HEIGHT,
            BASE_FPS_DEFAULT as VIDEO_FPS,
            FONT_PATH_DEFAULT,
            BACKGROUND_MUSIC_VOLUME,
            BACKGROUND_MUSIC_PATHS,
            SUBTITLE_BURNIN_TIMEOUT_DEFAULT,
            DEFAULT_SOUND_EFFECTS,
            DATE_FONT_SIZE_DEFAULT,
            DATE_TYPEWRITER_RATIO_DEFAULT,
        )
    try:
        from video_core import (
            safe_close_moviepy_object,
            create_safe_temp_filename,
            aggressive_memory_cleanup,
            log_mem,
            force_gc_and_cleanup,
            _validate_resources,
        )
        from video_clips import (
            concatenate_video_clips,
            resize_video_to_standard,
            apply_gaussian_blur,
        )
        from video_effects import add_background_music, create_random_transition_with_sound, hard_cleanup, comprehensive_moviepy_cleanup
        from video_overlays import (
            create_text_overlay_clip,
            create_subtitle_overlay,
            make_word_by_word_overlay_positioned,
            make_important_words_image,
            generate_nomi_speciali_overlay,
            generate_date_overlay,
            _render_single_image_overlay_task_worker
            # Task functions removed - using inline logic
        )
        from video_processing import get_year_from_date_text
        from video_audio import (
            transcribe_audio_with_whisper,
            translate_text_segments,
            create_srt_file,
            mux_stock_with_voiceover
        )
        from modules.audio.audio_processing import trascrivi_audio_parallel, slide_in
        from video_ffmpeg import run_ffmpeg_command, merge_overlays_ffmpeg, _safe_float, get_audio_duration_ffmpeg, get_audio_codec_ffmpeg, concatenate_videos_ffmpeg
        from utils import download_image
    except ImportError:
        from video_core import (
            safe_close_moviepy_object,
            create_safe_temp_filename,
            aggressive_memory_cleanup,
            log_mem,
            force_gc_and_cleanup,
            _validate_resources,
        )
        from video_clips import (
            concatenate_video_clips,
            resize_video_to_standard,
            apply_gaussian_blur,
        )
        from video_effects import add_background_music, create_random_transition_with_sound, hard_cleanup, comprehensive_moviepy_cleanup
        from video_overlays import (
            create_text_overlay_clip,
            create_subtitle_overlay,
            make_word_by_word_overlay_positioned,
            make_important_words_image,
            generate_nomi_speciali_overlay,
            generate_date_overlay,
            # _render_single_image_overlay_task_worker,  # Remove this import
            # Task functions removed - using inline logic
        )
        from video_processing import get_year_from_date_text
        # Import _render_single_image_overlay_task_worker from video_processing instead
        from video_audio import (
            transcribe_audio_with_whisper,
            translate_text_segments,
            create_srt_file,
            mux_stock_with_voiceover
        )
        from modules.audio.audio_processing import trascrivi_audio_parallel, slide_in
        from video_ffmpeg import run_ffmpeg_command, merge_overlays_ffmpeg, _safe_float, get_audio_duration_ffmpeg, get_audio_codec_ffmpeg, concatenate_videos_ffmpeg
        from utils import download_image

# Ensure mux_stock_with_voiceover points to the refactored implementation when available
try:
    from modules.video.video_audio import mux_stock_with_voiceover as _refactored_mux_stock_with_voiceover
except ImportError:
    try:
        from video_audio import mux_stock_with_voiceover as _refactored_mux_stock_with_voiceover
    except ImportError:
        _refactored_mux_stock_with_voiceover = None

if _refactored_mux_stock_with_voiceover is not None:
    mux_stock_with_voiceover = _refactored_mux_stock_with_voiceover

# Global imports - always available

logger = logging.getLogger(__name__)


# Strategy toggle: keep intermediates in PCM to avoid AAC priming gaps
# True = emit segment files with PCM audio (container MKV),
# concatenate to MKV with stream copy, and encode to AAC only at the final step.
USE_PCM_INTERMEDIATE = False




def generate_video_from_segments(
    segments: List[Dict[str, Any]],
    output_path: str,
    config: Optional[Dict[str, Any]] = None,
    status_callback: Optional[Callable[[str], None]] = None,
    progress_callback: Optional[Callable[[float], None]] = None
) -> bool:
    """Generate video from a list of segments."""
    try:
        # Handle case where config is None
        if config is None:
            # Video settings
            video_width = VIDEO_WIDTH
            video_height = VIDEO_HEIGHT
            video_fps = VIDEO_FPS

            # Processing options
            add_transitions = True
            add_subtitles = False
            add_music = False
            music_volume = 0.4
            # Video settings
            video_width = config.get('video_width', VIDEO_WIDTH)
            video_height = config.get('video_height', VIDEO_HEIGHT)
            video_fps = config.get('video_fps', VIDEO_FPS)

            # Processing options
            add_transitions = config.get('add_transitions', True)
            add_subtitles = config.get('add_subtitles', False)
            add_music = config.get('add_background_music', False)
            music_volume = config.get('music_volume', 0.4)

        # Process segments into video clips
        video_clips = []
        total_segments = len(segments)

        for i, segment in enumerate(segments):
            try:
                if status_callback:
                    status_callback(f"Processing segment {i+1}/{total_segments}")

                if progress_callback:
                    progress_callback((i / total_segments) * 0.8)  # 80% for processing

                # Create video clip from segment
                clip = create_video_clip_from_segment(
                    segment,
                    config=config if config is not None else {},
                    status_callback=status_callback
                )

                if clip:
                    video_clips.append(clip)
                else:
                    logger.warning(f"Failed to create clip for segment {i}")

                # Memory cleanup every few segments
                if (i + 1) % 5 == 0:
                    force_gc_and_cleanup()
                    log_mem(f"After processing segment {i+1}")

            except Exception as e:
                logger.error(f"Error processing segment {i}: {e}")
                continue

        if not video_clips:
            logger.error("No video clips were created from segments")
            return False

        if status_callback:
            status_callback(f"Created {len(video_clips)} video clips, concatenating...")

        # Add transitions between clips if requested
        if add_transitions and len(video_clips) > 1:
            video_clips = add_transitions_between_clips(
                video_clips,
                config=config if config is not None else {},
                status_callback=status_callback
            )

        # Concatenate all clips
        if status_callback:
            status_callback("Concatenating video clips...")

        final_video = concatenate_videoclips(
            video_clips,
            method="compose"
        )

        if not final_video:
            logger.error("Failed to concatenate video clips")
            return False

        # Ensure standard resolution
        final_video = resize_video_to_standard(final_video)

        # Add background music if requested
        if add_music:
            if status_callback:
                status_callback("Adding background music...")

            final_video = add_background_music(
                final_video,
                music_volume=music_volume,
                status_callback=status_callback
            )

        # Add subtitles if requested
        if add_subtitles:
            if status_callback:
                status_callback("Adding subtitles...")

            final_video = add_subtitles_to_video(
                final_video,
                segments,
                config=config if config is not None else {},
                status_callback=status_callback
            )

        # Write final video
        if status_callback:
            status_callback("Writing final video file...")

        if progress_callback:
            progress_callback(0.9)  # 90% complete

        success = write_final_video(
            final_video,
            output_path,
            config=config if config is not None else {},
            status_callback=status_callback
        )

        # Cleanup
        for clip in video_clips:
            safe_close_moviepy_object(clip, "video_clip")

        safe_close_moviepy_object(final_video, "final_video")

        # Final cleanup
        aggressive_memory_cleanup()
        log_mem("Video generation completed")

        if progress_callback:
            progress_callback(1.0)  # 100% complete

        if success:
            if status_callback:
                status_callback(f"Video generation completed successfully: {output_path}")
            return True
            if status_callback:
                status_callback("Video generation failed")
            return False

    except Exception as e:
        logger.error(f"Error generating video from segments: {e}")
        if status_callback:
            status_callback(f"Video generation error: {str(e)}")
        return False


def create_video_clip_from_segment(
    segment: Dict[str, Any],
    config: Optional[Dict[str, Any]] = None,
    status_callback: Optional[Callable[[str], None]] = None
) -> Optional[VideoFileClip]:
    """Create a video clip from a segment definition."""
    try:
        if not config:
            config = {}
        
        segment_type = segment.get('type', 'unknown')
        
        if segment_type == 'video':
            return create_video_segment_clip(segment, config, status_callback)
        elif segment_type == 'image':
            return create_image_segment_clip(segment, config, status_callback)
        elif segment_type == 'text':
            return create_text_segment_clip(segment, config, status_callback)
        elif segment_type == 'stock':
            return create_stock_segment_clip(segment, config, status_callback)
            logger.warning(f"Unknown segment type: {segment_type}")
            return None
            
    except Exception as e:
        logger.error(f"Error creating video clip from segment: {e}")
        return None


def create_video_segment_clip(
    segment: Dict[str, Any],
    config: Optional[Dict[str, Any]],
    status_callback: Optional[Callable[[str], None]] = None
) -> Optional[VideoFileClip]:
    """Create video clip from video segment."""
    try:
        video_path = segment.get('path')
        if not video_path or not os.path.exists(video_path):
            logger.error(f"Video file not found: {video_path}")
            return None
        
        # Load video clip
        clip = VideoFileClip(video_path)
        
        # Apply segment timing
        start_time = segment.get('start_time', 0)
        end_time = segment.get('end_time', clip.duration)
        duration = segment.get('duration', end_time - start_time)
        
        if start_time > 0 or end_time < clip.duration:
            clip = clip.subclip(start_time, min(end_time, clip.duration))
        
        # Resize to standard resolution
        clip = resize_video_to_standard(clip)
        
        return clip
        
    except Exception as e:
        logger.error(f"Error creating video segment clip: {e}")
        return None


def create_image_segment_clip(
    segment: Dict[str, Any],
    config: Dict[str, Any],
    status_callback: Optional[Callable[[str], None]] = None
) -> Optional[VideoFileClip]:
    """Create image clip from image segment with optional custom background."""
    try:
        image_path = segment.get('path')
        image_url = segment.get('url')
        duration = segment.get('duration', 3.0)
        background_path = segment.get('background_path')  # Custom background path
        
        # Download image if URL provided
        if image_url and not image_path:
            if status_callback:
                status_callback(f"Downloading image: {image_url}")
            
            temp_dir = tempfile.gettempdir()
            image_path = download_image([image_url], temp_dir, status_callback)
        
        if not image_path or not os.path.exists(image_path):
            logger.error(f"Image file not found: {image_path}")
            return None
        
        # Create image clip
        image_clip = ImageClip(image_path).with_duration(duration)
        
        # Resize image to standard resolution
        image_clip = image_clip.resized((config.get('video_width', VIDEO_WIDTH), config.get('video_height', VIDEO_HEIGHT)))
        
        # Check if custom background is provided
        if background_path and os.path.exists(background_path):
            # Create background clip
            if background_path.lower().endswith(('.mp4', '.avi', '.mov', '.mkv')):
                # Video background
                from moviepy import VideoFileClip
                from modules.video.video_clips import loop_video_clip
                background_clip = VideoFileClip(background_path)
                background_clip = loop_video_clip(background_clip, duration)
                background_clip = background_clip.resized((config.get('video_width', VIDEO_WIDTH), config.get('video_height', VIDEO_HEIGHT)))
            else:
                # Image background
                background_clip = ImageClip(background_path).with_duration(duration)
                background_clip = background_clip.resized((config.get('video_width', VIDEO_WIDTH), config.get('video_height', VIDEO_HEIGHT)))
            
            # Composite image over background
            final_clip = CompositeVideoClip([background_clip, image_clip.with_position('center')], size=(config.get('video_width', VIDEO_WIDTH), config.get('video_height', VIDEO_HEIGHT)))
            
            # Convert CompositeVideoClip to VideoFileClip by writing to temp file and re-reading
            temp_path = create_safe_temp_filename("image_with_bg", ".mp4")
            final_clip.write_videofile(temp_path, codec="libx264", audio=False, logger=None)
            final_clip.close()
            background_clip.close()
            image_clip.close()
            
            # Return as VideoFileClip
            return VideoFileClip(temp_path)
            # No custom background, convert ImageClip to VideoFileClip
            temp_path = create_safe_temp_filename("image_clip", ".mp4")
            image_clip.write_videofile(temp_path, codec="libx264", audio=False, logger=None)
            image_clip.close()
            
            # Return as VideoFileClip
            return VideoFileClip(temp_path)
        
    except Exception as e:
        logger.error(f"Error creating image segment clip: {e}")
        return None


def create_text_segment_clip(
    segment: Dict[str, Any],
    config: Dict[str, Any],
    status_callback: Optional[Callable[[str], None]] = None
) -> Optional[VideoFileClip]:
    """Create text clip from text segment."""
    try:
        text = segment.get('text', '')
        duration = segment.get('duration', 3.0)
        
        if not text:
            logger.warning("No text provided for text segment")
            return None
        
        # Create text overlay
        text_clip = create_text_overlay_clip(
            text=text,
            duration=duration,
            font_size=segment.get('font_size', 60),
            font_color=segment.get('font_color', 'white'),
            position=segment.get('position', ('center', 'center'))
        )
        
        if not text_clip:
            return None
        
        # Create background
        bg_color = segment.get('bg_color', 'black')
        background = ColorClip(
            size=(config.get('video_width', VIDEO_WIDTH), config.get('video_height', VIDEO_HEIGHT)),
            color=bg_color,
            duration=duration
        )
        
        # Composite text over background
        final_clip = CompositeVideoClip([background, text_clip])
        
        # Convert CompositeVideoClip to VideoFileClip by writing to temp file and re-reading
        temp_path = create_safe_temp_filename("text_segment", ".mp4")
        final_clip.write_videofile(temp_path, codec="libx264", audio=False, logger=None)
        final_clip.close()
        background.close()
        text_clip.close()
        
        # Return as VideoFileClip
        return VideoFileClip(temp_path)
        
    except Exception as e:
        logger.error(f"Error creating text segment clip: {e}")
        return None


def create_stock_segment_clip(
    segment: Dict[str, Any],
    config: Dict[str, Any],
    status_callback: Optional[Callable[[str], None]] = None
) -> Optional[VideoFileClip]:
    """Create clip from stock video segment."""
    try:
        # Stock segments are similar to video segments but may have special handling
        return create_video_segment_clip(segment, config, status_callback)
        
    except Exception as e:
        logger.error(f"Error creating stock segment clip: {e}")
        return None


def add_transitions_between_clips(
    clips: List[VideoFileClip],
    config: Dict[str, Any] = None,
    status_callback: Optional[Callable[[str], None]] = None
) -> List[VideoFileClip]:
    """Add transition effects between video clips."""
    try:
        if len(clips) <= 1:
            return clips
        
        if not config:
            config = {}
        
        transition_duration = config.get('transition_duration', 1.0)
        
        clips_with_transitions = []
        
        for i in range(len(clips)):
            clips_with_transitions.append(clips[i])
            
            # Add transition between clips (except after the last clip)
            if i < len(clips) - 1:
                if status_callback:
                    status_callback(f"Creating transition {i+1}/{len(clips)-1}")
                
                transition_path = create_random_transition_with_sound(
                    duration=transition_duration,
                    status_callback=status_callback
                )
                
                if transition_path and os.path.exists(transition_path):
                    transition_clip = VideoFileClip(transition_path)
                    clips_with_transitions.append(transition_clip)
        
        return clips_with_transitions
        
    except Exception as e:
        logger.error(f"Error adding transitions between clips: {e}")
        return clips


def add_subtitles_to_video(
    video_clip: VideoFileClip,
    segments: List[Dict[str, Any]],
    config: Dict[str, Any] = None,
    status_callback: Optional[Callable[[str], None]] = None
) -> VideoFileClip:
    """Add subtitles to video based on segments."""
    try:
        if not config:
            config = {}
        
        subtitle_clips = []
        
        for segment in segments:
            text = segment.get('text', '')
            start_time = segment.get('start_time', 0)
            end_time = segment.get('end_time', start_time + 3)
            
            if not text:
                continue
            
            duration = end_time - start_time
            
            # Create subtitle clip
            subtitle_clip = create_subtitle_overlay(
                subtitle_text=text,
                duration=duration,
                font_size=config.get('subtitle_font_size', 48),
                font_color=config.get('subtitle_color', 'white'),
                position=config.get('subtitle_position', ('center', 'bottom'))
            )
            
            if subtitle_clip:
                subtitle_clip = subtitle_clip.with_start(start_time)
                subtitle_clips.append(subtitle_clip)
        
        if subtitle_clips:
            # Composite subtitles over video
            final_video = CompositeVideoClip([video_clip] + subtitle_clips)
            return final_video
            return video_clip
            
    except Exception as e:
        logger.error(f"Error adding subtitles to video: {e}")
        return video_clip


def write_final_video(
    video_clip: VideoFileClip,
    output_path: str,
    config: Dict[str, Any] = None,
    status_callback: Optional[Callable[[str], None]] = None
) -> bool:
    """Write final video to file."""
    try:
        if not config:
            config = {}
        
        # Video encoding settings
        fps = config.get('video_fps', VIDEO_FPS)
        codec = config.get('video_codec', 'libx264')
        audio_codec = config.get('audio_codec', 'aac')
        bitrate = config.get('video_bitrate', '5000k')
        
        # Write video file
        video_clip.write_videofile(
            output_path,
            fps=fps,
            codec=codec,
            audio_codec=audio_codec,
            bitrate=bitrate,
            verbose=False,
            logger=None,
            temp_audiofile=create_safe_temp_filename("temp_audio", ".m4a")
        )
        
        # Verify output file exists
        if os.path.exists(output_path):
            file_size = os.path.getsize(output_path)
            if status_callback:
                status_callback(f"Video written successfully: {output_path} ({file_size} bytes)")
            return True
            logger.error(f"Output file was not created: {output_path}")
            return False
            
    except Exception as e:
        logger.error(f"Error writing final video: {e}")
        if status_callback:
            status_callback(f"Video writing error: {str(e)}")
        return False


def create_video_preview(
    segments: List[Dict[str, Any]],
    output_path: str,
    max_duration: float = 30.0,
    config: Dict[str, Any] = None,
    status_callback: Optional[Callable[[str], None]] = None
) -> bool:
    """Create a short preview of the video."""
    try:
        if not segments:
            return False
        
        if status_callback:
            status_callback("Creating video preview...")
        
        # Select segments for preview (first few segments up to max_duration)
        preview_segments = []
        total_duration = 0
        
        for segment in segments:
            segment_duration = segment.get('duration', 3.0)
            if total_duration + segment_duration <= max_duration:
                preview_segments.append(segment)
                total_duration += segment_duration
            else:
                # Add partial segment if it fits
                remaining_duration = max_duration - total_duration
                if remaining_duration > 1.0:  # At least 1 second
                    preview_segment = segment.copy()
                    preview_segment['duration'] = remaining_duration
                    preview_segments.append(preview_segment)
                break
        
        if not preview_segments:
            return False
        
        # Generate preview video
        preview_config = config.copy() if config else {}
        preview_config['add_transitions'] = False  # Skip transitions for faster preview
        preview_config['add_background_music'] = False  # Skip music for faster preview
        
        success = generate_video_from_segments(
            preview_segments,
            output_path,
            config=preview_config,
            status_callback=status_callback
        )
        
        if success and status_callback:
            status_callback(f"Preview created: {output_path}")
        
        return success
        
    except Exception as e:
        logger.error(f"Error creating video preview: {e}")
        if status_callback:
            status_callback(f"Preview creation error: {str(e)}")
        return False
    
    
def generate_stock_segment(
    duration: float,
    stock_clips: List[str],
    cache_dir: Optional[str],
    temp_dir: str,
    status_callback: Callable[[str], None],
    base_w: int = 1920,
    base_h: int = 1080,
    base_fps: int = 30,
    ffmpeg_preset: str = "medium",
    random_generator: Optional[random.Random] = None,
    mute_audio: bool = True
):
    """
    Genera un segmento video della durata richiesta combinando
    i clip stock forniti in ordine casuale e tagliandoli per coprire la durata.
    Utilizza un sistema di cache per velocizzare la generazione di segmenti identici.

    Args:
        duration: Durata desiderata in secondi.
        stock_clips: Lista di percorsi ai file video stock.
        cache_dir: Directory cache (non usata, sostituita dal cache manager).
        temp_dir: Directory temporanea.
        status_callback: Funzione per log.
        base_w: Larghezza target.
        base_h: Altezza target.
        base_fps: Frame rate target.
        random_generator: Istanza di random.Random per thread-safety. Se None, ne crea una.
        mute_audio: Se True, sostituisce l'audio originale con audio silenzioso per evitare voiceover vecchi.

    Returns:
        Percorso al file MP4 generato, oppure None se errore.
    """
    func = "generate_stock_segment"
    status_callback(f"[{func}] Inizio: durata richiesta = {duration:.2f}s")
    os.makedirs(temp_dir, exist_ok=True)
    
    # Prova a recuperare dal cache se disponibile
    if cache_manager:
        try:
            cached_path = cache_manager.get_cached_stock_segment(
                stock_clips=stock_clips,
                duration=duration,
                base_w=base_w,
                base_h=base_h,
                base_fps=float(base_fps),
                ffmpeg_preset=ffmpeg_preset
            )
            if cached_path:
                # Copia il file dalla cache alla directory temporanea
                out_name = f"stock_cached_{uuid.uuid4().hex}.mp4"
                out_path = os.path.join(temp_dir, out_name)
                shutil.copy2(cached_path, out_path)
                status_callback(f"[{func}] âœ… Segmento recuperato dalla cache: {os.path.basename(out_path)}")
                return out_path
        except Exception as e:
            status_callback(f"[{func}] âš ï¸ Errore cache: {e}, procedo con generazione normale")

    # 1. Clean stock clips - remove timestamp data if present
    # Stock clips may be in format "filepath||start_time||end_time"
    
    # FIX: Assicura che stock_clips sia una lista e non una stringa singola
    # Se stock_clips è una stringa (es. un singolo ID Google Drive), verrà iterata
    # carattere per carattere causando il fallimento di tutte le clip
    if stock_clips is None:
        stock_clips = []
    elif isinstance(stock_clips, str):
        status_callback(f"[{func}] ⚠️ stock_clips è una stringa singola ('{stock_clips[:50]}...'), convertendo in lista")
        stock_clips = [stock_clips]
    elif not isinstance(stock_clips, list):
        status_callback(f"[{func}] ⚠️ stock_clips ha tipo inaspettato ({type(stock_clips).__name__}), convertendo in lista")
        stock_clips = list(stock_clips) if hasattr(stock_clips, '__iter__') else []
    
    cleaned_stock_clips: List[str] = []
    for stock_entry in stock_clips:
        if isinstance(stock_entry, str):
            # If the entry contains timestamp data, extract only the file path
            if "||" in stock_entry:
                file_path = stock_entry.split("||")[0]
                status_callback(f"[{func}] ðŸ”§ Estratto percorso file stock: {file_path} da {stock_entry}")
                cleaned_stock_clips.append(file_path)
            else:
                cleaned_stock_clips.append(stock_entry)
    
    # Use cleaned paths for processing
    stock_clips = cleaned_stock_clips

    # 1.b Gestione esplicita del caso "nessuna clip stock"
    # Questo succede quando non vengono passati stock_clips per un segmento audio
    # (ad es. segmenti oltre i timestamp configurati). Invece di restituire None
    # e lasciare il segmento audio senza video, generiamo un semplice video di
    # backup a colore solido della durata richiesta, così il voiceover rimane
    # comunque coperto da un background visivo.
    if not stock_clips:
        status_callback(f"[{func}] âš ï¸ Nessuna clip stock fornita per questo segmento, genero video di backup a colore solido ({duration:.2f}s)", True)
        try:
            backup_video_path = os.path.join(
                tempfile.gettempdir(),
                f"backup_stock_{uuid.uuid4().hex[:8]}.mp4"
            )
            # Crea clip a colore solido
            backup_clip = ColorClip(
                size=(VIDEO_WIDTH, VIDEO_HEIGHT),
                color=(50, 50, 100),
                duration=duration
            )
            backup_clip = backup_clip.with_fps(VIDEO_FPS)

            # Prova ad aggiungere un testo di overlay per indicare che è un backup
            try:
                try:
                    backup_text = TextClip(txt="Stock Video Backup", fontsize=50, color="white", font=FONT_PATH_DEFAULT or "Arial")
                except TypeError:
                     backup_text = TextClip(text="Stock Video Backup", font_size=50, color="white", font=FONT_PATH_DEFAULT or "Arial")
                backup_text = backup_text.with_position("center").with_duration(duration)
                backup_clip = CompositeVideoClip([backup_clip, backup_text])
            except Exception as text_err:
                status_callback(f"[{func}] âš ï¸ Impossibile aggiungere testo al backup: {text_err}", True)

            # Salva il video di backup
            backup_clip.write_videofile(
                backup_video_path,
                fps=VIDEO_FPS,
                audio=False,
                logger=None
            )
            backup_clip.close()

            status_callback(f"[{func}] âœ… Video di backup generato: {os.path.basename(backup_video_path)} ({duration:.2f}s)", False)
            return backup_video_path
        except Exception as backup_err:
            status_callback(f"[{func}] âŒ Errore generazione video di backup: {backup_err}", True)
            return None

    # 2. Ottieni durata di ogni clip stock usando cache manager se disponibile
    durations: Dict[str, float] = {}
    failed_clips = []
    
    for clip in stock_clips:
        try:
            # Verifica che il file esista prima di tentare di leggerlo
            if not os.path.exists(clip):
                status_callback(f"[{func}] âš ï¸ File non trovato: {os.path.basename(clip)}")
                failed_clips.append(clip)
                continue
                
            # Verifica che il file non sia vuoto
            if os.path.getsize(clip) == 0:
                status_callback(f"[{func}] âš ï¸ File vuoto: {os.path.basename(clip)}")
                failed_clips.append(clip)
                continue
            
            duration_found = False
            
            # Prova prima con cache manager se disponibile
            if cache_manager:
                try:
                    clip_duration = cache_manager.get_clip_duration(clip)
                    if clip_duration is not None and clip_duration > 0:
                        durations[clip] = clip_duration
                        duration_found = True
                        status_callback(f"[{func}] âœ… Durata da cache: {os.path.basename(clip)} = {clip_duration:.2f}s")
                        continue
                except Exception as cache_err:
                    status_callback(f"[{func}] âš ï¸ Errore cache per {os.path.basename(clip)}: {cache_err}")
            
            # Fallback a ffprobe diretto se cache non disponibile o fallito
            if not duration_found:
                try:
                    result = subprocess.run([
                        "ffprobe", "-v", "error",
                        "-show_entries", "format=duration",
                        "-of", "default=noprint_wrappers=1:nokey=1",
                        clip
                    ], capture_output=True, text=True, timeout=30)
                    
                    if result.returncode == 0 and result.stdout.strip():
                        clip_duration = float(result.stdout.strip())
                        if clip_duration > 0:
                            durations[clip] = clip_duration
                            status_callback(f"[{func}] âœ… Durata da ffprobe: {os.path.basename(clip)} = {clip_duration:.2f}s")
                        else:
                            status_callback(f"[{func}] âš ï¸ Durata non valida (0s): {os.path.basename(clip)}")
                            failed_clips.append(clip)
                    else:
                        status_callback(f"[{func}] âš ï¸ FFprobe fallito per {os.path.basename(clip)}: {result.stderr}")
                        failed_clips.append(clip)
                        
                except subprocess.TimeoutExpired:
                    status_callback(f"[{func}] âš ï¸ Timeout lettura durata: {os.path.basename(clip)}")
                    failed_clips.append(clip)
                except (ValueError, subprocess.SubprocessError) as e:
                    status_callback(f"[{func}] âš ï¸ Errore parsing durata {os.path.basename(clip)}: {e}")
                    failed_clips.append(clip)
                    
        except Exception as e:
            status_callback(f"[{func}] âš ï¸ Errore generale lettura {os.path.basename(clip)}: {e}")
            failed_clips.append(clip)
    
    # Report sui risultati
    status_callback(f"[{func}] ðŸ“Š Clip processati: {len(stock_clips)} totali")
    status_callback(f"[{func}] âœ… Clip validi: {len(durations)}")
    if failed_clips:
        status_callback(f"[{func}] âŒ Clip falliti: {len(failed_clips)}")
        for failed_clip in failed_clips[:5]:  # Mostra solo i primi 5 per non intasare i log
            status_callback(f"[{func}]   - {os.path.basename(failed_clip)}")
        if len(failed_clips) > 5:
            status_callback(f"[{func}]   - ... e altri {len(failed_clips) - 5} clip")
    
    if not durations:
        status_callback(f"[{func}] âš ï¸ ATTENZIONE: Nessuna clip valida trovata su {len(stock_clips)} clip forniti")
        status_callback(f"[{func}] ðŸ”„ Attivazione strategie di fallback...")
        
        # STRATEGIA FALLBACK 1: Prova con MoviePy per leggere durate
        status_callback(f"[{func}] ðŸ”§ Fallback 1: Tentativo lettura durate con MoviePy...")
        moviepy_durations = {}
        moviepy_failed = []
        
        for clip in stock_clips:
            try:
                if not os.path.exists(clip) or os.path.getsize(clip) == 0:
                    continue
                    
                # Prova con MoviePy come fallback
                with VideoFileClip(clip) as video_clip:
                    clip_duration = video_clip.duration
                    if clip_duration and clip_duration > 0:
                        moviepy_durations[clip] = clip_duration
                        status_callback(f"[{func}] âœ… MoviePy: {os.path.basename(clip)} = {clip_duration:.2f}s")
                    else:
                        moviepy_failed.append(clip)
                        
            except Exception as e:
                status_callback(f"[{func}] âš ï¸ MoviePy fallito per {os.path.basename(clip)}: {str(e)[:100]}")
                moviepy_failed.append(clip)
        
        if moviepy_durations:
            status_callback(f"[{func}] âœ… Fallback 1 riuscito: {len(moviepy_durations)} clip recuperati con MoviePy")
            durations = moviepy_durations
            # STRATEGIA FALLBACK 2: Genera video di backup con colore solido
            status_callback(f"[{func}] ðŸ”§ Fallback 2: Generazione video di backup con colore solido...")
            
            try:
                # Crea un video di backup con colore solido
                backup_video_path = os.path.join(tempfile.gettempdir(), f"backup_stock_{uuid.uuid4().hex[:8]}.mp4")
                
                # Genera un video colorato di backup
                backup_clip = ColorClip(size=(VIDEO_WIDTH, VIDEO_HEIGHT), color=(50, 50, 100), duration=duration)
                backup_clip = backup_clip.with_fps(VIDEO_FPS)
                
                # Aggiungi un testo di overlay per indicare che Ã¨ un backup
                try:
                    try:
                        backup_text = TextClip(txt="Stock Video Backup", fontsize=50, color='white', font=FONT_PATH_DEFAULT or "Arial")
                    except TypeError:
                         backup_text = TextClip(text="Stock Video Backup", font_size=50, color='white', font=FONT_PATH_DEFAULT or "Arial")
                    backup_text = backup_text.with_position('center').with_duration(duration)
                    backup_clip = CompositeVideoClip([backup_clip, backup_text])
                except Exception as text_err:
                    status_callback(f"[{func}] âš ï¸ Impossibile aggiungere testo al backup: {text_err}")
                
                # Salva il video di backup
                backup_clip.write_videofile(backup_video_path,
                                          fps=VIDEO_FPS,
                                          audio=False)
                backup_clip.close()
                
                status_callback(f"[{func}] âœ… Fallback 2 riuscito: Video di backup generato ({duration:.2f}s)")
                return backup_video_path
                
            except Exception as backup_err:
                status_callback(f"[{func}] âŒ Fallback 2 fallito: {backup_err}")
                
                # STRATEGIA FALLBACK 3: Ritorna None ma con informazioni dettagliate
                status_callback(f"[{func}] âŒ ERRORE CRITICO: Tutti i fallback falliti")
                status_callback(f"[{func}] ðŸ“Š Riepilogo errori:")
                status_callback(f"[{func}] - Clip totali forniti: {len(stock_clips)}")
                status_callback(f"[{func}] - Clip falliti con ffprobe: {len(failed_clips)}")
                status_callback(f"[{func}] - Clip falliti con MoviePy: {len(moviepy_failed)}")
                status_callback(f"[{func}] ðŸ’¡ Suggerimenti:")
                status_callback(f"[{func}] - Verificare che i file video esistano e siano accessibili")
                status_callback(f"[{func}] - Controllare che i file non siano corrotti")
                status_callback(f"[{func}] - Verificare che ffprobe sia installato e funzionante")
                status_callback(f"[{func}] - Considerare di fornire clip stock alternativi")
                return None

    total_avail = sum(durations.values())
    status_callback(f"[{func}] Durata totale disponibile: {total_avail:.2f}s")
    status_callback(f"[{func}] Clip validi trovati: {len(durations)}")

    # 2. Smart clip selection - only use what we need!
    # Usa il generatore fornito o ne crea uno nuovo per garantire thread-safety
    rand = random_generator if random_generator else random.Random()
    
    segments: List[Tuple[str, float]] = []
    available_clips = list(durations.items())
    rand.shuffle(available_clips)
    
    # OPTIMIZATION: If we have much more content than needed, just pick enough clips
    status_callback(f"[{func}] ðŸ” === CONTROLLO OTTIMIZZAZIONE ===", False)
    status_callback(f"[{func}] ðŸ” Durata richiesta: {duration:.2f}s", False)
    status_callback(f"[{func}] ðŸ” Durata totale disponibile: {total_avail:.2f}s", False)
    status_callback(f"[{func}] ðŸ” Soglia ottimizzazione: {duration * 1.5:.2f}s (1.5x richiesta)", False)
    status_callback(f"[{func}] ðŸ” Condizione: {total_avail:.2f}s > {duration * 1.5:.2f}s = {total_avail > duration * 1.5}", False)
    
    if total_avail > duration * 1.5:  # If we have more than 1.5x the needed duration
        status_callback(f"[{func}] ðŸš€ === ATTIVAZIONE OTTIMIZZAZIONE ===", False)
        status_callback(f"[{func}] ðŸš€ OTTIMIZZAZIONE: Durata disponibile ({total_avail:.1f}s) >> richiesta ({duration:.1f}s)")
        status_callback(f"[{func}] ðŸš€ Seleziono clip sufficienti per coprire {duration:.1f}s invece di processare tutti i {len(available_clips)} clip")
        
        remaining = duration
        used_clips = []
        
        # Select clips randomly until we have enough duration to cover the segment
        while remaining > 0:
            # Pick a random clip (allow reuse if we've used all clips)
            if not available_clips:
                available_clips = list(durations.items())
                rand.shuffle(available_clips)
                status_callback(f"[{func}] Ricaricata lista clip per coprire {remaining:.2f}s rimanenti")
                
            clip_path, clip_dur = available_clips.pop(0)
            
            # Use the full clip duration or what's needed
            take = min(clip_dur, remaining)
            if take > 0:
                segments.append((clip_path, take))
                used_clips.append(os.path.basename(clip_path))
                status_callback(f"[{func}] âœ… Selezionato {os.path.basename(clip_path)} dur={take:.2f}s (originale: {clip_dur:.2f}s)")
                remaining -= take
                
        status_callback(f"[{func}] ðŸ“Š Copertura completata con {len(segments)} clip (invece di {len(durations)} micro-segmenti)")
                
    else:
        # Fallback to old method if we don't have much excess content
        status_callback(f"[{func}] ðŸ“Š === METODO STANDARD ===", False)
        status_callback(f"[{func}] ðŸ“Š Uso metodo standard - durata limitata")
        status_callback(f"[{func}] ðŸ“Š Processando tutti i {len(available_clips)} clip disponibili", False)
        remaining = duration
        
        # Calcola una durata massima per segmento per favorire l'uso di piÃ¹ clip
        max_segment_duration = duration / max(1, len(available_clips))  # Divide la durata tra i clip disponibili
        status_callback(f"[{func}] Durata massima per segmento: {max_segment_duration:.2f}s")

        while remaining > 0 and available_clips:
            # Seleziona un clip casuale
            clip_idx = rand.randint(0, len(available_clips) - 1)
            clip_path, clip_dur = available_clips[clip_idx]
            
            # Limita la durata del segmento per favorire varietÃ 
            take = min(clip_dur, remaining, max_segment_duration)
            if take > 0:
                segments.append((clip_path, take))
                status_callback(f"[{func}] Aggiunto {os.path.basename(clip_path)} dur={take:.2f}s")
                remaining -= take
            
            # Rimuovi il clip dalla lista temporaneamente per favorire varietÃ 
            available_clips.pop(clip_idx)
            
            # Se la lista Ã¨ vuota, ricreala e mescola
            if not available_clips and remaining > 0:
                available_clips = list(durations.items())
                rand.shuffle(available_clips)
                status_callback(f"[{func}] Ricaricata lista clip per coprire {remaining:.2f}s rimanenti")

    if remaining > 0:
        status_callback(f"[{func}] âš ï¸ Durata non coperta completamente: mancano {remaining:.2f}s")
        return None

    status_callback(f"[{func}] Totale segmenti selezionati: {len(segments)}")
    status_callback(f"[{func}] Clip uniche utilizzate: {len(set(segment[0] for segment in segments))}")

    # 3. Genera concat con filter_complex
    out_name = f"stock_random_{uuid.uuid4().hex}.mp4"
    out_path = os.path.join(temp_dir, out_name)
    
    # Debug: Check segment count and method selection
    status_callback(f"[{func}] Debug: Numero segmenti = {len(segments)}, soglia = 25")

    # Estimate command line length for dynamic method selection
    estimated_cmd_length = 200  # Base ffmpeg command
    for clip_path, _ in segments:
        estimated_cmd_length += len(clip_path) + 50  # Path + filter overhead per segment
    
    # Use file list method if too many segments OR if command would be too long
    use_file_list = len(segments) > 25 or estimated_cmd_length > 7000

    def build_file_list_concat(target_out_path: str) -> Optional[str]:
        """Generate stock segment via temporary per-clip renders and concat demuxer."""
        temp_segment_files: List[str] = []
        concat_list_path: Optional[str] = None
        try:
            for idx, (clip_path, clip_dur) in enumerate(segments):
                temp_segment_name = f"temp_segment_{idx}_{uuid.uuid4().hex[:8]}.mp4"
                temp_segment_path = os.path.join(temp_dir, temp_segment_name)

                if mute_audio:
                    # Generate silent audio to mute stock segment
                    segment_cmd = [
                        "ffmpeg", "-y", "-hide_banner",
                        "-i", clip_path,
                        "-filter_complex",
                        (
                            f"[0:v]scale={base_w}:{base_h}:force_original_aspect_ratio=decrease,"
                            f"pad={base_w}:{base_h}:(ow-iw)/2:(oh-ih)/2[vout];"
                            f"anullsrc=r=44100:cl=stereo:d={clip_dur:.6f}[aout]"
                        ),
                        "-map", "[vout]", "-map", "[aout]",
                        "-t", f"{clip_dur:.6f}",
                        "-c:v", "libx264",
                        "-preset", "fast",
                        "-pix_fmt", "yuv420p",
                        "-r", str(base_fps),
                        "-c:a", "aac", "-b:a", "192k", "-ar", "44100", "-ac", "2",
                        temp_segment_path
                    ]
                else:
                    # Use original audio from the clip
                    segment_cmd = [
                        "ffmpeg", "-y", "-hide_banner",
                        "-i", clip_path,
                        "-vf",
                        (
                            f"scale={base_w}:{base_h}:force_original_aspect_ratio=decrease,"
                            f"pad={base_w}:{base_h}:(ow-iw)/2:(oh-ih)/2"
                        ),
                        "-t", f"{clip_dur:.6f}",
                        "-c:v", "libx264",
                        "-preset", "fast",
                        "-pix_fmt", "yuv420p",
                        "-r", str(base_fps),
                        "-c:a", "aac", "-b:a", "192k", "-ar", "44100", "-ac", "2",
                        temp_segment_path
                    ]

                try:
                    subprocess.run(
                        segment_cmd,
                        check=True,
                        capture_output=True,
                        text=True,
                        encoding="utf-8",
                        errors="replace"
                    )
                    temp_segment_files.append(temp_segment_path)
                    if idx % 10 == 0:
                        status_callback(f"[{func}] Processati {idx+1}/{len(segments)} segmenti temporanei")
                except subprocess.CalledProcessError as e:
                    status_callback(f"[{func}] ⚠️ Errore generazione segmento {idx}: {e}")
                    if e.stderr:
                        status_callback(f"[{func}] FFmpeg stderr: {e.stderr[:200]}...")
                    continue

            if not temp_segment_files:
                status_callback(f"[{func}] ❌ Nessun segmento temporaneo generato")
                return None

            if len(temp_segment_files) < len(segments) // 2:
                status_callback(f"[{func}] ⚠️ Solo {len(temp_segment_files)}/{len(segments)} segmenti generati con successo")

            concat_list_path = os.path.join(temp_dir, f"concat_list_{uuid.uuid4().hex[:8]}.txt")
            with open(concat_list_path, "w", encoding="utf-8") as file_list:
                for temp_file in temp_segment_files:
                    file_list.write(f"file '{os.path.abspath(temp_file)}'\n")

            final_cmd = [
                "ffmpeg", "-y", "-hide_banner",
                "-f", "concat",
                "-safe", "0",
                "-i", concat_list_path,
                "-c", "copy",
                "-t", f"{duration:.6f}",
                target_out_path
            ]

            subprocess.run(
                final_cmd,
                check=True,
                capture_output=True,
                text=True,
                encoding="utf-8",
                errors="replace"
            )

            return target_out_path
        except subprocess.CalledProcessError as e:
            status_callback(f"[{func}] Errore metodo file list: {e}")
            if e.stderr:
                status_callback(f"[{func}] FFmpeg stderr: {e.stderr[:400]}...")
            return None
        except Exception as e:
            status_callback(f"[{func}] Errore imprevisto metodo file list: {e}")
            return None
        finally:
            for temp_file in temp_segment_files:
                try:
                    os.remove(temp_file)
                except Exception:
                    pass
            if concat_list_path and os.path.exists(concat_list_path):
                try:
                    os.remove(concat_list_path)
                except Exception:
                    pass

    # Check if we have too many segments for Windows command line limits
    # Windows has a ~8192 character limit for command lines
    # With optimized selection, we should have far fewer segments now
    if len(segments) == 1:
        status_callback(f"[{func}] Uso percorso semplificato per singolo segmento")
        clip_path, clip_dur = segments[0]
        target_duration = min(duration, clip_dur)
        if mute_audio:
            # Generate silent audio to mute stock segment
            audio_filter = f"anullsrc=r=44100:cl=stereo:d={target_duration:.6f}[aout]"
        else:
            # Use original audio from the clip
            audio_filter = f"[0:a]atrim=0:{target_duration:.6f},asetpts=PTS-STARTPTS[aout]"
        
        single_filter = (
            f"[0:v]trim=0:{target_duration:.6f},scale={base_w}:{base_h}:force_original_aspect_ratio=decrease,"
            f"pad={base_w}:{base_h}:(ow-iw)/2:(oh-ih)/2,setpts=PTS-STARTPTS[vout];"
            f"{audio_filter}"
        )
        cmd = [
            "ffmpeg", "-y", "-hide_banner", "-nostdin",
            "-i", clip_path,
            "-filter_complex", single_filter,
            "-map", "[vout]", "-map", "[aout]",
            "-c:v", "libx264",
            "-preset", "medium",
            "-pix_fmt", "yuv420p",
            "-r", str(base_fps),
            "-c:a", "aac", "-b:a", "192k", "-ar", "44100", "-ac", "2",
            "-t", f"{target_duration:.6f}",
            out_path
        ]
        result = run_ffmpeg_command(
            cmd,
            f"[{func}] FFmpeg singolo segmento",
            status_callback=status_callback,
            progress=False,
            timeout=90
        )
        if result.returncode != 0 or not os.path.exists(out_path) or os.path.getsize(out_path) < 1000:
            status_callback(f"[{func}] Errore generazione singolo segmento", True)
            return None
    elif use_file_list:  # Use file list method based on segment count or command length
        if len(segments) > 25:
            status_callback(f"[{func}] ⚠️ Troppi segmenti ({len(segments)}), uso metodo file list per evitare limiti Windows")
        else:
            status_callback(f"[{func}] ⚠️ Comando troppo lungo (stima: {estimated_cmd_length} caratteri), uso metodo file list per evitare limiti Windows")

        if not build_file_list_concat(out_path):
            return None

    else:
        # Original method for smaller numbers of segments
        status_callback(f"[{func}] Uso metodo originale filter_complex per {len(segments)} segmenti")
        inputs: List[str] = []
        filter_parts: List[str] = []
        concat_streams: List[str] = []
        
        for idx, (clip_path, clip_dur) in enumerate(segments):
            inputs += ["-i", clip_path]
            filter_parts.append(f"[{idx}:v]trim=0:{clip_dur:.6f},scale={base_w}:{base_h}:force_original_aspect_ratio=decrease,pad={base_w}:{base_h}:(ow-iw)/2:(oh-ih)/2,setpts=PTS-STARTPTS[v{idx}]")
            
            if mute_audio:
                # Generate silent audio to mute stock clips
                filter_parts.append(f"anullsrc=r=44100:cl=stereo,atrim=0:{clip_dur:.6f},asetpts=PTS-STARTPTS[a{idx}]")
            else:
                # Use original audio from the clip
                filter_parts.append(f"[{idx}:a]atrim=0:{clip_dur:.6f},asetpts=PTS-STARTPTS[a{idx}]")
            
            # Add streams in the correct order for concat: [v0][a0][v1][a1]...
            concat_streams.extend([f"[v{idx}]", f"[a{idx}]"])

        filter_parts.append(f"{''.join(concat_streams)}concat=n={len(segments)}:v=1:a=1[outv][outa]")
        filter_complex = ";".join(filter_parts)

        cmd = [
            "ffmpeg", "-y", "-hide_banner", "-nostdin"
        ] + inputs + [
            "-filter_complex", filter_complex,
            "-map", "[outv]", "-map", "[outa]",
            "-c:v", "libx264",
            "-preset", "medium",
            "-pix_fmt", "yuv420p",
            "-r", str(base_fps),
            "-c:a", "aac", "-b:a", "192k", "-ar", "44100", "-ac", "2",
            "-t", f"{duration:.6f}",
            out_path
        ]
        
        try:
            subprocess.run(cmd, check=True, capture_output=True, text=True, encoding='utf-8', errors='replace')
        except subprocess.CalledProcessError as e:
            status_callback(f"[{func}] Errore FFmpeg filter_complex: {e}")
            if e.stderr:
                status_callback(f"[{func}] Dettagli FFmpeg: {e.stderr[:400]}...")
            status_callback(f"[{func}] Tentativo fallback metodo file list dopo errore filter_complex")
            if not build_file_list_concat(out_path):
                return None
    
    if len(segments) > 1:
        status_callback(f"[{func}] Eseguo FFmpeg concat...")
    else:
        status_callback(f"[{func}] FFmpeg completato per singolo segmento")
    
    # Common success handling for both methods
    status_callback(f"[{func}] Segmento generato: {os.path.basename(out_path)}")

    # Add short sound effect into stock segment audio (pre-VO method)
    try:
        if DEFAULT_SOUND_EFFECTS:
            import random as _rnd
            sfx_path = _rnd.choice(DEFAULT_SOUND_EFFECTS)
            if os.path.exists(sfx_path):
                sfx_dur = min(0.8, max(0.2, duration * 0.3))
                out_with_sfx = os.path.join(temp_dir, f"{Path(out_path).stem}_sfx_{uuid.uuid4().hex[:6]}.mp4")
                filter_sfx = (
                    f"[0:a]aresample=sample_rate=44100,aformat=channel_layouts=stereo:sample_fmts=s16,asetpts=PTS-STARTPTS[b];"
                    f"[1:a]aresample=sample_rate=44100,aformat=channel_layouts=stereo:sample_fmts=s16,atrim=0:{sfx_dur:.3f},asetpts=PTS-STARTPTS[s];"
                    f"[b][s]amix=inputs=2:weights='1.0 0.2':duration=first:normalize=0:dropout_transition=0[aout]"
                )
                cmd_sfx = [
                    "ffmpeg", "-y", "-hide_banner", "-loglevel", "error",
                    "-i", out_path,
                    "-i", sfx_path,
                    "-filter_complex", filter_sfx,
                    "-map", "0:v:0", "-map", "[aout]",
                    "-c:v", "copy",
                    "-c:a", "aac", "-b:a", "192k", "-ar", "44100", "-ac", "2",
                    "-shortest",
                    out_with_sfx
                ]
                res = subprocess.run(cmd_sfx, capture_output=True, text=True)
                if res.returncode == 0 and os.path.exists(out_with_sfx) and os.path.getsize(out_with_sfx) > 1000:
                    try:
                        os.remove(out_path)
                    except Exception:
                        pass
                    out_path = out_with_sfx
                    status_callback(f"[{func}] SFX inserito nel segmento stock")
    except Exception as e:
        status_callback(f"[{func}] ⚠️ SFX non inserito: {e}", True)
    
    # Garbage collection after heavy stock segment generation
    gc.collect()
    
    # Salva nella cache se disponibile (cache video disabilitata per risparmiare memoria)
    if cache_manager:
        try:
            cache_success = cache_manager.cache_stock_segment(
                stock_clips=stock_clips,
                duration=duration,
                base_w=base_w,
                base_h=base_h,
                base_fps=base_fps,
                ffmpeg_preset=ffmpeg_preset,
                output_path=out_path
            )
            # Non stampare messaggio se cache è disabilitata (ritorna False)
            # Cache video disabilitata per risparmiare memoria
            # if cache_success:
            #     status_callback(f"[{func}] Segmento salvato nella cache")
        except Exception as e:
            status_callback(f"[{func}] âš ï¸ Errore salvataggio cache: {e}")
    
    return out_path


def get_clip_duration(filepath: str) -> float:
    """
    Restituisce la durata di un file video in secondi usando cache FFmpeg completa.
    
    Args:
        filepath: Percorso del file video.
    
    Returns:
        Durata in secondi, o 0.0 in caso di errore.
    """
    # Validazione preliminare del file
    try:
        if not filepath or not isinstance(filepath, str):
            logging.error(f"Percorso file non valido: {filepath}")
            return 0.0
            
        if not os.path.exists(filepath):
            logging.error(f"File non trovato: {filepath}")
            return 0.0
            
        if os.path.getsize(filepath) == 0:
            logging.error(f"File vuoto: {filepath}")
            return 0.0
            
    except (OSError, IOError) as e:
        logging.error(f"Errore accesso file {filepath}: {e}")
        return 0.0
    
    # Usa la nuova cache FFmpeg completa se disponibile
    if cache_manager:
        try:
            duration = cache_manager.get_video_duration_cached(filepath)
            if duration is not None and duration > 0:
                return duration
            else:
                logging.warning(f"Cache ha restituito durata non valida per {filepath}: {duration}")
        except Exception as e:
            logging.warning(f"Cache FFmpeg fallita per {filepath}: {e}")
    
    # Fallback a ffprobe diretto
    try:
        command = [
            "ffprobe", "-v", "error", "-show_entries", "format=duration",
            "-of", "json", filepath
        ]
        result = subprocess.run(
            command,
            capture_output=True,
            text=True,
            check=True,
            timeout=30  # Timeout per evitare blocchi
        )
        
        if not result.stdout.strip():
            logging.error(f"FFprobe non ha restituito output per {filepath}")
            return 0.0
            
        data = json.loads(result.stdout)
        format_info = data.get("format", {})
        
        if "duration" not in format_info:
            logging.error(f"Durata non trovata nei metadati di {filepath}")
            return 0.0
            
        duration = float(format_info["duration"])
        
        if duration <= 0:
            logging.error(f"Durata non valida per {filepath}: {duration}")
            return 0.0
            
        return duration
        
    except subprocess.TimeoutExpired:
        logging.error(f"Timeout durante lettura durata di {filepath}")
        return 0.0
    except subprocess.CalledProcessError as e:
        logging.error(f"FFprobe fallito per {filepath}: {e.stderr if e.stderr else 'Nessun errore specifico'}")
        return 0.0
    except (ValueError, json.JSONDecodeError) as e:
        logging.error(f"Errore parsing output FFprobe per {filepath}: {e}")
        return 0.0
    except FileNotFoundError:
        logging.error(f"FFprobe non trovato nel sistema")
        return 0.0
    except Exception as e:
        logging.error(f"Errore imprevisto nel calcolare la durata di {filepath}: {e}")
        return 0.0
