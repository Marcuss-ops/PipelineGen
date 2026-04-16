"""FFmpeg utilities for high-performance video and audio processing.

This module provides FFmpeg-based alternatives to MoviePy operations
for better performance and lower memory usage.
"""

import os
import logging
import subprocess
import tempfile
import json
from typing import Callable, List, Optional, Dict, Any, Tuple
from pathlib import Path
import uuid

logger = logging.getLogger(__name__)

# Import cache manager
try:
    from modules.utils.cache_manager import cache_manager
except ImportError:
    try:
        from modules.utils.cache_manager import cache_manager
    except ImportError:
        cache_manager = None


class FFmpegProcessor:
    """High-performance video and audio processing using FFmpeg."""
    
    def __init__(self):
        self.ffmpeg_path = self._find_ffmpeg()
        if not self.ffmpeg_path:
            raise RuntimeError("FFmpeg not found in PATH")
        logger.info(f"FFmpeg found at: {self.ffmpeg_path}")
    
    def _find_ffmpeg(self) -> Optional[str]:
        """Find FFmpeg executable in PATH."""
        try:
            result = subprocess.run(['ffmpeg', '-version'], 
                                  capture_output=True, text=True, check=True)
            return 'ffmpeg'
        except (subprocess.CalledProcessError, FileNotFoundError):
            return None
    
    def get_video_info(self, video_path: str) -> Dict[str, Any]:
        """Get video information using FFmpeg cache completa."""
        # Usa la cache FFmpeg completa se disponibile
        if cache_manager:
            try:
                metadata = cache_manager.get_video_metadata_complete(video_path)
                if metadata:
                    video = metadata.get('video', {})
                    return {
                        'duration': metadata.get('duration', 0.0),
                        'fps': video.get('fps', 0.0) if video else 0.0,
                        'width': video.get('width', 0) if video else 0,
                        'height': video.get('height', 0) if video else 0,
                        'has_audio': metadata.get('audio') is not None,
                        'size': metadata.get('size', 0)
                    }
            except Exception as e:
                logger.warning(f"Cache FFmpeg fallita per {video_path}: {e}")
        
        # Fallback to direct ffprobe
        try:
            cmd = [
                'ffprobe', '-v', 'quiet', '-print_format', 'json',
                '-show_format', '-show_streams', video_path
            ]
            result = subprocess.run(cmd, capture_output=True, text=True, check=True)
            data = json.loads(result.stdout)
            
            video_stream = next((s for s in data['streams'] if s['codec_type'] == 'video'), None)
            audio_stream = next((s for s in data['streams'] if s['codec_type'] == 'audio'), None)
            
            return {
                'duration': float(data['format']['duration']),
                'fps': eval(video_stream['r_frame_rate']) if video_stream else 0,
                'width': video_stream['width'] if video_stream else 0,
                'height': video_stream['height'] if video_stream else 0,
                'has_audio': audio_stream is not None,
                'size': int(data['format']['size'])
            }
        except Exception as e:
            logger.error(f"Error getting video info: {e}")
            return {}
    
    def concatenate_videos(self, video_paths: List[str], output_path: str, 
                          add_transitions: bool = False) -> bool:
        """Concatenate multiple videos using FFmpeg."""
        try:
            if not video_paths:
                return False
            
            if len(video_paths) == 1:
                # Just copy the single file
                cmd = ['ffmpeg', '-y', '-i', video_paths[0], '-c', 'copy', output_path]
                subprocess.run(cmd, check=True, capture_output=True, timeout=300)
                return True
            
            # Create temporary file list for concatenation
            with tempfile.NamedTemporaryFile(mode='w', suffix='.txt', delete=False) as f:
                for video_path in video_paths:
                    # Usa path assoluti e escape per caratteri speciali
                    abs_path = os.path.abspath(video_path).replace('\\', '/')
                    f.write(f"file '{abs_path}'\n")
                list_file = f.name
            
            try:
                # Prima prova con -c copy per velocità massima
                cmd_copy = [
                    'ffmpeg', '-y', '-f', 'concat', '-safe', '0',
                    '-i', list_file, '-c', 'copy', output_path
                ]
                
                try:
                    result = subprocess.run(cmd_copy, check=True, capture_output=True, text=True, timeout=300)
                    # Verifica che il file di output sia stato creato correttamente
                    if os.path.exists(output_path) and os.path.getsize(output_path) > 1024:
                        logger.info(f"Concatenazione FFmpeg con -c copy completata: {output_path}")
                        return True
                    else:
                        logger.warning("File output vuoto o non creato con -c copy, provo con re-encoding")
                        raise subprocess.CalledProcessError(1, cmd_copy, "Output file empty or not created")
                except (subprocess.CalledProcessError, subprocess.TimeoutExpired) as e:
                    logger.warning(f"Concatenazione con -c copy fallita: {e}, provo con re-encoding")
                    # Se -c copy fallisce, prova con re-encoding
                    cmd_reencode = [
                        'ffmpeg', '-y', '-f', 'concat', '-safe', '0',
                        '-i', list_file, '-c:v', 'libx264', '-preset', 'medium', '-crf', '23',
                        '-c:a', 'aac', '-b:a', '192k', '-pix_fmt', 'yuv420p', output_path
                    ]
                    
                    result = subprocess.run(cmd_reencode, check=True, capture_output=True, text=True, timeout=600)
                    
                    # Verifica che il file di output sia stato creato correttamente
                    if os.path.exists(output_path) and os.path.getsize(output_path) > 1024:
                        logger.info(f"Concatenazione FFmpeg con re-encoding completata: {output_path}")
                        return True
                    else:
                        logger.error("File output vuoto o non creato anche con re-encoding")
                        return False
                        
            finally:
                if os.path.exists(list_file):
                    os.unlink(list_file)
                
        except Exception as e:
            logger.error(f"Error concatenating videos: {e}")
            return False
    
    def resize_video(self, input_path: str, output_path: str, 
                    width: int = 1920, height: int = 1080) -> bool:
        """Resize video using FFmpeg hardware acceleration when available."""
        try:
            # Try with hardware acceleration first with faster preset
            cmd_nvenc = [
                'ffmpeg', '-y', '-i', input_path,
                '-vf', f'scale={width}:{height}',
                '-c:v', 'h264_nvenc', '-preset', 'p1', '-tune', 'hq',
                '-c:a', 'copy', output_path
            ]
            
            try:
                subprocess.run(cmd_nvenc, check=True, capture_output=True)
                return True
            except subprocess.CalledProcessError:
                # Fallback to software encoding with ultrafast preset
                cmd_cpu = [
                    'ffmpeg', '-y', '-i', input_path,
                    '-vf', f'scale={width}:{height}',
                    '-c:v', 'libx264', '-preset', 'ultrafast', '-crf', '28',
                    '-c:a', 'copy', output_path
                ]
                subprocess.run(cmd_cpu, check=True, capture_output=True)
                return True
                
        except Exception as e:
            logger.error(f"Error resizing video: {e}")
            return False
    
    def extract_audio(self, video_path: str, output_path: str) -> bool:
        """Extract audio from video using FFmpeg."""
        try:
            cmd = [
                'ffmpeg', '-y', '-i', video_path,
                '-vn', '-acodec', 'copy', output_path
            ]
            subprocess.run(cmd, check=True, capture_output=True)
            return True
        except Exception as e:
            logger.error(f"Error extracting audio: {e}")
            return False
    
    def add_audio_to_video(self, video_path: str, audio_path: str, 
                          output_path: str, audio_volume: float = 1.0) -> bool:
        """Add audio track to video using FFmpeg."""
        try:
            volume_filter = f'volume={audio_volume}' if audio_volume != 1.0 else ''
            
            if volume_filter:
                cmd = [
                    'ffmpeg', '-y', '-i', video_path, '-i', audio_path,
                    '-filter_complex', f'[1:a]{volume_filter}[a]',
                    '-map', '0:v', '-map', '[a]',
                    '-c:v', 'copy', '-shortest', output_path
                ]
            else:
                cmd = [
                    'ffmpeg', '-y', '-i', video_path, '-i', audio_path,
                    '-c:v', 'copy', '-c:a', 'aac', '-shortest', output_path
                ]
            
            subprocess.run(cmd, check=True, capture_output=True)
            return True
        except Exception as e:
            logger.error(f"Error adding audio to video: {e}")
            return False
    
    def add_background_music_ffmpeg(self, video_path: str, music_path: str, 
                                   output_path: str, music_volume: float = 0.5) -> bool:
        """Mix video audio with background music using FFmpeg directly."""
        try:
            # Check if video has audio
            probe_cmd = [
                'ffprobe', '-v', 'quiet', '-select_streams', 'a:0', 
                '-show_entries', 'stream=index', '-of', 'csv=p=0', video_path
            ]
            
            try:
                result = subprocess.run(probe_cmd, capture_output=True, text=True, check=True)
                has_audio = bool(result.stdout.strip())
            except subprocess.CalledProcessError:
                has_audio = False
            
            if has_audio:
                # Mix existing video audio with background music
                cmd = [
                    'ffmpeg', '-y', '-i', video_path, '-i', music_path,
                    '-filter_complex', 
                    f'[0:a]volume=1.0[a0];[1:a]volume={music_volume}[a1];[a0][a1]amix=inputs=2:duration=first:normalize=0[mixed]',
                    '-map', '0:v', '-map', '[mixed]',
                    '-c:v', 'copy', '-c:a', 'aac', '-b:a', '192k',
                    '-shortest', output_path
                ]
            else:
                # Video has no audio, just add background music
                cmd = [
                    'ffmpeg', '-y', '-i', video_path, '-i', music_path,
                    '-filter_complex', f'[1:a]volume={music_volume}[a]',
                    '-map', '0:v', '-map', '[a]',
                    '-c:v', 'copy', '-c:a', 'aac', '-b:a', '192k',
                    '-shortest', output_path
                ]
            
            logger.info(f"Running FFmpeg background music command: {' '.join(cmd)}")
            result = subprocess.run(cmd, check=True, capture_output=True, text=True)
            
            if result.returncode == 0:
                logger.info(f"Background music added successfully: {output_path}")
                return True
            else:
                logger.error(f"FFmpeg failed with return code {result.returncode}: {result.stderr}")
                return False
                
        except subprocess.CalledProcessError as e:
            logger.error(f"FFmpeg command failed: {e.stderr if e.stderr else str(e)}")
            return False
        except Exception as e:
            logger.error(f"Error adding background music with FFmpeg: {e}")
            return False
    
    def trim_video(self, input_path: str, output_path: str, 
                  start_time: float = 0.0, duration: Optional[float] = None) -> bool:
        """Trim video using FFmpeg."""
        try:
            cmd = ['ffmpeg', '-y', '-ss', str(start_time), '-i', input_path]
            
            if duration:
                cmd.extend(['-t', str(duration)])
            
            cmd.extend(['-c', 'copy', output_path])
            subprocess.run(cmd, check=True, capture_output=True)
            return True
        except Exception as e:
            logger.error(f"Error trimming video: {e}")
            return False
    
    def create_video_preview(self, input_path: str, output_path: str,
                           duration: float = 10.0, start_time: float = 0.0) -> bool:
        """Create video preview using FFmpeg."""
        return self.trim_video(input_path, output_path, start_time, duration)
    
    def convert_audio_format(self, input_path: str, output_path: str,
                           format: str = 'wav', sample_rate: int = 44100) -> bool:
        """Convert audio format using FFmpeg."""
        try:
            cmd = [
                'ffmpeg', '-y', '-i', input_path,
                '-ar', str(sample_rate), '-ac', '2', output_path
            ]
            subprocess.run(cmd, check=True, capture_output=True)
            return True
        except Exception as e:
            logger.error(f"Error converting audio: {e}")
            return False
    
    def normalize_audio(self, input_path: str, output_path: str,
                       target_lufs: float = -23.0) -> bool:
        """Normalize audio using FFmpeg loudnorm filter."""
        try:
            cmd = [
                'ffmpeg', '-y', '-i', input_path,
                '-af', f'loudnorm=I={target_lufs}:TP=-1.5:LRA=11',
                output_path
            ]
            subprocess.run(cmd, check=True, capture_output=True)
            return True
        except Exception as e:
            logger.error(f"Error normalizing audio: {e}")
            return False
    
    def mix_audio_files(self, audio_paths: List[str], output_path: str,
                       volumes: Optional[List[float]] = None) -> bool:
        """Mix multiple audio files using FFmpeg."""
        try:
            if not audio_paths:
                return False
            
            if len(audio_paths) == 1:
                cmd = ['ffmpeg', '-y', '-i', audio_paths[0], '-c', 'copy', output_path]
                subprocess.run(cmd, check=True, capture_output=True)
                return True
            
            # Build complex filter for mixing
            inputs = []
            for i, path in enumerate(audio_paths):
                inputs.extend(['-i', path])
            
            if volumes:
                volume_filters = []
                for i, vol in enumerate(volumes[:len(audio_paths)]):
                    volume_filters.append(f'[{i}:a]volume={vol}[a{i}]')
                
                mix_inputs = ''.join(f'[a{i}]' for i in range(len(audio_paths)))
                filter_complex = ';'.join(volume_filters) + f';{mix_inputs}amix=inputs={len(audio_paths)}:normalize=0[mixed];[mixed]alimiter=limit=0.95[out]'
                
                cmd = ['ffmpeg', '-y'] + inputs + ['-filter_complex', filter_complex, '-map', '[out]', output_path]
            else:
                mix_inputs = ''.join(f'[{i}:a]' for i in range(len(audio_paths)))
                filter_complex = f'{mix_inputs}amix=inputs={len(audio_paths)}:normalize=0[mixed];[mixed]alimiter=limit=0.95[out]'
                
                cmd = ['ffmpeg', '-y'] + inputs + ['-filter_complex', filter_complex, '-map', '[out]', output_path]
            
            subprocess.run(cmd, check=True, capture_output=True)
            return True
        except Exception as e:
            logger.error(f"Error mixing audio files: {e}")
            return False


# Global instance
ffmpeg_processor = None

def get_ffmpeg_processor() -> Optional[FFmpegProcessor]:
    """Get global FFmpeg processor instance."""
    global ffmpeg_processor
    if ffmpeg_processor is None:
        try:
            ffmpeg_processor = FFmpegProcessor()
        except RuntimeError as e:
            logger.warning(f"FFmpeg not available: {e}")
            return None
    return ffmpeg_processor


# Convenience functions that fallback to MoviePy if FFmpeg is not available
def concatenate_videos_fast(video_paths: List[str], output_path: str) -> bool:
    """Fast video concatenation using FFmpeg, fallback to MoviePy."""
    processor = get_ffmpeg_processor()
    if processor:
        success = processor.concatenate_videos(video_paths, output_path)
        if success:
            return True
        else:
            logger.warning("FFmpeg concatenation failed, trying MoviePy fallback")
    
    # Fallback to MoviePy
    try:
        from moviepy import VideoFileClip, concatenate_videoclips
        logger.info(f"Usando MoviePy per concatenare {len(video_paths)} video")
        
        clips = []
        for path in video_paths:
            if os.path.exists(path):
                clip = VideoFileClip(path)
                clips.append(clip)
            else:
                logger.warning(f"File video non trovato: {path}")
        
        if not clips:
            logger.error("Nessun clip valido per la concatenazione MoviePy")
            return False
            
        final_clip = concatenate_videoclips(clips, method="compose")
        final_clip.write_videofile(
            output_path,
            codec='libx264',
            audio_codec='aac',
            preset='medium',
            logger=None,
            bitrate='5000k',
            audio_bitrate='192k'
        )
        
        # Cleanup
        for clip in clips:
            clip.close()
        final_clip.close()
        
        # Verifica che il file sia stato creato correttamente
        if os.path.exists(output_path) and os.path.getsize(output_path) > 1024:
            logger.info(f"Concatenazione MoviePy completata: {output_path}")
            return True
        else:
            logger.error("File output MoviePy vuoto o non creato")
            return False
            
    except Exception as e:
        logger.error(f"Error in MoviePy fallback: {e}")
        return False


def resize_video_fast(input_path: str, output_path: str, width: int = 1920, height: int = 1080) -> bool:
    """Fast video resizing using FFmpeg, fallback to MoviePy."""
    processor = get_ffmpeg_processor()
    if processor:
        return processor.resize_video(input_path, output_path, width, height)
    
    # Fallback to MoviePy
    try:
        from moviepy import VideoFileClip
        clip = VideoFileClip(input_path)
        resized = clip.resized((width, height))
        resized.write_videofile(output_path)
        clip.close()
        resized.close()
        return True
    except Exception as e:
        logger.error(f"Error in MoviePy fallback: {e}")
        return False


def extract_audio_fast(video_path: str, output_path: str) -> bool:
    """Fast audio extraction using FFmpeg, fallback to MoviePy."""
    processor = get_ffmpeg_processor()
    if processor:
        return processor.extract_audio(video_path, output_path)
    
    # Fallback to MoviePy
    try:
        from moviepy import VideoFileClip
        clip = VideoFileClip(video_path)
        if clip.audio:
            clip.audio.write_audiofile(output_path)
            clip.close()
            return True
        clip.close()
        return False
    except Exception as e:
        logger.error(f"Error in MoviePy fallback: {e}")
        return False
    
    

def run_ffmpeg_command(cmd: list, stage_desc: str, status_callback: Callable = print, progress: bool = False, timeout: Optional[float] = None) -> subprocess.CompletedProcess:
    """Esegue un comando FFmpeg con gestione ottimizzata di output, errori e timeout."""
    if '-loglevel' not in cmd:
        insert_pos = 1
        if '-y' in cmd: insert_pos = cmd.index('-y') + 1
        cmd = cmd[:insert_pos] + ['-loglevel', 'info'] + cmd[insert_pos:]

    progress_file = None
    if progress and len(cmd) > 1 and isinstance(cmd[-1], str) and '.' in os.path.basename(cmd[-1]):
        try:
            out_dir = os.path.dirname(cmd[-1]) or "."
            progress_file = os.path.join(out_dir, f"ffmpeg_progress_{uuid.uuid4().hex}.txt")
            cmd = cmd[:-1] + ['-progress', progress_file] + [cmd[-1]]
        except Exception as e_prog_file:
            status_callback(f"⚠️ Impossibile impostare file progresso ffmpeg: {e_prog_file}")
            progress_file = None

    cmd_str_log = ' '.join(f'"{c}"' if ' ' in c else c for c in cmd)
    if len(cmd_str_log) > 300: cmd_str_log = cmd_str_log[:150] + " ... " + cmd_str_log[-150:]
    status_callback(f"[FFMPEG] ({stage_desc}): {cmd_str_log}")

    process = None
    try:
        process = subprocess.Popen(
            cmd, stdout=subprocess.PIPE, stderr=subprocess.STDOUT,
            text=True, encoding='utf-8', errors='ignore'
        )

        stdout_lines = []
        if process.stdout:
            for line in iter(process.stdout.readline, ''):
                line_stripped = line.strip()
                if line_stripped:
                    status_callback(f"[FFMPEG-LOG] {line_stripped}")
                    stdout_lines.append(line_stripped)
        
        return_code = process.wait(timeout=timeout)

        if return_code != 0:
            full_output = "\n".join(stdout_lines)
            raise subprocess.CalledProcessError(return_code, cmd, full_output, full_output)

    except subprocess.TimeoutExpired as e:
        if process:
            process.kill()
        status_callback(f"❌ FFMPEG TIMEOUT ({stage_desc}): Il comando ha superato il limite di {timeout}s.")
        raise Exception(f"ffmpeg timeout in {stage_desc}.") from e
    except subprocess.CalledProcessError as e:
        status_callback(f"❌ ERRORE ffmpeg ({stage_desc}): Return code {e.returncode}")
        raise Exception(f"ffmpeg fallito (codice {e.returncode}) in {stage_desc}.") from e
    except FileNotFoundError:
        status_callback("❌ ERRORE FATALE: ffmpeg o ffprobe non trovato.")
        raise
    except Exception as e:
        status_callback(f"❌ ERRORE IMPREVISTO durante esecuzione ffmpeg ({stage_desc}): {e}")
        raise Exception(f"Errore imprevisto in {stage_desc}: {e}") from e
    finally:
        if progress_file and os.path.exists(progress_file):
            try:
                os.remove(progress_file)
            except OSError:
                pass

    return subprocess.CompletedProcess(cmd, 0, '\n'.join(stdout_lines), '')