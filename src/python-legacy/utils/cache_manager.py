"""Cache manager for video processing optimization."""

import os
import hashlib
import json
import shutil
import subprocess
import sys
from typing import Dict, Optional, Tuple, List, Any
from pathlib import Path
import time
from functools import lru_cache
import requests
from urllib.parse import urlparse
import logging

class VideoCacheManager:
    """Manages caching of processed video clips to speed up video generation.
    
    NOTE: La cache dei video (stock clips, processed clips) è DISABILITATA
    per risparmiare memoria su computer con poca RAM. Solo immagini e trascrizioni
    vengono cachate, in quanto sono file leggeri che non causano problemi di memoria.
    """
    
    def __init__(self, cache_dir: str = "video_cache"):
        """Initialize the cache manager.
        
        Args:
            cache_dir: Directory to store cached files
            NOTE: Solo immagini e trascrizioni vengono cachate. Video disabilitati.
        """
        self.cache_dir = Path(cache_dir)
        # Be robust to nested cache paths and varying working directories.
        self.cache_dir.mkdir(parents=True, exist_ok=True)
        
        # Subdirectories for different types of cached content
        self.stock_cache_dir = self.cache_dir / "stock_clips"
        self.processed_cache_dir = self.cache_dir / "processed_clips"
        self.images_cache_dir = self.cache_dir / "images"
        self.transcription_cache_dir = self.cache_dir / "transcriptions"
        self.metadata_cache_dir = self.cache_dir / "metadata"
        
        for dir_path in [
            self.stock_cache_dir,
            self.processed_cache_dir,
            self.images_cache_dir,
            self.transcription_cache_dir,
            self.metadata_cache_dir,
        ]:
            dir_path.mkdir(parents=True, exist_ok=True)
        
        # Cache metadata file
        self.metadata_file = self.metadata_cache_dir / "cache_metadata.json"
        self.metadata = self._load_metadata()
        
        # FFmpeg metadata cache
        self._ffmpeg_metadata_cache = {}
        self._cache_hits = 0
        self._cache_misses = 0
        
        # Logger
        self.logger = logging.getLogger(__name__)
    
    def _load_metadata(self) -> Dict:
        """Load cache metadata from file."""
        if self.metadata_file.exists():
            try:
                with open(self.metadata_file, 'r', encoding='utf-8') as f:
                    return json.load(f)
            except (json.JSONDecodeError, IOError):
                return {}
        return {}
    
    def _save_metadata(self):
        """Save cache metadata to file."""
        try:
            with open(self.metadata_file, 'w', encoding='utf-8') as f:
                json.dump(self.metadata, f, indent=2, ensure_ascii=False)
        except IOError as e:
            print(f"Warning: Could not save cache metadata: {e}")
    
    def get_video_metadata_complete(self, file_path: str) -> Dict[str, Any]:
        """Ottiene e cache TUTTI i metadati di un video con una singola chiamata ffprobe.
        
        Args:
            file_path: Percorso del file video/audio
            
        Returns:
            Dizionario con tutti i metadati del file
        """
        try:
            # Verifica se il file esiste
            if not os.path.exists(file_path):
                self.logger.error(f"File non trovato: {file_path}")
                return {}
            
            # Genera chiave cache basata su path, timestamp e dimensione
            file_stat = os.stat(file_path)
            cache_key = f"{file_path}_{file_stat.st_mtime}_{file_stat.st_size}"
            
            # Controlla se è già in cache
            if cache_key in self._ffmpeg_metadata_cache:
                self._cache_hits += 1
                return self._ffmpeg_metadata_cache[cache_key]
            
            self._cache_misses += 1
            
            # Chiamata completa a ffprobe
            cmd = [
                'ffprobe', '-v', 'quiet', '-print_format', 'json',
                '-show_format', '-show_streams', file_path
            ]
            
            result = subprocess.run(cmd, capture_output=True, text=True, check=True)
            data = json.loads(result.stdout)
            
            # Estrai metadati video e audio
            video_stream = next((s for s in data['streams'] if s['codec_type'] == 'video'), None)
            audio_stream = next((s for s in data['streams'] if s['codec_type'] == 'audio'), None)
            
            # Costruisci dizionario metadati completo
            metadata = {
                'duration': float(data['format'].get('duration', 0)),
                'size': int(data['format'].get('size', 0)),
                'bitrate': int(data['format'].get('bit_rate', 0)),
                'format_name': data['format'].get('format_name', ''),
                'video': {
                    'codec': video_stream['codec_name'] if video_stream else None,
                    'width': int(video_stream.get('width', 0)) if video_stream else 0,
                    'height': int(video_stream.get('height', 0)) if video_stream else 0,
                    'fps': self._parse_fps(video_stream.get('r_frame_rate', '0/1')) if video_stream else 0,
                    'bitrate': int(video_stream.get('bit_rate', 0)) if video_stream else 0
                } if video_stream else None,
                'audio': {
                    'codec': audio_stream['codec_name'] if audio_stream else None,
                    'sample_rate': int(audio_stream.get('sample_rate', 0)) if audio_stream else 0,
                    'channels': int(audio_stream.get('channels', 0)) if audio_stream else 0,
                    'bitrate': int(audio_stream.get('bit_rate', 0)) if audio_stream else 0
                } if audio_stream else None
            }
            
            # Salva in cache
            self._ffmpeg_metadata_cache[cache_key] = metadata
            
            # Limita dimensione cache (LRU)
            if len(self._ffmpeg_metadata_cache) > 1000:
                # Rimuovi il 10% più vecchio
                keys_to_remove = list(self._ffmpeg_metadata_cache.keys())[:100]
                for key in keys_to_remove:
                    del self._ffmpeg_metadata_cache[key]
            
            return metadata
            
        except (subprocess.CalledProcessError, json.JSONDecodeError, ValueError, OSError) as e:
            self.logger.error(f"Errore nell'ottenere metadati per {file_path}: {e}")
            return {}
    
    def _parse_fps(self, fps_string: str) -> float:
        """Converte stringa fps (es. '30/1') in float."""
        try:
            if '/' in fps_string:
                num, den = fps_string.split('/')
                return float(num) / float(den) if float(den) != 0 else 0
            return float(fps_string)
        except (ValueError, ZeroDivisionError):
            return 0.0
    
    # Funzioni di accesso rapido ai metadati
    def get_video_duration_cached(self, file_path: str) -> float:
        """Ottiene la durata del video dalla cache."""
        metadata = self.get_video_metadata_complete(file_path)
        return metadata.get('duration', 0.0)
    
    def get_video_resolution_cached(self, file_path: str) -> str:
        """Ottiene la risoluzione del video dalla cache nel formato 'WIDTHxHEIGHT'."""
        metadata = self.get_video_metadata_complete(file_path)
        video = metadata.get('video')
        if video:
            width = video.get('width', 0)
            height = video.get('height', 0)
            return f"{width}x{height}"
        return "0x0"
    
    def get_video_fps_cached(self, file_path: str) -> float:
        """Ottiene gli FPS del video dalla cache."""
        metadata = self.get_video_metadata_complete(file_path)
        video = metadata.get('video')
        if video:
            return video.get('fps', 0.0)
        return 0.0
    
    def get_video_codec_cached(self, file_path: str) -> str:
        """Ottiene il codec video dalla cache."""
        metadata = self.get_video_metadata_complete(file_path)
        video = metadata.get('video')
        if video:
            return video.get('codec', '')
        return ''
    
    def get_audio_codec_cached(self, file_path: str) -> str:
        """Ottiene il codec audio dalla cache."""
        metadata = self.get_video_metadata_complete(file_path)
        audio = metadata.get('audio')
        if audio:
            return audio.get('codec', '')
        return ''
    
    def get_file_size_cached(self, file_path: str) -> int:
        """Ottiene la dimensione del file dalla cache."""
        metadata = self.get_video_metadata_complete(file_path)
        return metadata.get('size', 0)
    
    def has_audio_cached(self, file_path: str) -> bool:
        """Verifica se il file ha traccia audio dalla cache."""
        metadata = self.get_video_metadata_complete(file_path)
        return metadata.get('audio') is not None
    
    def get_cache_stats(self) -> Dict[str, Any]:
        """Ottiene statistiche della cache FFmpeg."""
        total_requests = self._cache_hits + self._cache_misses
        hit_rate = (self._cache_hits / total_requests * 100) if total_requests > 0 else 0
        
        return {
            'cache_hits': self._cache_hits,
            'cache_misses': self._cache_misses,
            'hit_rate_percent': round(hit_rate, 2),
            'cached_files': len(self._ffmpeg_metadata_cache),
            'total_requests': total_requests
        }
    
    @staticmethod
    def _get_file_hash(file_path: str) -> str:
        """Calculate MD5 hash of a file.
        
        Args:
            file_path: Path to the file
            
        Returns:
            MD5 hash string
        """
        hash_md5 = hashlib.md5()
        try:
            with open(file_path, "rb") as f:
                for chunk in iter(lambda: f.read(4096), b""):
                    hash_md5.update(chunk)
            return hash_md5.hexdigest()
        except IOError:
            return ""
    
    def _get_cache_key(self, file_path: str, processing_params: Dict = None) -> str:
        """Generate cache key based on file hash and processing parameters.
        
        Args:
            file_path: Path to the source file
            processing_params: Dictionary of processing parameters
            
        Returns:
            Cache key string
        """
        file_hash = self._get_file_hash(file_path)
        if not file_hash:
            return ""
        
        if processing_params:
            params_str = json.dumps(processing_params, sort_keys=True)
            params_hash = hashlib.md5(params_str.encode()).hexdigest()[:8]
            return f"{file_hash}_{params_hash}"
        
        return file_hash
    
    def get_cached_stock_clip(self, original_path: str, duration: float, start_time: float = 0) -> Optional[str]:
        """Get cached stock clip if available.
        
        NOTE: Cache dei video stock DISABILITATA per risparmiare memoria.
        Solo immagini e trascrizioni vengono cachate.
        
        Args:
            original_path: Path to original stock clip
            duration: Required duration
            start_time: Start time for trimming
            
        Returns:
            None (cache disabilitata per video)
        """
        # Cache disabilitata per video stock - troppo pesanti per computer con poca memoria
        return None
    
    def cache_stock_clip(self, original_path: str, processed_path: str, duration: float, start_time: float = 0) -> bool:
        """Cache a processed stock clip.
        
        NOTE: Cache dei video stock DISABILITATA per risparmiare memoria.
        Solo immagini e trascrizioni vengono cachate.
        
        Args:
            original_path: Path to original stock clip
            processed_path: Path to processed clip to cache
            duration: Duration of the clip
            start_time: Start time used for trimming
            
        Returns:
            False (cache disabilitata per video)
        """
        # Cache disabilitata per video stock - troppo pesanti per computer con poca memoria
        return False
    
    def _get_url_hash(self, url: str) -> str:
        """Generate hash for URL.
        
        Args:
            url: URL to hash
            
        Returns:
            MD5 hash string
        """
        return hashlib.md5(url.encode()).hexdigest()
    
    def get_cached_image(self, url: str) -> Optional[str]:
        """Get cached image if available.
        
        Args:
            url: URL of the image
            
        Returns:
            Path to cached image or None if not found
        """
        if not url or not isinstance(url, str):
            return None
        
        url_hash = self._get_url_hash(url)
        cache_key = f"img_{url_hash}"
        
        # Check if we have metadata for this image
        if cache_key in self.metadata:
            cached_path = self.metadata[cache_key].get("cached_path")
            if cached_path and os.path.exists(cached_path):
                # Update access time
                self.metadata[cache_key]["last_accessed"] = time.time()
                self._save_metadata()
                return cached_path
            else:
                # Remove stale metadata
                del self.metadata[cache_key]
                self._save_metadata()
        
        return None
    
    def cache_image(self, url: str, local_path: str) -> bool:
        """Cache a downloaded image.
        
        Args:
            url: Original URL of the image
            local_path: Path to the downloaded image file
            
        Returns:
            True if caching was successful
        """
        if not url or not isinstance(url, str) or not os.path.exists(local_path):
            return False
        
        try:
            url_hash = self._get_url_hash(url)
            
            # Determine file extension
            parsed_url = urlparse(url)
            url_ext = os.path.splitext(parsed_url.path)[1]
            if not url_ext:
                # Try to get extension from local file
                url_ext = os.path.splitext(local_path)[1]
            if not url_ext:
                url_ext = '.jpg'  # Default extension
            
            cache_key = f"img_{url_hash}"
            cached_file = self.images_cache_dir / f"{cache_key}{url_ext}"
            
            # Copy the file to cache
            shutil.copy2(local_path, cached_file)
            
            # Update metadata
            self.metadata[cache_key] = {
                "original_url": url,
                "cached_path": str(cached_file),
                "type": "image",
                "created_time": time.time(),
                "last_accessed": time.time(),
                "file_size": os.path.getsize(cached_file)
            }
            
            self._save_metadata()
            return True
            
        except (IOError, OSError) as e:
            print(f"Warning: Could not cache image {url}: {e}")
            return False
    
    def download_and_cache_image(self, url: str, headers: Optional[Dict] = None, timeout: int = 10) -> Optional[str]:
        """Download and cache an image from URL.
        
        Args:
            url: URL of the image to download
            headers: Optional HTTP headers
            timeout: Request timeout in seconds
            
        Returns:
            Path to cached image or None if failed
        """
        # First check if image is already cached
        cached_path = self.get_cached_image(url)
        if cached_path:
            return cached_path
        
        # Download the image
        try:
            if headers is None:
                headers = {"User-Agent": "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36"}
            
            response = requests.get(url, headers=headers, timeout=timeout, stream=True)
            response.raise_for_status()
            
            # Determine file extension
            content_type = response.headers.get('Content-Type', '').lower()
            if 'jpeg' in content_type or 'jpg' in content_type:
                ext = '.jpg'
            elif 'png' in content_type:
                ext = '.png'
            elif 'gif' in content_type:
                ext = '.gif'
            elif 'webp' in content_type:
                ext = '.webp'
            else:
                parsed_url = urlparse(url)
                ext = os.path.splitext(parsed_url.path)[1] or '.jpg'
            
            # Create temporary file for download
            url_hash = self._get_url_hash(url)
            cache_key = f"img_{url_hash}"
            cached_file = self.images_cache_dir / f"{cache_key}{ext}"
            
            # Download and save
            with open(cached_file, 'wb') as f:
                for chunk in response.iter_content(chunk_size=8192):
                    if chunk:
                        f.write(chunk)
            
            # Verify the image (basic check)
            try:
                from PIL import Image
                with Image.open(cached_file) as img:
                    img.verify()
            except Exception:
                # If verification fails, still keep the file but log warning
                print(f"Warning: Downloaded image may be corrupted: {url}")
            
            # Update metadata
            self.metadata[cache_key] = {
                "original_url": url,
                "cached_path": str(cached_file),
                "type": "image",
                "created_time": time.time(),
                "last_accessed": time.time(),
                "file_size": os.path.getsize(cached_file)
            }
            
            self._save_metadata()
            return str(cached_file)
            
        except Exception as e:
            print(f"Warning: Could not download and cache image {url}: {e}")
            return None
    
    @lru_cache(maxsize=128)
    def get_clip_duration(self, file_path: str) -> Optional[float]:
        """Get cached clip duration using ffprobe.
        
        Args:
            file_path: Path to video file
            
        Returns:
            Duration in seconds or None if error
        """
        import subprocess
        try:
            result = subprocess.run([
                'ffprobe', '-v', 'quiet', '-show_entries', 'format=duration',
                '-of', 'csv=p=0', file_path
            ], capture_output=True, text=True, timeout=10)
            
            if result.returncode == 0 and result.stdout.strip():
                return float(result.stdout.strip())
        except (subprocess.TimeoutExpired, subprocess.CalledProcessError, ValueError):
            pass
        
        return None
    
    def cleanup_old_cache(self, max_age_days: int = 7, max_size_gb: float = 5.0):
        """Clean up old cache files.
        
        Args:
            max_age_days: Maximum age of cache files in days
            max_size_gb: Maximum total cache size in GB
        """
        current_time = time.time()
        max_age_seconds = max_age_days * 24 * 3600
        max_size_bytes = max_size_gb * 1024 * 1024 * 1024
        
        # Get all cache files with their metadata
        cache_files = []
        total_size = 0
        
        for cache_key, metadata in list(self.metadata.items()):
            cached_path = metadata.get("cached_path")
            if cached_path and os.path.exists(cached_path):
                file_size = metadata.get("file_size", 0)
                last_accessed = metadata.get("last_accessed", 0)
                
                cache_files.append({
                    "key": cache_key,
                    "path": cached_path,
                    "size": file_size,
                    "last_accessed": last_accessed
                })
                total_size += file_size
            else:
                # Remove metadata for non-existent files
                del self.metadata[cache_key]
        
        # Remove old files
        files_to_remove = []
        for file_info in cache_files:
            age = current_time - file_info["last_accessed"]
            if age > max_age_seconds:
                files_to_remove.append(file_info)
        
        # Remove files by size if total size exceeds limit
        if total_size > max_size_bytes:
            # Sort by last accessed time (oldest first)
            cache_files.sort(key=lambda x: x["last_accessed"])
            
            for file_info in cache_files:
                if total_size <= max_size_bytes:
                    break
                if file_info not in files_to_remove:
                    files_to_remove.append(file_info)
                    total_size -= file_info["size"]
        
        # Actually remove the files
        for file_info in files_to_remove:
            try:
                os.remove(file_info["path"])
                del self.metadata[file_info["key"]]
                print(f"Removed cached file: {file_info['path']}")
            except OSError as e:
                print(f"Warning: Could not remove cached file {file_info['path']}: {e}")
        
        if files_to_remove:
            self._save_metadata()
            print(f"Cache cleanup completed. Removed {len(files_to_remove)} files.")
    
    def clear_transcription_cache(self) -> bool:
        """Clear only transcription cache files and related metadata.
        
        Returns:
            True if successful
        """
        try:
            # Remove transcription cache directory
            if self.transcription_cache_dir.exists():
                shutil.rmtree(self.transcription_cache_dir)
                self.transcription_cache_dir.mkdir(parents=True, exist_ok=True)
            
            # Clear transcription-related metadata
            transcription_keys = [key for key, value in self.metadata.items() 
                                if value.get('processing_params', {}).get('type') == 'transcription']
            for key in transcription_keys:
                del self.metadata[key]
            
            self._save_metadata()
            
            self.logger.info("🧹 Transcription cache cleared successfully.")
            return True
            
        except Exception as e:
            self.logger.error(f"Error clearing transcription cache: {e}")
            return False
    
    def clear_all_cache(self) -> bool:
        """Clear all cache files and metadata.
        
        Returns:
            True if successful
        """
        try:
            # Remove all cache directories
            for cache_dir in [self.stock_cache_dir, self.processed_cache_dir, self.images_cache_dir, self.transcription_cache_dir]:
                if cache_dir.exists():
                    shutil.rmtree(cache_dir)
                    cache_dir.mkdir(parents=True, exist_ok=True)
            
            # Clear metadata
            self.metadata.clear()
            self._save_metadata()
            
            # Clear LRU cache
            self.get_clip_duration.cache_clear()
            
            print("All cache cleared successfully.")
            return True
            
        except Exception as e:
            print(f"Error clearing cache: {e}")
            return False
    
    def get_cached_transcription(self, audio_path: str, model_size: str = "small", language_code: str = None) -> Optional[Tuple[str, List]]:
        """Get cached transcription if available.
        
        Args:
            audio_path: Path to the audio file
            model_size: Whisper model size
            language_code: Language code
            
        Returns:
            Tuple (transcribed_text, segments) or None if not found
        """
        processing_params = {
            "model_size": model_size,
            "language_code": language_code,
            "type": "transcription"
        }
        
        cache_key = self._get_cache_key(audio_path, processing_params)
        if not cache_key:
            return None
        
        cached_file = self.transcription_cache_dir / f"{cache_key}.json"
        
        if cached_file.exists() and cache_key in self.metadata:
            try:
                with open(cached_file, 'r', encoding='utf-8') as f:
                    data = json.load(f)
                
                # Update access time
                self.metadata[cache_key]["last_accessed"] = time.time()
                self._save_metadata()
                
                self.logger.info(f"🎯 Transcription retrieved from cache: {os.path.basename(audio_path)}")
                return data["text"], data["segments"]
            except (json.JSONDecodeError, IOError, KeyError) as e:
                self.logger.warning(f"Error reading cached transcription: {e}")
        
        return None

    def cache_transcription(self, audio_path: str, text: str, segments: List, model_size: str = "small", language_code: str = None) -> bool:
        """Save a transcription to cache.
        
        Args:
            audio_path: Path to the audio file
            text: Transcribed text
            segments: List of segments
            model_size: Whisper model size
            language_code: Language code
            
        Returns:
            True if caching succeeded
        """
        processing_params = {
            "model_size": model_size,
            "language_code": language_code,
            "type": "transcription"
        }
        
        cache_key = self._get_cache_key(audio_path, processing_params)
        if not cache_key:
            return False
        
        cached_file = self.transcription_cache_dir / f"{cache_key}.json"
        
        try:
            # Ensure cache directory exists even if the environment changed (e.g., cwd, cleaned cache).
            cached_file.parent.mkdir(parents=True, exist_ok=True)

            # Convert faster_whisper Segment objects to serializable dictionaries
            serializable_segments = []
            for segment in segments:
                if hasattr(segment, 'start') and hasattr(segment, 'end') and hasattr(segment, 'text'):
                    # This is a faster_whisper Segment object
                    serializable_segments.append({
                        "start": float(segment.start),
                        "end": float(segment.end),
                        "text": segment.text.strip()
                    })
                elif isinstance(segment, dict):
                    # This is already a dictionary
                    serializable_segments.append(segment)
                else:
                    # Fallback: try to convert to string representation
                    serializable_segments.append(str(segment))
            
            data = {
                "text": text,
                "segments": serializable_segments,
                "audio_path": audio_path,
                "processing_params": processing_params
            }
            
            with open(cached_file, 'w', encoding='utf-8') as f:
                json.dump(data, f, indent=2, ensure_ascii=False)
            
            # Update metadata
            self.metadata[cache_key] = {
                "audio_path": audio_path,
                "cached_path": str(cached_file),
                "processing_params": processing_params,
                "created_time": time.time(),
                "last_accessed": time.time(),
                "file_size": os.path.getsize(cached_file)
            }
            
            self._save_metadata()
            self.logger.info(f"💾 Transcription saved to cache: {os.path.basename(audio_path)}")
            return True
            
        except (IOError, OSError) as e:
            self.logger.warning(f"Cannot cache transcription {audio_path}: {e}")
            return False

    def get_general_cache_stats(self) -> Dict:
        """Get general cache statistics for files and images.
        
        Returns:
            Dictionary with general cache statistics
        """
        total_files = 0
        total_size = 0
        video_files = 0
        image_files = 0
        video_size = 0
        image_size = 0
        
        for metadata in self.metadata.values():
            cached_path = metadata.get("cached_path")
            if cached_path and os.path.exists(cached_path):
                file_size = metadata.get("file_size", 0)
                file_type = metadata.get("type", "unknown")
                
                total_files += 1
                total_size += file_size
                
                if file_type == "image":
                    image_files += 1
                    image_size += file_size
                else:
                    video_files += 1
                    video_size += file_size
        
        return {
            "total_files": total_files,
            "total_size_mb": round(total_size / (1024 * 1024), 2),
            "video_files": video_files,
            "video_size_mb": round(video_size / (1024 * 1024), 2),
            "image_files": image_files,
            "image_size_mb": round(image_size / (1024 * 1024), 2),
            "cache_dir": str(self.cache_dir)
        }


    def get_cached_stock_segment(self, stock_clips: List[str], duration: float, 
                                base_w: int, base_h: int, base_fps: float, 
                                ffmpeg_preset: str = "medium") -> Optional[str]:
        """Recupera un segmento stock dalla cache se disponibile.
        
        NOTE: Cache dei video stock DISABILITATA per risparmiare memoria.
        Solo immagini e trascrizioni vengono cachate.
        
        Args:
            stock_clips: Lista dei clip stock utilizzati
            duration: Durata richiesta del segmento
            base_w: Larghezza base del video
            base_h: Altezza base del video
            base_fps: FPS base del video
            ffmpeg_preset: Preset FFmpeg utilizzato
            
        Returns:
            None (cache disabilitata per video)
        """
        # Cache disabilitata per video stock - troppo pesanti per computer con poca memoria
        return None
    
    def cache_stock_segment(self, stock_clips: List[str], duration: float,
                           base_w: int, base_h: int, base_fps: float,
                           ffmpeg_preset: str, output_path: str) -> bool:
        """Salva un segmento stock nella cache.
        
        NOTE: Cache dei video stock DISABILITATA per risparmiare memoria.
        Solo immagini e trascrizioni vengono cachate.
        
        Args:
            stock_clips: Lista dei clip stock utilizzati
            duration: Durata del segmento
            base_w: Larghezza base del video
            base_h: Altezza base del video
            base_fps: FPS base del video
            ffmpeg_preset: Preset FFmpeg utilizzato
            output_path: Percorso del file da cachare
            
        Returns:
            False (cache disabilitata per video)
        """
        # Cache disabilitata per video stock - troppo pesanti per computer con poca memoria
        return False

# --- Wrapper functions globali ---
def get_cached_stock_segment(stock_clips: list, duration: float, base_w: int, base_h: int, base_fps: float, ffmpeg_preset: str = "medium") -> Optional[str]:
    return get_cache_manager().get_cached_stock_segment(stock_clips, duration, base_w, base_h, base_fps, ffmpeg_preset)

def cache_stock_segment(stock_clips: list, duration: float, base_w: int, base_h: int, base_fps: float, ffmpeg_preset: str, output_path: str) -> bool:
    return get_cache_manager().cache_stock_segment(stock_clips, duration, base_w, base_h, base_fps, ffmpeg_preset, output_path)

# Global cache manager instance
_cache_manager = None

def get_cache_manager() -> VideoCacheManager:
    """Get the global cache manager instance."""
    global _cache_manager
    if _cache_manager is None:
        _cache_manager = VideoCacheManager()
    return _cache_manager

# Global cache manager instance for direct import
cache_manager = get_cache_manager()