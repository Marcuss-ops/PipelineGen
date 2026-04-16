"""FFmpeg utilities for video processing."""

from datetime import time
import logging
import os
import shutil
import subprocess
import sys
from typing import Any, Callable, List, Optional, Union

from moviepy import ColorClip, CompositeVideoClip, TextClip

logger = logging.getLogger(__name__)

# Try to get a sensible default FPS for overlay normalization
try:
    from refactored.config import BASE_FPS_DEFAULT as MERGE_FPS
except Exception:
    try:
        from config import BASE_FPS_DEFAULT as MERGE_FPS
    except Exception:
        MERGE_FPS = 30


def get_audio_duration_ffmpeg(audio_path: str) -> float:
    """Get audio duration using FFprobe (more reliable than FFmpeg progress parsing)."""
    try:
        # First try with ffprobe (reliable metadata extraction)
        cmd_ffprobe = [
            'ffprobe', '-v', 'error',
            '-show_entries', 'format=duration',
            '-of', 'default=noprint_wrappers=1:nokey=1',
            audio_path
        ]

        result_ffprobe = subprocess.run(cmd_ffprobe, capture_output=True, text=True, timeout=60)

        if result_ffprobe.returncode == 0:
            duration_str = result_ffprobe.stdout.strip()
            if duration_str:
                try:
                    return float(duration_str)
                except ValueError:
                    pass

        # Fallback to python audio processing if ffprobe fails
        try:
            from pydub import AudioSegment
            audio = AudioSegment.from_file(audio_path)
            return len(audio) / 1000.0  # pydub uses milliseconds
        except ImportError:
            logger.warning("pydub not available, continuing with fallback")
        except Exception as e:
            logger.warning(f"pydub fallback failed: {e}")

        # Last fallback: MoviePy audioclip duration
        try:
            from moviepy.audio.AudioClip import AudioFileClip
            with AudioFileClip(audio_path) as audio_clip:
                return audio_clip.duration
        except Exception as e:
            logger.error(f"MoviePy fallback also failed: {e}")
            return 0.0

    except Exception as e:
        logger.error(f"Error getting audio duration with FFprobe/fallbacks: {e}")
        return 0.0


def get_audio_codec_ffmpeg(audio_path: str) -> str:
    """Get audio codec using FFmpeg."""
    try:
        cmd = ['ffprobe', '-v', 'quiet', '-show_entries', 'stream=codec_name', 
               '-of', 'csv=p=0', audio_path]
        
        result = subprocess.run(cmd, capture_output=True, text=True, timeout=10)
        
        if result.returncode == 0:
            return result.stdout.strip()
        else:
            return "unknown"
    except Exception as e:
        logger.error(f"Error getting audio codec with FFmpeg: {e}")
        return "unknown"


def run_ffmpeg_command(cmd: list, stage_desc: str, status_callback: Callable = print,
                      progress: bool = False, timeout: Optional[float] = None) -> subprocess.CompletedProcess:
    """Run FFmpeg command with error handling and progress tracking."""
    try:
        if status_callback:
            status_callback(f"Starting {stage_desc}...")
        
        # Set up environment
        env = os.environ.copy()
        if sys.platform == "win32":
            env["PYTHONIOENCODING"] = "utf-8"
        
        # Run command
        if progress:
            # Stream only stderr (FFmpeg writes progress to stderr) to avoid stdout pipe deadlocks on Windows
            import time as _time
            process = subprocess.Popen(
                cmd,
                stdout=subprocess.DEVNULL,
                stderr=subprocess.PIPE,
                text=True,
                env=env
            )

            stderr_lines = []
            start_ts = _time.monotonic()

            while True:
                line = process.stderr.readline() if process.stderr else ''
                if line:
                    stderr_lines.append(line)
                    if status_callback and 'time=' in line:
                        status_callback(f"{stage_desc}: {line.strip()}")

                # Exit conditions
                if process.poll() is not None:
                    # Drain remaining stderr
                    if process.stderr:
                        remainder = process.stderr.read()
                        if remainder:
                            stderr_lines.append(remainder)
                    break

                # Timeout guard for long-running/no-output cases
                if timeout and (_time.monotonic() - start_ts) > float(timeout):
                    process.kill()
                    timed_out_msg = f"\n[run_ffmpeg_command] Timed out after {timeout}s"
                    stderr_lines.append(timed_out_msg)
                    break

            result = subprocess.CompletedProcess(
                cmd,
                process.returncode if process.returncode is not None else 1,
                '',
                ''.join(stderr_lines)
            )
        else:
            result = subprocess.run(
                cmd, capture_output=True, text=True, 
                timeout=timeout, env=env, check=False
            )
        
        if result.returncode == 0:
            if status_callback:
                status_callback(f"{stage_desc} completed successfully")
        else:
            if status_callback:
                status_callback(f"{stage_desc} failed: {result.stderr}")
            logger.error(f"FFmpeg command failed: {' '.join(cmd)}")
            logger.error(f"Error: {result.stderr}")
        
        return result
        
    except subprocess.TimeoutExpired:
        logger.error(f"FFmpeg command timed out: {' '.join(cmd)}")
        if status_callback:
            status_callback(f"{stage_desc} timed out")
        raise
    except Exception as e:
        logger.error(f"Error running FFmpeg command: {e}")
        if status_callback:
            status_callback(f"{stage_desc} error: {str(e)}")
        raise


def sanitize_ffmpeg_path(path: str) -> str:
    """
    Sanitizes file paths for FFmpeg compatibility across platforms.
    Enhanced to handle special characters and encoding issues.
    """
    if not path:
        return path
    
    # If callers accidentally pass a quoted path (e.g. "'/tmp/file.mp4'"), strip wrappers.
    # This function is used to build argv lists for subprocess, so we must NOT shell-quote paths.
    while len(path) >= 2 and (
        (path[0] == path[-1] and path[0] in ("'", '"'))
        or (path.startswith("''") and path.endswith("''"))
    ):
        if path.startswith("''") and path.endswith("''"):
            path = path[2:-2]
        else:
            path = path[1:-1]

    # Convert to absolute path
    abs_path = os.path.abspath(path)
    
    # On Windows, handle long path names and special characters
    if sys.platform.startswith('win'):
        # Handle UNC paths and long paths
        if len(abs_path) > 260:
            # Use extended path syntax for long paths
            if not abs_path.startswith('\\\\?\\'):
                abs_path = '\\\\?\\' + abs_path
        
        # Escape problematic characters for Windows
        # Replace problematic characters that can cause FFmpeg issues
        abs_path = abs_path.replace('&', '_and_')
        abs_path = abs_path.replace('%', '_percent_')
        abs_path = abs_path.replace('(', '_')
        abs_path = abs_path.replace(')', '_')
        abs_path = abs_path.replace('[', '_')
        abs_path = abs_path.replace(']', '_')
        abs_path = abs_path.replace('{', '_')
        abs_path = abs_path.replace('}', '_')
        
    # Unix/Linux: return the raw absolute path (do not replace brackets so existing files are found).
    # Important: when using subprocess with argv lists (shell=False), quoting via shlex.quote()
    # would become part of the filename and break FFmpeg I/O (e.g. "'/tmp/out.mp4'").
    
    return abs_path

def sanitize_text_for_filename(text: str, max_length: int = 50) -> str:
    """
    Sanitizes text to be safely used in filenames for FFmpeg operations.
    Removes/replaces special characters that can cause subprocess issues.
    """
    if not text:
        return "untitled"
    
    import unicodedata
    import re
    
    # Normalize unicode characters
    text = unicodedata.normalize('NFKD', text)
    
    # Remove or replace problematic characters
    # Characters that commonly cause FFmpeg subprocess issues
    replacements = {
        "'": '',           # Single quotes
        '"': '',           # Double quotes
        '`': '',           # Backticks
        '\\': '_',         # Backslashes
        '/': '_',          # Forward slashes
        ':': '_',          # Colons
        ';': '_',          # Semicolons
        '|': '_',          # Pipes
        '&': 'and',        # Ampersands
        '%': 'percent',    # Percent signs
        '$': 'dollar',     # Dollar signs
        '#': 'hash',       # Hash/pound signs
        '@': 'at',         # At signs
        '!': '',           # Exclamation marks
        '?': '',           # Question marks
        '*': '',           # Asterisks
        '<': '',           # Less than
        '>': '',           # Greater than
        '(': '',           # Parentheses
        ')': '',
        '[': '',           # Brackets
        ']': '',
        '{': '',           # Braces
        '}': '',
        '=': '_equals_',   # Equals
        '+': '_plus_',     # Plus
        '~': '_tilde_',    # Tilde
        '^': '',           # Caret
    }
    
    # Apply replacements
    for old_char, new_char in replacements.items():
        text = text.replace(old_char, new_char)
    
    # Remove any remaining non-ASCII characters that might cause issues
    text = ''.join(char for char in text if ord(char) < 128)
    
    # Replace multiple spaces/underscores with single underscore
    text = re.sub(r'[\s_]+', '_', text)
    
    # Remove leading/trailing underscores and spaces
    text = text.strip('_').strip()
    
    # Ensure it's not empty
    if not text:
        text = "text_content"
    
    # Limit length
    if len(text) > max_length:
        text = text[:max_length].rstrip('_')
    
    return text


def get_cross_platform_subprocess_env():
    """
    Returns environment variables optimized for cross-platform subprocess execution.
    Helps ensure consistent behavior across Windows and Linux for FFmpeg operations.
    Enhanced to prevent common subprocess issues.
    """
    env = dict(os.environ)
    
    if sys.platform.startswith('win'):
        # Windows-specific optimizations
        env.update({
            'PYTHONIOENCODING': 'utf-8',
            'PYTHONLEGACYWINDOWSSTDIO': '1',  # Helps with stdout/stderr handling
        })
        # Ensure PATH includes FFmpeg if available
        ffmpeg_path = shutil.which('ffmpeg')
        if ffmpeg_path:
            ffmpeg_dir = os.path.dirname(ffmpeg_path)
            if ffmpeg_dir not in env.get('PATH', ''):
                env['PATH'] = ffmpeg_dir + os.pathsep + env.get('PATH', '')
    else:
        # Linux/Unix: Force UTF-8 locale to prevent encoding issues
        env.update({
            'LC_ALL': 'C.UTF-8',
            'LANG': 'C.UTF-8', 
            'LC_CTYPE': 'C.UTF-8',
            'PYTHONIOENCODING': 'utf-8'
        })
    
    return env

def merge_overlays_ffmpeg(base_video_path, overlay_files, output_path, middle_clip_timestamps=None):
    """
    Applica gli overlay video al base_clip e mixa l'audio dagli overlay con sound effects.
    Cross-platform compatible version with improved error handling.
    
    Args:
        base_video_path: Percorso del video base (con audio voce+musica già mixati).
        overlay_files:   Lista di tuple (overlay_path, start_time, duration) con overlay.
        output_path:     Percorso del video finale da scrivere.
        middle_clip_timestamps: Lista di tuple (start_time, end_time) per disabilitare overlay durante middle clips.
    """
    import subprocess, shlex, logging, sys, os

    # Cross-platform compatibility setup
    is_windows = sys.platform.startswith('win')
    
    # Validate input files before proceeding
    if not os.path.exists(base_video_path):
        raise FileNotFoundError(f"Base video file not found: {base_video_path}")
    
    # Validate overlay files (enhanced to handle None paths and invalid data)
    for i, (ov_path, start, dur) in enumerate(overlay_files):
        if not ov_path or not isinstance(ov_path, str):
            logging.warning(f"📋 Overlay #{i+1} has invalid path (None or not string): {ov_path}")
            continue
        if not os.path.exists(ov_path):
            logging.warning(f"📋 Overlay file #{i+1} not found: {ov_path}")
            continue
        if os.path.getsize(ov_path) == 0:
            logging.warning(f"📋 Overlay file #{i+1} is empty: {ov_path}")
            continue
        # Additional validation for timing parameters
        try:
            float(start), float(dur)
        except (ValueError, TypeError):
            logging.warning(f"📋 Overlay #{i+1} has invalid timing (start={start}, dur={dur})")
            continue

    # Filter out invalid overlay files (includes None, non-existent, empty, or invalid timing)
    valid_overlay_files = []
    for ov_path, start, dur in overlay_files:
        if not ov_path or not isinstance(ov_path, str):
            continue
        if not os.path.exists(ov_path) or os.path.getsize(ov_path) == 0:
            continue
        try:
            start, dur = float(start), float(dur)
        except (ValueError, TypeError):
            continue
        valid_overlay_files.append((ov_path, start, dur))
    
    if len(valid_overlay_files) != len(overlay_files):
        logging.warning(f"📋 Filtered {len(overlay_files) - len(valid_overlay_files)} invalid overlay files")
        overlay_files = valid_overlay_files
    
    if not overlay_files:
        logging.warning("❌ No valid overlay files found, copying base video")
        import shutil
        shutil.copy2(base_video_path, output_path)
        return output_path if os.path.exists(output_path) else None

    # Probe base for audio (voiceover + music must be in base)
    def _base_has_audio(path):
        try:
            probe_cmd = [
                "ffprobe", "-v", "error", "-select_streams", "a:0",
                "-show_entries", "stream=index", "-of", "csv=p=0", path
            ]
            res = subprocess.run(probe_cmd, capture_output=True, text=True, timeout=10)
            return res.returncode == 0 and bool(res.stdout.strip())
        except Exception:
            return False

    base_has_audio = _base_has_audio(base_video_path)
    if not base_has_audio:
        logging.warning("❌ Base video has no audio stream - output will be silent. Fix Assembly/Finalization so base has voiceover+music.")
    
    # 1) Prepara gli input: base + ciascun overlay
    # Use sanitized absolute paths to avoid path resolution issues on Linux
    cmd = ["ffmpeg", "-y", "-hwaccel", "none", "-i", sanitize_ffmpeg_path(base_video_path)]
    for ov_path, _, _ in overlay_files:
        cmd += ["-i", sanitize_ffmpeg_path(ov_path)]

    # 2) Detect which overlays have audio streams (optional)
    overlays_with_audio = []
    mix_overlay_audio = str(os.environ.get("VELOX_OVERLAY_AUDIO_MIX", "0")).strip() == "1"
    if mix_overlay_audio:
        for idx, (ov_path, start, dur) in enumerate(overlay_files, start=1):
            try:
                # Check if overlay has audio stream
                probe_cmd = ["ffprobe", "-v", "error", "-select_streams", "a", "-show_entries", "stream=codec_type", "-of", "default=noprint_wrappers=1:nokey=1", ov_path]
                probe_result = subprocess.run(probe_cmd, capture_output=True, text=True, check=True)
                if probe_result.stdout.strip():
                    overlays_with_audio.append((idx, start, dur))
                    logging.info(f"📋 Overlay #{idx} has audio track (sound effects): {os.path.basename(ov_path)}")
            except subprocess.CalledProcessError:
                pass  # No audio stream or probe failed
    
    # 3) Costruisci filter_complex per gli overlay video
    filter_parts = []
    # 3.a) shift dei singoli overlay
    for idx, (_, start, dur) in enumerate(overlay_files, start=1):
        filter_parts.append(f"[{idx}:v] setpts=PTS+{start:.6f}/TB [ovr{idx}]")
    
    # 3.b) chain degli overlay sul base
    last = "0:v"
    for idx, (_, start, dur) in enumerate(overlay_files, start=1):
        tag = f"tmp{idx}"
        
        # Costruisci condizione enable che esclude i middle clips
        base_enable = f"between(t,{start:.6f},{start+dur:.6f})"
        
        if middle_clip_timestamps:
            # Crea condizioni per disabilitare overlay durante middle clips
            disable_conditions = []
            for clip_start, clip_end in middle_clip_timestamps:
                disable_conditions.append(f"between(t,{clip_start:.6f},{clip_end:.6f})")
            
            if disable_conditions:
                # Overlay è abilitato quando la condizione base è vera E non durante nessun middle clip
                # FFmpeg: not(A+B+C) = (1-A)*(1-B)*(1-C)
                disable_expr = "+".join(disable_conditions)
                enable = f"({base_enable})*(1-({disable_expr}))"
                logging.info(f"📋 Overlay #{idx} enable condition: {enable}")
            else:
                enable = base_enable
        else:
            enable = base_enable
        
        filter_parts.append(f"[{last}][ovr{idx}] overlay=enable='{enable}' [{tag}]")
        last = tag

    # 4) Audio: always take base (voiceover + music) through filter to avoid wrong stream when overlays have audio
    if overlays_with_audio:
        logging.info(f"🎛️ Mixing audio from {len(overlays_with_audio)} overlays with base audio")
        
        # Prepare base audio
        audio_inputs = ["[0:a]"]
        
        # Add audio from overlays with proper timing
        for overlay_idx, start_time, duration in overlays_with_audio:
            # Delay and limit the overlay audio to match its video timing
            filter_parts.append(f"[{overlay_idx}:a] adelay={int(start_time * 1000)}:all=1,atrim=0:{duration},apad [aud{overlay_idx}]")
            audio_inputs.append(f"[aud{overlay_idx}]")
        
        # Mix all audio inputs with enhanced volume for sound effects
        if len(audio_inputs) > 1:
            # Create weights: base audio at full volume (1.0), sound effects at enhanced volume (0.8)
            # Increased sound effects volume from 0.3 to 0.8 for better audibility
            weights = "1.0 " + " ".join(["0.8"] * (len(audio_inputs) - 1))
            filter_parts.append(f"{''.join(audio_inputs)} amix=inputs={len(audio_inputs)}:duration=first:weights='{weights}':normalize=0 [mixed_audio]")
            audio_map = "[mixed_audio]"
        else:
            audio_map = "[0:a]"
    else:
        # Base audio (voiceover + music) through filter so overlays' audio is never selected.
        # If base has no audio, do not add [0:a] (would make ffmpeg fail); map optional so output has no audio.
        if base_has_audio:
            filter_parts.append("[0:a] volume=1.0 [aout]")
            audio_map = "[aout]"
        else:
            audio_map = "0:a?"
    
    # Debug: Log audio mixing configuration
    if overlays_with_audio:
        logging.info(f"🔊 Audio mixing enabled - Base: 1.0, Sound effects: 0.8 volume, Total inputs: {len(audio_inputs)}")
        logging.info(f"🎵 Sound effects timing: {[(idx, start, dur) for idx, start, dur in overlays_with_audio]}")
    else:
        logging.info("📋 No overlay audio detected - using base audio only")
    
    filter_complex = ";".join(filter_parts)

    # Detect original sample rate to preserve audio pitch
    try:
        import subprocess
        import json
        probe_cmd = [
            'ffprobe', '-v', 'quiet', '-print_format', 'json',
            '-show_streams', '-select_streams', 'a:0', base_video_path
        ]
        result = subprocess.run(probe_cmd, capture_output=True, text=True, check=True)
        probe_data = json.loads(result.stdout)
        original_sample_rate = probe_data['streams'][0]['sample_rate']
        logging.info(f"📋 Original sample rate detected: {original_sample_rate} Hz - preserving to avoid pitch alterations")
    except Exception as e:
        logging.warning(f"❌ Could not detect original sample rate: {e}, using 48000 Hz fallback")
        original_sample_rate = '48000'

    # 4.5) Calculate maximum duration needed (base video + longest overlay end time)
    base_duration = 0.0
    max_duration_needed = 0.0
    stream_loop_count = -1  # -1 means infinite loop, 0 means no loop
    try:
        # Get base video duration
        probe_cmd = ["ffprobe", "-v", "error", "-show_entries", "format=duration", "-of", "default=noprint_wrappers=1:nokey=1", base_video_path]
        probe_result = subprocess.run(probe_cmd, capture_output=True, text=True, timeout=10)
        if probe_result.returncode == 0 and probe_result.stdout.strip():
            base_duration = float(probe_result.stdout.strip())
            max_duration_needed = base_duration
    except Exception as e:
        logging.warning(f"❌ Could not detect base video duration: {e}")
    
    # Check overlay end times
    for _, start, dur in overlay_files:
        overlay_end = start + dur
        if overlay_end > max_duration_needed:
            max_duration_needed = overlay_end
    
    # If overlays extend beyond base video, use stream_loop to extend base video
    if max_duration_needed > base_duration and base_duration > 0:
        loops_needed = int(max_duration_needed / base_duration) + 2  # Add 2 extra loops for safety
        stream_loop_count = loops_needed
        logging.info(f"📋 Extending base video from {base_duration:.2f}s to {max_duration_needed:.2f}s using stream_loop={loops_needed}")

    # 5) Mappa il video compositato e l'audio mixato con percorso sanitizzato
    output_duration_param = []
    if max_duration_needed > base_duration and base_duration > 0:
        output_duration_param = ["-t", f"{max_duration_needed:.6f}"]  # Limit output to maximum duration needed
    
    # Add stream_loop parameter if needed (before -i)
    if stream_loop_count > 0:
        # Find the position of base_video_path in cmd and insert -stream_loop before -i
        base_video_index = cmd.index(sanitize_ffmpeg_path(base_video_path))
        cmd.insert(base_video_index - 1, str(stream_loop_count))
        cmd.insert(base_video_index - 1, "-stream_loop")
    
    if base_has_audio:
        cmd += [
            "-filter_complex", filter_complex,
            "-map", f"[{last}]", "-map", audio_map,
            "-c:v", "libx264", "-preset", "fast",
            "-c:a", "aac", "-b:a", "256k", "-ar", original_sample_rate,
            "-avoid_negative_ts", "make_zero",
        ] + output_duration_param + [sanitize_ffmpeg_path(output_path)]
    else:
        cmd += [
            "-filter_complex", filter_complex,
            "-map", f"[{last}]", "-an",
            "-c:v", "libx264", "-preset", "fast",
            "-avoid_negative_ts", "make_zero",
        ] + output_duration_param + [sanitize_ffmpeg_path(output_path)]

    # Debug: Log the command being executed
    logging.info(f"⚠️ FFmpeg command: {' '.join(cmd[:5])}... (total {len(cmd)} args)")
    logging.debug(f"⚙️🔍 Full FFmpeg command: {' '.join(cmd)}")

    # 4) Esegui con gestione cross-platform migliorata
    try:
        # First attempt: Use platform-appropriate encoding
        if is_windows:
            # Windows: Use UTF-8 with error replacement
            result = subprocess.run(
                cmd, 
                check=True, 
                capture_output=True, 
                text=True, 
                encoding='utf-8', 
                errors='replace',
                creationflags=subprocess.CREATE_NO_WINDOW if hasattr(subprocess, 'CREATE_NO_WINDOW') else 0
            )
        else:
            # Linux/Unix: Use cross-platform environment with UTF-8 locale
            result = subprocess.run(
                cmd, 
                check=True, 
                capture_output=True, 
                text=True, 
                env=get_cross_platform_subprocess_env(),  # Use optimized environment
                errors='replace'
            )
        logging.info(f"✅ Video finale generato con successo: {output_path}")
        if os.path.exists(output_path) and os.path.getsize(output_path) > 0:
            return output_path
        return None

    except subprocess.CalledProcessError as e:
        # Enhanced error handling with platform-specific details
        error_msg = "Unknown FFmpeg error"
        
        if e.stderr:
            if isinstance(e.stderr, bytes):
                try:
                    error_msg = e.stderr.decode('utf-8', errors='replace')
                except Exception:
                    error_msg = f"FFmpeg error (binary stderr): {len(e.stderr)} bytes"
            else:
                error_msg = str(e.stderr)
        
        platform_info = f"Platform: {sys.platform}, Windows: {is_windows}"
        logging.error(f"❌ Errore FFmpeg compositing ({platform_info}): {error_msg}")
        
        # Try fallback execution with different parameters
        if not is_windows:
            logging.info("🔄 Tentativo fallback su Linux senza locale specifico...")
            try:
                result = subprocess.run(
                    cmd, 
                    check=True, 
                    capture_output=True, 
                    text=False  # Binary mode fallback
                )
                logging.info(f"✅ Video finale generato con successo (fallback binario): {output_path}")
                return output_path if (os.path.exists(output_path) and os.path.getsize(output_path) > 0) else None
            except subprocess.CalledProcessError as e2:
                error_msg2 = f"Fallback binary error: {e2.returncode}"
                if e2.stderr:
                    try:
                        error_msg2 = e2.stderr.decode('utf-8', errors='replace')
                    except Exception:
                        error_msg2 = f"Binary fallback failed: {len(e2.stderr)} bytes stderr"
                logging.error(f"❌ Errore anche nel fallback: {error_msg2}")
        
        raise Exception(f"Errore durante il compositing con FFmpeg ({platform_info}): {error_msg}")
        
    except UnicodeDecodeError as e:
        logging.error(f"❌ Errore di encoding Unicode in FFmpeg output: {e}")
        
        # Retry with binary output mode if Unicode fails
        try:
            logging.info("🔄 Retry con modalità binaria...")
            result = subprocess.run(
                cmd, 
                check=True, 
                capture_output=True, 
                text=False  # Binary mode
            )
            logging.info(f"✅ Video finale generato con successo: {output_path} (modalità binaria)")
            return output_path if (os.path.exists(output_path) and os.path.getsize(output_path) > 0) else None
        except subprocess.CalledProcessError as e:
            error_msg = "FFmpeg error (binary output)"
            if e.stderr:
                try:
                    error_msg = e.stderr.decode('utf-8', errors='replace')
                except Exception:
                    error_msg = f"FFmpeg error (binary): {len(e.stderr)} bytes of stderr"
            logging.error(f"❌ Errore FFmpeg compositing (retry binario): {error_msg}")
            raise Exception(f"Errore durante il compositing con FFmpeg (retry binario): {error_msg}")
    
    except Exception as e:
        logging.error(f"❌ Errore inaspettato durante l'esecuzione FFmpeg: {e}")
        raise Exception(f"Errore inaspettato durante il compositing con FFmpeg: {e}")
    
    



def has_video_stream(file_path):
    """Check if file has a video stream using ffprobe with retry logic"""
    try:
        # Enhanced validation for None or invalid paths
        if not file_path or not isinstance(file_path, str):
            logging.warning(f"Invalid file path (None or not string): {file_path}")
            return False

        if not os.path.exists(file_path):
            logging.warning(f"File non trovato: {file_path}")
            return False

        file_size = os.path.getsize(file_path)
        if file_size == 0:
            logging.warning(f"File vuoto: {file_path}")
            return False
            
        max_retries = 3
        for attempt in range(max_retries):
            try:
                # Check for video stream
                probe_cmd = ["ffprobe", "-v", "error", "-select_streams", "v:0", 
                           "-show_entries", "stream=codec_type", 
                           "-of", "default=noprint_wrappers=1:nokey=1", file_path]
                probe_result = subprocess.run(probe_cmd, capture_output=True, text=True, 
                                            check=True, timeout=5)
                
                if probe_result.stdout.strip() == "video":
                    logging.info(f"✅ Stream video trovato in {file_path}")
                    return True
                
                # Check all streams for debugging
                debug_cmd = ["ffprobe", "-v", "error", "-show_entries", "stream=codec_type", 
                           "-of", "csv=p=0", file_path]
                debug_result = subprocess.run(debug_cmd, capture_output=True, text=True, timeout=5)
                
                if debug_result.returncode == 0:
                    streams = [s.strip() for s in debug_result.stdout.split('\n') if s.strip()]
                    logging.warning(f"⚠️ Nessuno stream video in {file_path}. Streams: {streams}")
                
                if attempt < max_retries - 1:
                    time.sleep(0.2)
                    
            except subprocess.TimeoutExpired:
                logging.warning(f"⏰ Timeout ffprobe (tentativo {attempt + 1})")
            except subprocess.CalledProcessError as e:
                logging.warning(f"❌ ffprobe fallito: {e.stderr}")
            except Exception as e:
                logging.warning(f"❌ Errore controllo stream: {e}")
        
        return False
    except Exception as e:
        logging.warning(f"❌ Errore generico controllo stream: {e}")
        return False

def _moviepy_overlay_fallback(base_video_path, overlay_files, output_path):
    """
    Fallback function to compose video with overlays using MoviePy when FFmpeg fails.
    
    Args:
        base_video_path (str): Path to the base video
        overlay_files (list): List of overlay file tuples (path, start_time, duration)
        output_path (str): Path for the output video
    
    Returns:
        bool: True if successful, False otherwise
    """
    try:
        from moviepy import VideoFileClip, CompositeVideoClip
        import gc
        
        logging.info(f"🎬 Avvio fallback MoviePy con {len(overlay_files)} overlay")
        
        # Load base video
        base_clip = VideoFileClip(base_video_path)
        clips = [base_clip]
        
        # Process overlays
        overlay_clips = []
        for i, (overlay_path, start_time, duration) in enumerate(overlay_files):
            try:
                if not overlay_path or not os.path.exists(overlay_path):
                    logging.warning(f"⚠️ Overlay {i+1} non trovato: {overlay_path}")
                    continue
                
                # Check if overlay has video stream
                if not has_video_stream(overlay_path):
                    logging.warning(f"⚠️ Overlay {i+1} non ha stream video: {overlay_path}")
                    continue
                
                # Load overlay clip
                overlay_clip = VideoFileClip(overlay_path)
                
                # Set timing
                overlay_clip = overlay_clip.set_start(start_time)
                
                # Add to clips list
                overlay_clips.append(overlay_clip)
                clips.append(overlay_clip)
                
                logging.info(f"✅ Overlay {i+1} caricato: start={start_time}s")
                
            except Exception as e:
                logging.error(f"❌ Errore caricamento overlay {i+1}: {e}")
                continue
        
        if len(clips) == 1:
            logging.warning("⚠️ Nessun overlay valido trovato, copio solo il video base")
            # Just copy the base video
            base_clip.write_videofile(
                output_path,
                codec='libx264',
                audio_codec='aac',
                verbose=False,
                logger=None
            )
        else:
            # Compose final video
            logging.info(f"🎬 Compositing {len(clips)} clip totali")
            final_clip = CompositeVideoClip(clips)
            
            # Write final video
            final_clip.write_videofile(
                output_path,
                codec='libx264',
                audio_codec='aac',
                verbose=False,
                logger=None
            )
            
            # Cleanup composite clip
            final_clip.close()
            del final_clip
        
        # Cleanup all clips
        base_clip.close()
        for overlay_clip in overlay_clips:
            overlay_clip.close()
        
        del base_clip, overlay_clips, clips
        gc.collect()
        
        logging.info(f"✅ Fallback MoviePy completato con successo: {output_path}")
        return True
        
    except Exception as e:
        logging.error(f"❌ Errore nel fallback MoviePy: {e}")
        
        # Cleanup on error
        try:
            if 'base_clip' in locals():
                base_clip.close()
            if 'overlay_clips' in locals():
                for clip in overlay_clips:
                    clip.close()
            if 'final_clip' in locals():
                final_clip.close()
            gc.collect()
        except:
            pass
        
        return False


def format_timestamp(seconds):
    """Format seconds to HH:MM:SS.mmm format for FFmpeg."""
    hours = int(seconds // 3600)
    minutes = int((seconds % 3600) // 60)
    secs = seconds % 60
    return f"{hours:02d}:{minutes:02d}:{secs:06.3f}"


def trim_video_reencode(
    input_path: str,
    output_path: str,
    start_time: float = 0.0,
    duration: Optional[float] = None,
    base_w: Optional[int] = None,
    base_h: Optional[int] = None,
    base_fps: Optional[float] = None,
    status_callback: Optional[Callable[[str], None]] = None,
) -> bool:
    """Trim video with re-encode to avoid keyframe cut issues and normalize specs."""
    try:
        if not input_path or not os.path.exists(input_path):
            if status_callback:
                status_callback(f"❌ trim_video_reencode: input non trovato: {input_path}")
            return False

        vf_parts = []
        if base_w and base_h:
            vf_parts.append(
                f"scale={int(base_w)}:{int(base_h)}:force_original_aspect_ratio=decrease,"
                f"pad={int(base_w)}:{int(base_h)}:(ow-iw)/2:(oh-ih)/2"
            )
        vf = ",".join(vf_parts) if vf_parts else None

        cmd = [
            "ffmpeg", "-y",
            "-ss", f"{max(0.0, float(start_time)):.3f}",
            "-i", input_path,
        ]
        if duration is not None and duration > 0:
            cmd.extend(["-t", f"{float(duration):.3f}"])

        if vf:
            cmd.extend(["-vf", vf])
        if base_fps:
            cmd.extend(["-r", f"{float(base_fps):.3f}"])

        cmd.extend([
            "-map", "0:v:0",
            "-map", "0:a?",
            "-c:v", "libx264",
            "-preset", "veryfast",
            "-crf", "20",
            "-pix_fmt", "yuv420p",
            "-c:a", "aac",
            "-b:a", "192k",
            "-ar", "44100",
            "-ac", "2",
            "-shortest",
            output_path,
        ])

        result = run_ffmpeg_command(cmd, "FFmpeg trim re-encode", status_callback, progress=False, timeout=600)
        if result.returncode == 0 and os.path.exists(output_path) and os.path.getsize(output_path) > 0:
            return True
        if status_callback:
            status_callback(f"❌ trim_video_reencode fallito: {result.stderr if hasattr(result, 'stderr') else ''}")
        return False
    except Exception as e:
        if status_callback:
            status_callback(f"❌ trim_video_reencode errore: {e}")
        return False


def concatenate_videos_ffmpeg(
    video_paths: List[str],
    output_path: str,
    status_callback: Optional[Callable[[str], None]] = None,
    speed_optimize: bool = True,
) -> bool:
    """Concatenate video files using FFmpeg's concat demuxer (no re-encoding).

    speed_optimize=True uses the leanest set of flags for maximum speed
    (drops genpts/avoid_negative_ts/faststart and disables progress parsing).
    """
    try:
        if not video_paths:
            logger.error("No video paths provided for concatenation")
            return False

        if status_callback:
            status_callback(f"Concatenating {len(video_paths)} video files with FFmpeg...")

        # Create temporary concat list file
        temp_dir = os.path.dirname(output_path)
        concat_list_path = os.path.join(temp_dir, f"concat_list_{os.urandom(4).hex()}.txt")

        try:
            # Create concat list file
            with open(concat_list_path, 'w', encoding='utf-8') as f:
                for video_path in video_paths:
                    if os.path.exists(video_path):
                        # Escape single quotes in path for FFmpeg
                        escaped_path = video_path.replace("'", "\\'")
                        f.write(f"file '{escaped_path}'\n")

            # Build FFmpeg command for concatenation
            base_cmd = [
                'ffmpeg', '-y',
                '-loglevel', 'error',   # keep stderr minimal for speed
                '-nostdin',             # avoid waiting on stdin
                '-f', 'concat', '-safe', '0',
                '-i', concat_list_path,
                '-c', 'copy',
            ]

            # Extra safety flags only when not in speed mode
            if not speed_optimize:
                base_cmd.extend(['-fflags', '+genpts', '-avoid_negative_ts', 'make_zero', '-movflags', '+faststart'])

            cmd = base_cmd + [output_path]

            # Run FFmpeg command
            if status_callback:
                status_callback("Running FFmpeg concatenation...")

            # Disable progress streaming in speed mode to reduce Python-side overhead
            result = run_ffmpeg_command(
                cmd,
                "FFmpeg video concatenation",
                status_callback,
                progress=False if speed_optimize else True,
                timeout=300,
            )

            # Verify output
            if result.returncode == 0 and os.path.exists(output_path) and os.path.getsize(output_path) > 0:
                if status_callback:
                    status_callback(f"✅ FFmpeg concatenation completed: {os.path.basename(output_path)}")
                return True
            else:
                if status_callback:
                    status_callback(f"❌ FFmpeg concatenation failed: {result.stderr}")
                return False

        finally:
            # Clean up concat list file
            try:
                if os.path.exists(concat_list_path):
                    os.remove(concat_list_path)
            except OSError:
                pass

    except Exception as e:
        error_msg = f"Error concatenating videos with FFmpeg: {e}"
        logger.error(error_msg)
        if status_callback:
            status_callback(f"❌ {error_msg}")
        return False


def concatenate_videos_ffmpeg_safe(
    video_paths: List[str],
    output_path: str,
    base_w: int,
    base_h: int,
    base_fps: float,
    status_callback: Optional[Callable[[str], None]] = None
) -> bool:
    """Concatenate videos by re-encoding to uniform params (robust).

    - Scales/pads each input to `base_w`x`base_h` and sets fps to `base_fps`.
    - For many inputs (>5), uses concat demuxer with pre-processing to avoid Windows cmd line limits.
    - For few inputs, uses filter_complex concat to avoid non‑monotonic DTS and frozen video issues
      that occur with `-c copy` when inputs differ.
    """
    try:
        if not video_paths:
            logger.error("No video paths provided for safe concatenation")
            return False

        if status_callback:
            status_callback(f"Concatenating {len(video_paths)} videos (safe re-encode)...")

        # Filter out missing files
        valid_paths = [vp for vp in video_paths if os.path.exists(vp)]
        if not valid_paths:
            if status_callback:
                status_callback("No valid video files found for safe concatenation")
            return False

        # Windows command line has ~8192 character limit. For many inputs (>5), the filter_complex
        # becomes too long and complex. Use a better heuristic: if > 3 inputs for filter_complex
        if len(valid_paths) > 3:
            return _concatenate_videos_safe_many(valid_paths, output_path, base_w, base_h, base_fps, status_callback)
        else:
            return _concatenate_videos_safe_few(valid_paths, output_path, base_w, base_h, base_fps, status_callback)

    except Exception as e:
        logger.error(f"Error in safe concatenation: {e}")
        if status_callback:
            status_callback(f"❌ Error in safe concatenation: {e}")
        # Final fallback attempt
        try:
            success_fallback = concatenate_videos_ffmpeg(valid_paths, output_path, status_callback=status_callback)
            if success_fallback:
                return True
        except Exception:
            pass
        return False


def _concatenate_videos_safe_few(
    video_paths: List[str],
    output_path: str,
    base_w: int,
    base_h: int,
    base_fps: float,
    status_callback: Optional[Callable[[str], None]] = None
) -> bool:
    """Handle safe concatenation for few inputs (≤5) using filter_complex."""
    try:
        # Build inputs and filter graph
        inputs: List[str] = []
        filter_parts: List[str] = []
        v_streams: List[str] = []
        a_streams: List[str] = []

        def _probe_has_audio(path: str) -> bool:
            try:
                cmd = ['ffprobe', '-v', 'error', '-select_streams', 'a:0', '-show_entries', 'stream=codec_type', '-of', 'csv=p=0', path]
                res = subprocess.run(cmd, capture_output=True, text=True, timeout=10)
                return (res.returncode == 0) and ('audio' in (res.stdout or '').strip().lower())
            except Exception:
                return False

        def _probe_duration(path: str) -> float:
            try:
                cmd = ['ffprobe', '-v', 'error', '-show_entries', 'format=duration', '-of', 'default=noprint_wrappers=1:nokey=1', path]
                res = subprocess.run(cmd, capture_output=True, text=True, timeout=10)
                if res.returncode == 0 and res.stdout.strip():
                    return float(res.stdout.strip())
            except Exception:
                pass
            return 0.0

        inp_idx = 0
        for idx, vp in enumerate(video_paths):
            inputs += ['-i', vp]
            # Video chain: scale/pad, set fps, reset PTS
            filter_parts.append(
                f"[{inp_idx}:v]scale={base_w}:{base_h}:force_original_aspect_ratio=decrease,"
                f"pad={base_w}:{base_h}:(ow-iw)/2:(oh-ih)/2,setsar=1,fps={int(base_fps)},setpts=PTS-STARTPTS[v{inp_idx}]"
            )
            v_streams.append(f"[v{inp_idx}]")

            # Audio: use input audio if present, otherwise generate silence matching duration
            has_a = _probe_has_audio(vp)
            dur = _probe_duration(vp)
            if has_a:
                # Trim audio to avoid spilling beyond video segment
                filter_parts.append(
                    f"[{inp_idx}:a]aresample=async=1,atrim=0:{dur:.6f},asetpts=PTS-STARTPTS[a{inp_idx}]"
                )
            else:
                sr = 48000
                filter_parts.append(
                    f"anullsrc=r={sr}:cl=stereo,atrim=0:{dur:.6f},asetpts=PTS-STARTPTS[a{inp_idx}]"
                )
            a_streams.append(f"[a{inp_idx}]")
            inp_idx += 1

        if not v_streams:
            if status_callback:
                status_callback("No valid video streams found for safe concatenation")
            return False

        # Ensure we have equal number of video and audio streams for concat
        if len(v_streams) != len(a_streams):
            if status_callback:
                status_callback(f"Stream count mismatch: {len(v_streams)} video, {len(a_streams)} audio")
            return False

        # Build concat filter with proper stream ordering: [v0][a0][v1][a1]...
        concat_streams = []
        for i in range(len(v_streams)):
            concat_streams.extend([v_streams[i], a_streams[i]])
        
        filter_parts.append(f"{''.join(concat_streams)}concat=n={len(v_streams)}:v=1:a=1[outv][outa]")
        map_args = ['-map', '[outv]', '-map', '[outa]']

        cmd = ['ffmpeg', '-y', '-hide_banner'] + inputs + [
            '-filter_complex', ';'.join(filter_parts),
        ] + map_args + [
            '-c:v', 'libx264', '-preset', 'medium', '-crf', '22', '-pix_fmt', 'yuv420p', '-r', str(int(base_fps)),
            '-c:a', 'aac', '-b:a', '192k',
            output_path
        ]

        result = run_ffmpeg_command(cmd, 'FFmpeg safe concatenation', status_callback, progress=True, timeout=600)
        if result.returncode == 0 and os.path.exists(output_path) and os.path.getsize(output_path) > 0:
            if status_callback:
                status_callback(f"✅ Safe concatenation completed: {os.path.basename(output_path)}")
            return True
        else:
            # Fallback: try concat demuxer with stream copy
            if status_callback:
                status_callback(f"❌ Safe concatenation failed, provo fallback demuxer: {result.stderr if hasattr(result,'stderr') else ''}")
            try:
                success_fallback = concatenate_videos_ffmpeg(video_paths, output_path, status_callback=status_callback)
                if success_fallback:
                    return True
            except Exception as fb_e:
                if status_callback:
                    status_callback(f"Fallback demuxer error: {fb_e}")
            return False

    except Exception as e:
        logger.error(f"Error in safe concatenation (few inputs): {e}")
        if status_callback:
            status_callback(f"❌ Error in safe concatenation: {e}")
        # Fallback attempt even on exceptions
        try:
            success_fallback = concatenate_videos_ffmpeg(video_paths, output_path, status_callback=status_callback)
            if success_fallback:
                return True
        except Exception:
            pass
        return False


def _concatenate_videos_safe_many(
    video_paths: List[str],
    output_path: str,
    base_w: int,
    base_h: int,
    base_fps: float,
    status_callback: Optional[Callable[[str], None]] = None
) -> bool:
    """Handle safe concatenation for many inputs (>5) using pre-processing + concat demuxer."""
    try:
        temp_dir = os.path.dirname(output_path)
        temp_files = []

        # Step 1: Pre-process each video to uniform format
        preprocessed_paths = []
        for i, video_path in enumerate(video_paths):
            if not os.path.exists(video_path):
                if status_callback:
                    status_callback(f"⚠️ Video file not found: {video_path}, skipping")
                continue

            temp_out = os.path.join(temp_dir, f"preproc_{i}_{os.urandom(4).hex()}.mp4")
            temp_files.append(temp_out)

            # Pre-process: scale, pad, set fps, ensure consistent codec
            cmd_preproc = [
                'ffmpeg', '-y', '-i', video_path,
                '-vf', f'scale={base_w}:{base_h}:force_original_aspect_ratio=decrease,pad={base_w}:{base_h}:(ow-iw)/2:(oh-ih)/2,setsar=1',
                '-r', str(int(base_fps)),
                '-c:v', 'libx264', '-preset', 'ultrafast', '-crf', '28', '-pix_fmt', 'yuv420p',
                '-c:a', 'aac', '-b:a', '192k',
                '-avoid_negative_ts', 'make_zero',
                temp_out
            ]

            try:
                result = run_ffmpeg_command(cmd_preproc, f'Pre-processing video {i+1}/{len(video_paths)}', status_callback, progress=False, timeout=300)
                if result.returncode == 0 and os.path.exists(temp_out) and os.path.getsize(temp_out) > 0:
                    preprocessed_paths.append(temp_out)
                    if status_callback:
                        status_callback(f"✅ Pre-processed {i+1}/{len(video_paths)}: {os.path.basename(temp_out)}")
                else:
                    if status_callback:
                        status_callback(f"❌ Failed to pre-process {os.path.basename(video_path)} (return code: {result.returncode}), skipping")
            except (subprocess.TimeoutExpired, Exception) as e:
                # Handle timeouts and other exceptions gracefully - don't stop the entire process
                if status_callback:
                    status_callback(f"❌ Error pre-processing {os.path.basename(video_path)}: {str(e)}, skipping")
                logger.warning(f"Failed to pre-process video {video_path}: {e}")
                # Remove the temp file if it was created but failed
                if os.path.exists(temp_out):
                    try:
                        os.remove(temp_out)
                    except Exception:
                        pass

        if not preprocessed_paths:
            if status_callback:
                status_callback("No videos were successfully pre-processed")
            return False

        # Step 2: Use concat demuxer on pre-processed files
        if status_callback:
            status_callback(f"Using concat demuxer with {len(preprocessed_paths)} pre-processed videos...")

        success = concatenate_videos_ffmpeg(preprocessed_paths, output_path, status_callback)

        if success and status_callback:
            status_callback(f"✅ Safe concatenation (many inputs) completed: {os.path.basename(output_path)}")

        # Cleanup temporary files
        for temp_file in temp_files:
            try:
                if os.path.exists(temp_file):
                    os.remove(temp_file)
            except Exception:
                pass

        return success

    except Exception as e:
        logger.error(f"Error in safe concatenation (many inputs): {e}")
        if status_callback:
            status_callback(f"❌ Error in safe concatenation (many inputs): {e}")

        # Cleanup on error
        for temp_file in temp_files:
            try:
                if os.path.exists(temp_file):
                    os.remove(temp_file)
            except Exception:
                pass

        return False


def concatenate_videos_with_transitions_ffmpeg(
    video_paths: List[str],
    output_path: str,
    transition_duration: float = 1.0,
    transition_type: Optional[Union[str, List[str]]] = None,
    status_callback: Optional[Callable[[str], None]] = None,
    timeout: int = 600,
    target_fps: Optional[float] = None,
    temp_dir: Optional[str] = None,
    audio_sample_rate: int = 48000,
) -> bool:
    """Fallback wrapper that disables FFmpeg transitions and performs standard concatenation."""
    if status_callback:
        status_callback("FFmpeg transitions disabilitate: utilizzo concatenazione standard.")
    return concatenate_videos_ffmpeg(
        video_paths=video_paths,
        output_path=output_path,
        status_callback=status_callback,
    )


def concatenate_stock_videos_with_transitions(*args: Any, **kwargs: Any) -> bool:
    """Backward compatible alias for previous API name."""
    return concatenate_videos_with_transitions_ffmpeg(*args, **kwargs)


def _safe_float(value: Any, default: float = 0.0) -> float:
    """Safely convert value to float."""
    try:
        return float(value)
    except (ValueError, TypeError):
        return default
