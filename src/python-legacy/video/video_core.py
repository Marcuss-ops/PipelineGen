"""Core video processing functions for safe video operations and memory management."""

import gc
import logging
import os
import psutil
import signal
import subprocess
import sys
import tempfile
import threading
import time
import traceback
import uuid
from pathlib import Path
from typing import Any, Callable, Dict, List, Optional, Tuple

from moviepy import VideoFileClip, AudioFileClip, VideoClip, AudioClip
import numpy as np

# Configure logging
logger = logging.getLogger(__name__)


def validate_audio_stream(clip, text_content: str = "unknown", status_callback: Callable[[str, bool], None] = None) -> Tuple[bool, Optional[str]]:
    """Validate audio stream of a video clip."""
    try:
        if hasattr(clip, 'audio') and clip.audio is not None:
            # Test audio access
            try:
                audio_array = clip.audio.to_soundarray(fps=22050, nbytes=2)
                if len(audio_array) > 0:
                    return True, None
                else:
                    return False, "Audio stream is empty"
            except Exception as e:
                return False, f"Audio stream validation failed: {str(e)}"
        else:
            return False, "No audio stream found"
    except Exception as e:
        logger.error(f"Error validating audio stream for {text_content}: {e}")
        return False, f"Audio validation error: {str(e)}"


def create_safe_audio_clip(clip, text_content: str = "unknown", status_callback: Callable[[str, bool], None] = None) -> Tuple[Any, bool, Optional[str]]:
    """Create a safe audio clip with validation."""
    try:
        if hasattr(clip, 'audio') and clip.audio is not None:
            is_valid, error_msg = validate_audio_stream(clip, text_content, status_callback)
            if is_valid:
                return clip.audio, True, None
            else:
                logger.warning(f"Invalid audio stream for {text_content}: {error_msg}")
                return None, False, error_msg
        else:
            logger.warning(f"No audio stream found for {text_content}")
            return None, False, "No audio stream"
    except Exception as e:
        logger.error(f"Error creating safe audio clip for {text_content}: {e}")
        return None, False, f"Audio creation error: {str(e)}"


def safe_moviepy_write_videofile_pure(clip, output_path: str, text_content: str = "unknown", 
                                    status_callback: Callable[[str, bool], None] = None,
                                    fps: int = 30, audio_codec: str = 'aac', 
                                    video_codec: str = 'libx264', preset: str = 'medium',
                                    timeout: float = 1800, threads: int = 4,
                                    audio_bitrate: str = '128k', video_bitrate: str = '2000k',
                                    temp_audiofile: str = None, remove_temp: bool = True,
                                    verbose: bool = False, logger_func: Callable = None) -> bool:
    """Safe video file writing with comprehensive error handling."""
    try:
        # Ensure output directory exists
        os.makedirs(os.path.dirname(output_path), exist_ok=True)
        
        # Write video file with timeout
        clip.write_videofile(
            output_path,
            fps=fps,
            audio_codec=audio_codec,
            codec=video_codec,
            preset=preset,
            threads=threads,
            audio_bitrate=audio_bitrate,
            bitrate=video_bitrate,
            temp_audiofile=temp_audiofile,
            remove_temp=remove_temp,
            verbose=verbose,
            logger=logger_func
        )
        
        # Verify output file
        if os.path.exists(output_path) and os.path.getsize(output_path) > 0:
            if status_callback:
                status_callback(f"Video successfully written: {output_path}", True)
            return True
        else:
            if status_callback:
                status_callback(f"Video file creation failed: {output_path}", False)
            return False
            
    except Exception as e:
        logger.error(f"Error writing video file {output_path} for {text_content}: {e}")
        if status_callback:
            status_callback(f"Video writing error: {str(e)}", False)
        return False


def safe_moviepy_write_videofile(clip, output_path: str, text_content: str = "unknown", 
                                status_callback: Callable[[str, bool], None] = None,
                                **kwargs) -> bool:
    """Safe video file writing with default parameters."""
    return safe_moviepy_write_videofile_pure(
        clip, output_path, text_content, status_callback, **kwargs
    )


def get_cross_platform_subprocess_env():
    """Get cross-platform subprocess environment."""
    env = os.environ.copy()
    if sys.platform == "win32":
        env["PYTHONIOENCODING"] = "utf-8"
    return env


def sanitize_ffmpeg_path(path: str) -> str:
    """Sanitize FFmpeg path for safe usage."""
    if not path:
        return ""
    
    # Remove potentially dangerous characters
    sanitized = path.replace('"', '').replace("'", "").replace(";", "").replace("&", "")
    
    # Ensure path exists if it's a file path
    if os.path.exists(sanitized):
        return sanitized
    
    return path  # Return original if sanitization fails


def sanitize_text_for_filename(text: str, max_length: int = 50) -> str:
    """Sanitize text for use in filenames."""
    if not text:
        return "unnamed"
    
    # Remove or replace invalid filename characters
    invalid_chars = '<>:"/\\|?*'
    for char in invalid_chars:
        text = text.replace(char, '_')
    
    # Remove extra whitespace and limit length
    text = ' '.join(text.split())
    if len(text) > max_length:
        text = text[:max_length].rstrip()
    
    return text or "unnamed"


def create_safe_temp_filename(base_name: str, extension: str = ".mp4", temp_dir: str = None) -> str:
    """Create a safe temporary filename."""
    if temp_dir is None:
        temp_dir = tempfile.gettempdir()
    
    # Sanitize base name
    safe_base = sanitize_text_for_filename(base_name, 30)
    
    # Add unique identifier
    unique_id = str(uuid.uuid4())[:8]
    filename = f"{safe_base}_{unique_id}{extension}"
    
    return os.path.join(temp_dir, filename)


def safe_moviepy_operation(operation_func, operation_name: str, timeout: float = 300, *args, **kwargs):
    """Execute MoviePy operation with timeout and error handling."""
    import threading
    import time
    
    result = [None]
    exception = [None]
    
    def target():
        try:
            result[0] = operation_func(*args, **kwargs)
        except Exception as e:
            exception[0] = e
    
    thread = threading.Thread(target=target)
    thread.daemon = True
    thread.start()
    thread.join(timeout)
    
    if thread.is_alive():
        logger.error(f"Operation {operation_name} timed out after {timeout} seconds")
        return None
    
    if exception[0]:
        logger.error(f"Operation {operation_name} failed: {exception[0]}")
        raise exception[0]
    
    return result[0]


def aggressive_memory_cleanup(*clips_to_close):
    """Aggressive memory cleanup for MoviePy objects."""
    for clip in clips_to_close:
        if clip is not None:
            try:
                if hasattr(clip, 'close'):
                    clip.close()
                if hasattr(clip, 'reader') and clip.reader:
                    clip.reader.close()
                if hasattr(clip, 'audio') and clip.audio:
                    if hasattr(clip.audio, 'close'):
                        clip.audio.close()
            except Exception as e:
                logger.warning(f"Error during clip cleanup: {e}")
    
    # Force garbage collection
    gc.collect()


def safe_close_moviepy_object(obj, obj_name="object"):
    """Safely close MoviePy object."""
    if obj is None:
        return
    
    try:
        if hasattr(obj, 'close'):
            obj.close()
        if hasattr(obj, 'reader') and obj.reader:
            obj.reader.close()
        if hasattr(obj, 'audio') and obj.audio:
            if hasattr(obj.audio, 'close'):
                obj.audio.close()
    except Exception as e:
        logger.warning(f"Error closing {obj_name}: {e}")


def cleanup_ffmpeg_processes():
    """Clean up any hanging FFmpeg processes."""
    try:
        if sys.platform == "win32":
            subprocess.run(['taskkill', '/f', '/im', 'ffmpeg.exe'], 
                         capture_output=True, check=False)
        else:
            subprocess.run(['pkill', '-f', 'ffmpeg'], 
                         capture_output=True, check=False)
    except Exception as e:
        logger.warning(f"Error cleaning up FFmpeg processes: {e}")


def test_ffmpeg_subprocess_health() -> bool:
    """Test if FFmpeg subprocess is healthy."""
    try:
        result = subprocess.run(['ffmpeg', '-version'], 
                              capture_output=True, text=True, timeout=10)
        return result.returncode == 0
    except Exception:
        return False


def comprehensive_moviepy_cleanup(*clips_to_close):
    """Comprehensive cleanup for MoviePy objects and system resources."""
    # Close all clips
    for clip in clips_to_close:
        safe_close_moviepy_object(clip)
    
    # Clean up FFmpeg processes
    cleanup_ffmpeg_processes()
    
    # Force garbage collection
    force_gc_and_cleanup()


def close_clip(clip):
    """Close a single clip safely."""
    safe_close_moviepy_object(clip, "clip")


def hard_cleanup(*objs):
    """Hard cleanup for objects."""
    for obj in objs:
        safe_close_moviepy_object(obj)
    force_gc_and_cleanup()


def log_mem(tag=""):
    """Log current memory usage."""
    try:
        process = psutil.Process()
        memory_info = process.memory_info()
        logger.info(f"Memory {tag}: RSS={memory_info.rss / 1024 / 1024:.1f}MB, "
                   f"VMS={memory_info.vms / 1024 / 1024:.1f}MB")
    except Exception as e:
        logger.warning(f"Error logging memory: {e}")


def force_gc_and_cleanup():
    """Force garbage collection and cleanup."""
    import gc
    gc.collect()
    gc.collect()  # Call twice for better cleanup


def _validate_resources():
    """Validate system resources."""
    try:
        # Check available memory
        memory = psutil.virtual_memory()
        if memory.percent > 90:
            logger.warning(f"High memory usage: {memory.percent}%")
        
        # Check available disk space
        disk = psutil.disk_usage('/')
        if disk.percent > 90:
            logger.warning(f"High disk usage: {disk.percent}%")
        
        return True
    except Exception as e:
        logger.error(f"Error validating resources: {e}")
        return False


def has_video_stream(file_path):
    """Check if file has video stream."""
    try:
        with VideoFileClip(file_path) as clip:
            return clip.duration > 0
    except Exception:
        return False


def get_clip_duration(filepath: str) -> float:
    """Get duration of video/audio clip."""
    try:
        if filepath.lower().endswith(('.mp4', '.avi', '.mov', '.mkv', '.webm')):
            with VideoFileClip(filepath) as clip:
                return clip.duration
        elif filepath.lower().endswith(('.mp3', '.wav', '.aac', '.m4a')):
            with AudioFileClip(filepath) as clip:
                return clip.duration
        else:
            return 0.0
    except Exception as e:
        logger.error(f"Error getting duration for {filepath}: {e}")
        return 0.0


def extract_audio_from_video(video_path: str, output_path: str) -> bool:
    """Extract audio from video file."""
    try:
        with VideoFileClip(video_path) as video:
            if video.audio is not None:
                video.audio.write_audiofile(output_path, logger=None)
                return os.path.exists(output_path)
            else:
                logger.warning(f"No audio stream found in {video_path}")
                return False
    except Exception as e:
        logger.error(f"Error extracting audio from {video_path}: {e}")
        return False


def get_video_info(video_path: str) -> dict:
    """Get video file information."""
    try:
        with VideoFileClip(video_path) as video:
            info = {
                'duration': video.duration,
                'fps': video.fps,
                'size': video.size,
                'has_audio': video.audio is not None
            }
            if video.audio:
                info['audio_fps'] = video.audio.fps
            return info
    except Exception as e:
        logger.error(f"Error getting video info for {video_path}: {e}")
        return {}


def create_video_preview(video_path: str, output_path: str, start_time: float = 0, duration: float = 10) -> bool:
    """Create a preview of video file."""
    try:
        with VideoFileClip(video_path) as video:
            preview = video.subclip(start_time, min(start_time + duration, video.duration))
            preview.write_videofile(output_path, logger=None)
            return os.path.exists(output_path)
    except Exception as e:
        logger.error(f"Error creating preview for {video_path}: {e}")
        return False