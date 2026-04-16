"""Audio processing functions for VideoCollegaTanti application."""

import os
import logging
import tempfile
import json
from typing import List, Optional, Dict, Any, Callable, Tuple
from pathlib import Path
import torch
from moviepy import AudioFileClip, ImageClip
import librosa
import soundfile as sf
import numpy as np
from dataclasses import dataclass 
from types import SimpleNamespace

try:
    from config.config import USE_GPU, DEFAULT_SOUND_EFFECTS
    from modules.utils.utils import safe_remove, format_duration
    from modules.video.ffmpeg_utils import get_ffmpeg_processor
    from modules.utils.cache_manager import cache_manager
except ImportError:
    try:
        from config.config import USE_GPU, DEFAULT_SOUND_EFFECTS
        from modules.utils.utils import safe_remove, format_duration
        from modules.video.ffmpeg_utils import get_ffmpeg_processor
        from modules.utils.cache_manager import cache_manager
    except ImportError:
        try:
            from config.config import USE_GPU, DEFAULT_SOUND_EFFECTS
            from modules.utils.utils import safe_remove, format_duration
            from modules.video.ffmpeg_utils import get_ffmpeg_processor
            from modules.utils.cache_manager import cache_manager
        except ImportError:
            # Fallback legacy per compatibilità (solo se tutti i percorsi modulari falliscono)
            from config import USE_GPU, DEFAULT_SOUND_EFFECTS  # type: ignore
            from utils import safe_remove, format_duration  # type: ignore
            from ffmpeg_utils import get_ffmpeg_processor  # type: ignore
            from cache_manager import cache_manager  # type: ignore
import concurrent.futures
import gc
from faster_whisper import WhisperModel
from pydub import AudioSegment

logger = logging.getLogger(__name__)
# ImageMagick configuration removed - not needed in MoviePy 2.x

class AudioProcessor:
    """Handles audio processing operations including transcription and analysis."""
    
    def __init__(self):
        self.whisper_model = None
        self.device = "cuda" if USE_GPU and torch.cuda.is_available() else "cpu"
        logger.info(f"AudioProcessor initialized with device: {self.device}")
    
    def load_whisper_model(self, model_size: str = "base") -> bool:
        """Load Whisper model for transcription using faster_whisper."""
        try:
            if self.whisper_model is None:
                logger.info(f"Loading Whisper model '{model_size}' on {self.device}")
                # Use faster_whisper instead of the standard whisper library
                compute_type = "float16" if self.device == "cuda" else "int8"
                self.whisper_model = WhisperModel(model_size, device=self.device, compute_type=compute_type)
                logger.info("Whisper model loaded successfully")
            return True
        except Exception as e:
            logger.error(f"Failed to load Whisper model: {e}")
            return False
    
    def transcribe_audio(
        self,
        audio_path: str,
        language: str = "auto",
        status_callback: Optional[Callable[[str], None]] = None
    ) -> Optional[Dict[str, Any]]:
        """
        Transcribe audio file using faster_whisper.
        
        Args:
            audio_path: Path to the audio file
            language: Language code or 'auto' for automatic detection
            status_callback: Function to report status updates
        
        Returns:
            Dictionary with transcription results or None if failed
        """
        if status_callback is None:
            status_callback = lambda x: None
        
        try:
            if not os.path.exists(audio_path):
                status_callback(f"❌ File audio non trovato: {audio_path}")
                return None
            
            if not self.load_whisper_model():
                status_callback("❌ Impossibile caricare il modello Whisper")
                return None
            
            status_callback(f"🎤 Trascrizione audio: {os.path.basename(audio_path)}")
            
            # Prepare transcription options for faster_whisper
            language_param = None if language == "auto" else language
            
            # Perform transcription using faster_whisper API
            logger.info(f"🔍 Starting Whisper transcription for: {os.path.basename(audio_path)}")
            segments, info = self.whisper_model.transcribe(
                audio_path, 
                language=language_param,
                beam_size=5,
                word_timestamps=False
            )
            
            # Convert segments generator to list and extract text
            segments_list = list(segments)
            logger.info(f"🔍 Whisper returned {len(segments_list)} segments")
            
            full_text = " ".join([segment.text.strip() for segment in segments_list])
            logger.info(f"🔍 Full text length: {len(full_text)} characters")
            
            if full_text:
                status_callback(f"✅ Trascrizione completata: {len(full_text)} caratteri")
                logger.info(f"✅ Transcription successful - Text preview: {full_text[:100]}...")
                return {
                    "text": full_text.strip(),
                    "language": info.language,
                    "segments": segments_list,  # These are faster_whisper segment objects
                    "duration": self.get_audio_duration(audio_path)
                }
            else:
                status_callback("❌ Trascrizione fallita: risultato vuoto")
                logger.warning("❌ Transcription returned empty text")
                return None
                
        except Exception as e:
            status_callback(f"❌ Errore durante la trascrizione: {str(e)}")
            logger.error(f"Transcription error for {audio_path}: {e}", exc_info=True)
            return None



    def get_audio_duration(self, audio_path: str) -> float:
        """Get duration of audio file in seconds."""
        try:
            audio = AudioFileClip(audio_path)
            duration = audio.duration
            audio.close()
            return duration
        except Exception as e:
            logger.error(f"Error getting audio duration: {e}")
            return 0.0

    def extract_audio_features(self, audio_path: str) -> Optional[Dict[str, Any]]:
        """Extract audio features using librosa."""
        try:
            # Load audio
            y, sr = librosa.load(audio_path)
            
            # Extract features
            features = {
                "duration": len(y) / sr,
                "sample_rate": sr,
                "rms_energy": float(np.mean(librosa.feature.rms(y=y))),
                "zero_crossing_rate": float(np.mean(librosa.feature.zero_crossing_rate(y))),
                "spectral_centroid": float(np.mean(librosa.feature.spectral_centroid(y=y, sr=sr))),
                "tempo": float(librosa.beat.tempo(y=y, sr=sr)[0])
            }
            
            return features
            
        except Exception as e:
            logger.error(f"Error extracting audio features: {e}")
            return None

    def normalize_audio(
        self,
        input_path: str,
        output_path: str,
        target_lufs: float = -23.0
    ) -> bool:
        """Normalize audio to target LUFS level."""
        try:
            # Try FFmpeg first for proper LUFS normalization
            processor = get_ffmpeg_processor()
            if processor:
                if processor.normalize_audio(input_path, output_path, target_lufs):
                    return True
                logger.warning("FFmpeg audio normalization failed, falling back to librosa")
            
            # Fallback to librosa for simple peak normalization
            y, sr = librosa.load(input_path)
            
            # Simple normalization (peak normalization)
            # For proper LUFS normalization, you'd need pyloudnorm
            peak = np.max(np.abs(y))
            if peak > 0:
                y = y / peak * 0.8  # Normalize to 80% of peak
            
            # Save normalized audio
            sf.write(output_path, y, sr)
            return True
            
        except Exception as e:
            logger.error(f"Error normalizing audio: {e}")
            return False

    def convert_audio_format(
        self,
        input_path: str,
        output_path: str,
        format: str = "wav",
        sample_rate: int = 44100
    ) -> bool:
        """Convert audio to specified format and sample rate."""
        try:
            # Try FFmpeg first for better performance
            processor = get_ffmpeg_processor()
            if processor:
                if processor.convert_audio_format(input_path, output_path, format, sample_rate):
                    return True
                logger.warning("FFmpeg audio conversion failed, falling back to pydub")
            
            # Fallback to pydub
            audio = AudioSegment.from_file(input_path)
            audio = audio.set_frame_rate(sample_rate)
            audio.export(output_path, format=format)
            return True
        except Exception as e:
            logger.error(f"Error converting audio format: {e}")
            return False

    def split_audio_by_silence(
        self,
        audio_path: str,
        output_dir: str,
        min_silence_len: int = 1000,
        silence_thresh: int = -40
    ) -> List[str]:
        """Split audio file by silence detection."""
        try:
            from pydub import AudioSegment
            from pydub.silence import split_on_silence
            
            # Load audio
            audio = AudioSegment.from_file(audio_path)
            
            # Split on silence
            chunks = split_on_silence(
                audio,
                min_silence_len=min_silence_len,
                silence_thresh=silence_thresh,
                keep_silence=500
            )
            
            # Save chunks
            output_paths = []
            for i, chunk in enumerate(chunks):
                if len(chunk) > 1000:  # Only save chunks longer than 1 second
                    output_path = os.path.join(output_dir, f"chunk_{i:03d}.wav")
                    chunk.export(output_path, format="wav")
                    output_paths.append(output_path)
            
            return output_paths
            
        except Exception as e:
            logger.error(f"Error splitting audio by silence: {e}")
            return []

    def cleanup(self):
        """Clean up resources."""
        if self.whisper_model is not None:
            del self.whisper_model
            self.whisper_model = None
            if torch.cuda.is_available():
                torch.cuda.empty_cache()


@dataclass
class TranscriptionSegment:
    start: float
    end: float
    text: str


_AUDIO_PROCESSOR_SINGLETON: Optional[AudioProcessor] = None


def _get_audio_processor() -> AudioProcessor:
    """Return a shared AudioProcessor instance."""
    global _AUDIO_PROCESSOR_SINGLETON
    if _AUDIO_PROCESSOR_SINGLETON is None:
        _AUDIO_PROCESSOR_SINGLETON = AudioProcessor()
    return _AUDIO_PROCESSOR_SINGLETON


def _emit_status(callback: Optional[Callable], message: str, error: bool = False) -> None:
    if callback is None:
        return
    try:
        callback(message, error)
    except TypeError:
        callback(message)


def _normalize_transcription_segment(segment: Any) -> Optional[TranscriptionSegment]:
    """
    Convert various segment representations (Whisper object, dict, SimpleNamespace)
    into a TranscriptionSegment.
    """
    try:
        if segment is None:
            return None
        if isinstance(segment, TranscriptionSegment):
            return segment
        if isinstance(segment, SimpleNamespace):
            start = float(getattr(segment, "start", 0.0))
            end = float(getattr(segment, "end", start))
            text = str(getattr(segment, "text", "")).strip()
            return TranscriptionSegment(start=start, end=end, text=text)
        if hasattr(segment, "start") and hasattr(segment, "end") and hasattr(segment, "text"):
            start = float(getattr(segment, "start", 0.0))
            end = float(getattr(segment, "end", start))
            text = str(getattr(segment, "text", "")).strip()
            return TranscriptionSegment(start=start, end=end, text=text)
        if isinstance(segment, dict):
            start = float(segment.get("start", 0.0))
            end = float(segment.get("end", start))
            text = str(segment.get("text", "")).strip()
            return TranscriptionSegment(start=start, end=end, text=text)
    except (TypeError, ValueError):
        return None
    return None


def _serialize_transcription_segments(segments: List[TranscriptionSegment]) -> List[Dict[str, Any]]:
    serializable = []
    for seg in segments:
        if not seg:
            continue
        serializable.append({
            "start": float(seg.start),
            "end": float(seg.end),
            "text": seg.text,
        })
    return serializable


def transcribe_clip_audio(
    clip_path: str,
    status_callback: Optional[Callable[[str, bool], None]],
    label: str,
    processing_params: Optional[Dict[str, Any]] = None
) -> List[TranscriptionSegment]:
    """
    Transcribe an audio/video clip and return Whisper-like segments.

    This function adds caching, logging, and graceful fallbacks so callers
    can always iterate over the returned list without extra checks.
    """
    processing_params = processing_params or {}
    label_prefix = f"[{label}] " if label else ""

    def emit(message: str, error: bool = False) -> None:
        _emit_status(status_callback, f"{label_prefix}{message}", error)

    logger.info(f"{label_prefix}🔍 transcribe_clip_audio called - clip_path: {clip_path}")
    logger.info(f"{label_prefix}🔍 File exists: {os.path.exists(clip_path) if clip_path else 'No path provided'}")
    
    if not clip_path or not os.path.exists(clip_path):
        emit(f"Audio file not found for transcription: {clip_path}", True)
        logger.error(f"{label_prefix}❌ Returning empty list - file not found")
        return []

    target_language = processing_params.get("transcription_language") or \
        processing_params.get("language") or "auto"
    model_size = processing_params.get("transcription_model_size") or \
        processing_params.get("whisper_model_size") or "base"
    enable_cache = processing_params.get("enable_transcription_cache", True)
    
    logger.info(f"{label_prefix}🔍 Language: {target_language}, Model: {model_size}, Cache: {enable_cache}")

    # Attempt to load from cache first
    if enable_cache and cache_manager:
        try:
            cached = cache_manager.get_cached_transcription(
                audio_path=clip_path,
                model_size=model_size,
                language_code=None if target_language == "auto" else target_language
            )
            if cached:
                cached_text, cached_segments = cached
                segments = [
                    seg for seg in
                    (_normalize_transcription_segment(s) for s in cached_segments)
                    if seg
                ]
                if segments:
                    emit(f"Trascrizione caricata da cache ({len(segments)} segmenti)", False)
                    return segments
                else:
                    emit("Trascrizione cache priva di segmenti validi, procedo con nuova trascrizione", True)
        except Exception as cache_error:
            emit(f"Errore durante lettura cache trascrizioni: {cache_error}", True)

    processor = _get_audio_processor()
    logger.info(f"{label_prefix}🔍 Attempting to load Whisper model '{model_size}'")
    
    if not processor.load_whisper_model(model_size):
        emit(f"Impossibile caricare il modello Whisper '{model_size}'", True)
        logger.error(f"{label_prefix}❌ Failed to load Whisper model")
        return []
    
    logger.info(f"{label_prefix}✅ Whisper model loaded successfully")
    emit("Trascrizione audio in corso...", False)
    
    try:
        logger.info(f"{label_prefix}🔍 Starting transcription...")
        result = processor.transcribe_audio(
            audio_path=clip_path,
            language=target_language,
            status_callback=lambda msg: emit(msg, False)
        )
        logger.info(f"{label_prefix}🔍 Transcription completed, result: {result is not None}")
    except Exception as transcription_error:
        emit(f"Errore durante la trascrizione: {transcription_error}", True)
        logger.error("Transcription failure for %s: %s", clip_path, transcription_error, exc_info=True)
        return []

    if not result or not result.get("segments"):
        emit("Trascrizione vuota o non riuscita", True)
        logger.warning(f"{label_prefix}⚠️ Transcription result empty or no segments")
        return []

    segments = [
        seg for seg in
        (_normalize_transcription_segment(s) for s in result.get("segments", []))
        if seg and seg.text
    ]

    if not segments:
        emit("Trascrizione completata ma senza segmenti utili", True)
        return []

    emit(f"Trascrizione completata ({len(segments)} segmenti)", False)

    if enable_cache and cache_manager:
        try:
            cache_manager.cache_transcription(
                audio_path=clip_path,
                text=result.get("text", ""),
                segments=_serialize_transcription_segments(segments),
                model_size=model_size,
                language_code=None if target_language == "auto" else target_language
            )
        except Exception as cache_error:
            emit(f"Impossibile salvare la trascrizione in cache: {cache_error}", True)

    return segments


def trascrivi_audio_parallel(
    audio_file_path: str,
    num_chunks: int = 10,
    model_size: str = "small",
    beam_size: int = 5,
    language_code: Optional[str] = None,
    timeout_per_chunk: float = 60.0,
    timeout_buffer: float = 120.0
) -> tuple[str, list, str]:
    """
    Trascrive un file audio in parallelo utilizzando faster-whisper.
    
    Args:
        audio_file_path: Percorso del file audio.
        num_chunks: Numero di parti in cui dividere l'audio.
        model_size: Dimensione del modello Whisper (es. "small", "medium", "large-v2").
        beam_size: Dimensione del beam search per la decodifica.
        language_code: Codice della lingua (es. "it", "en"). Se None, viene rilevato automaticamente.
        timeout_per_chunk: Timeout in secondi per ogni chunk di trascrizione (default: 60.0).
        timeout_buffer: Buffer aggiuntivo in secondi per il timeout totale (default: 120.0).

    Returns:
        Una tupla contenente:
        - Il testo completo della trascrizione.
        - Una lista di dizionari, dove ogni dizionario rappresenta un segmento 
          con 'start', 'end', e 'text'.
        - La lingua rilevata automaticamente (se language_code=None).
    """
    if not os.path.exists(audio_file_path):
        raise FileNotFoundError(f"File audio non trovato: {audio_file_path}")

    logger.info(f"Inizio trascrizione per: {os.path.basename(audio_file_path)}")
    logger.info(f"Modello: {model_size}, Beam Size: {beam_size}, Lingua: {language_code or 'Auto-detect'}")

    # Inizializza il modello Whisper
    device = "cuda" if torch.cuda.is_available() else "cpu"
    compute_type = "float16" if device == "cuda" else "int8"
    
    try:
        model = WhisperModel(model_size, device=device, compute_type=compute_type)
    except Exception as e:
        logger.error(f"Errore durante il caricamento del modello Whisper: {e}")
        raise

    # Carica l'audio con pydub
    try:
        audio = AudioSegment.from_file(audio_file_path)
        duration_ms = len(audio)
        logger.info(f"Durata audio: {duration_ms / 1000:.2f} secondi.")
    except Exception as e:
        logger.error(f"Errore durante il caricamento del file audio con pydub: {e}")
        raise

    # Suddivide l'audio in chunk
    chunk_length_ms = duration_ms // num_chunks
    chunks = []
    for i in range(num_chunks):
        start_ms = i * chunk_length_ms
        end_ms = (i + 1) * chunk_length_ms if i < num_chunks - 1 else duration_ms
        chunk = audio[start_ms:end_ms]
        chunks.append({
            "id": i,
            "start_ms": start_ms,
            "end_ms": end_ms,
            "audio_chunk": chunk
        })

    def transcribe_chunk_fn(chunk_info):
        chunk_id = chunk_info["id"]
        start_ms = chunk_info["start_ms"]
        audio_chunk = chunk_info["audio_chunk"]
        
        # Salva il chunk temporaneamente
        temp_chunk_path = f"temp_chunk_{chunk_id}.wav"
        try:
            audio_chunk.export(temp_chunk_path, format="wav")
            
            # Trascrivi il chunk
            segments, info = model.transcribe(
                temp_chunk_path,
                beam_size=beam_size,
                language=language_code
            )
            
            chunk_segments = []
            for segment in segments:
                chunk_segments.append({
                    "start": segment.start + (start_ms / 1000.0),
                    "end": segment.end + (start_ms / 1000.0),
                    "text": segment.text.strip()
                })
            
            return chunk_id, chunk_segments, info.language
            
        finally:
            # Rimuovi il file temporaneo
            if os.path.exists(temp_chunk_path):
                os.remove(temp_chunk_path)

    # Esegui la trascrizione in parallelo
    all_segments = [None] * num_chunks
    detected_languages = []
    max_workers = min(num_chunks, os.cpu_count() or 1)

    with concurrent.futures.ThreadPoolExecutor(max_workers=max_workers) as executor:
        future_to_chunk_id_map = {
            executor.submit(transcribe_chunk_fn, chunk_info): chunk_info["id"]
            for chunk_info in chunks
        }

        for future_obj in concurrent.futures.as_completed(future_to_chunk_id_map):
            try:
                completed_chunk_id = future_to_chunk_id_map[future_obj]
                _chunk_idx, segments, lang = future_obj.result()
                all_segments[completed_chunk_id] = segments
                if lang:
                    detected_languages.append(lang)
            except Exception as e:
                logger.error(f"Errore nel future del chunk: {e}")

    # Unisci e ordina i segmenti
    final_segments = []
    for segment_list in all_segments:
        if segment_list:
            final_segments.extend(segment_list)
    
    # Ordina per tempo di inizio
    final_segments.sort(key=lambda x: x['start'])

    # Unisci il testo completo
    full_text = " ".join([seg['text'] for seg in final_segments])
    
    # Determina la lingua più probabile
    final_lang = max(set(detected_languages), key=detected_languages.count) if detected_languages else "N/A"
    
    logger.info(f"[OK] Trascrizione completata. Lingua rilevata: {final_lang}")
    logger.info(f"Testo completo: {full_text[:200]}...")

    # Rilascia memoria
    del model
    gc.collect()
    if torch.cuda.is_available():
        torch.cuda.empty_cache()
        
    return full_text, final_segments, final_lang


def detect_audio_language(audio_path: str) -> str:
    """
    Rileva la lingua di un file audio usando Whisper sui primi 30 secondi.
    
    Args:
        audio_path: Percorso del file audio
        
    Returns:
        Codice della lingua rilevata
    """
    try:
        device = "cuda" if torch.cuda.is_available() else "cpu"
        compute_type = "float16" if device == "cuda" else "int8"
        model = WhisperModel("small", device=device, compute_type=compute_type)
        
        # Trascrivi solo i primi 30 secondi per rilevare la lingua
        # Questo evita di trascrivere tutto l'audio solo per il rilevamento lingua
        segments, info = model.transcribe(
            audio_path, 
            language=None,
            initial_prompt=None,
            condition_on_previous_text=False,
            # Limita la trascrizione ai primi 30 secondi
            clip_timestamps=[(0.0, 30.0)]
        )
        
        # Consuma solo il primo segmento per ottenere le informazioni sulla lingua
        first_segment = next(segments, None)
        detected_language = info.language
        
        logger.info(f"🌍 Lingua rilevata dai primi 30s: {detected_language}")
        
        del model
        gc.collect()
        if torch.cuda.is_available():
            torch.cuda.empty_cache()
            
        return detected_language
        
    except Exception as e:
        logger.error(f"Errore nel rilevamento lingua: {e}")
        return "en"  # Fallback
    


def create_silence_audio(duration: float, output_path: str) -> bool:
    """Create a silent audio file of specified duration."""
    try:
        # Create silent audio using numpy
        sample_rate = 44100
        samples = int(duration * sample_rate)
        silence = np.zeros(samples)
        
        # Save as WAV file
        sf.write(output_path, silence, sample_rate)
        return True
        
    except Exception as e:
        logger.error(f"Error creating silence audio: {e}")
        return False


def mix_audio_files(
    audio_paths: List[str],
    output_path: str,
    volumes: Optional[List[float]] = None
) -> bool:
    """Mix multiple audio files together."""
    try:
        if not audio_paths:
            return False
        
        if len(audio_paths) == 1:
            # Just copy the single file
            import shutil
            shutil.copy2(audio_paths[0], output_path)
            return True
        
        # Try FFmpeg first for better performance
        processor = get_ffmpeg_processor()
        if processor:
            if processor.mix_audio_files(audio_paths, output_path, volumes):
                return True
            logger.warning("FFmpeg audio mixing failed, falling back to MoviePy")
        
        # Fallback to MoviePy
        if volumes is None:
            volumes = [1.0] * len(audio_paths)
        
        # Load first audio to get properties
        mixed_audio = AudioFileClip(audio_paths[0])
        if len(volumes) > 0:
            mixed_audio = mixed_audio.with_volume_scaled(volumes[0])
        
        # Mix with other audio files
        for i, audio_path in enumerate(audio_paths[1:], 1):
            if os.path.exists(audio_path):
                audio = AudioFileClip(audio_path)
                if i < len(volumes):
                    audio = audio.with_volume_scaled(volumes[i])
                
                # Ensure same duration
                min_duration = min(mixed_audio.duration, audio.duration)
                mixed_audio = mixed_audio.subclipped(0, min_duration)
                audio = audio.subclipped(0, min_duration)
                
                # Mix
                from moviepy.audio.fx import CompositeAudioClip
                mixed_audio = CompositeAudioClip([mixed_audio, audio])
                audio.close()
        
        # Save mixed audio
        mixed_audio.write_audiofile(output_path, logger=None)
        mixed_audio.close()
        return True
        
    except Exception as e:
        logger.error(f"Error mixing audio files: {e}")
        return False


def add_sound_effect(
    audio_path: str,
    effect_name: str,
    output_path: str,
    effect_volume: float = 0.25  # Reduced to match SOUND_EFFECT_CONFIG
) -> bool:
    """Add a sound effect to an audio file."""
    try:
        if effect_name not in DEFAULT_SOUND_EFFECTS:
            logger.warning(f"Sound effect '{effect_name}' not found")
            return False
        
        effect_path = DEFAULT_SOUND_EFFECTS[effect_name]
        if not os.path.exists(effect_path):
            logger.warning(f"Sound effect file not found: {effect_path}")
            return False
        
        # Load main audio and effect
        main_audio = AudioFileClip(audio_path)
        effect_audio = AudioFileClip(effect_path)
        
        # Adjust effect volume
        effect_audio = effect_audio.with_volume_scaled(effect_volume)
        
        # Ensure effect doesn't exceed main audio duration
        if effect_audio.duration > main_audio.duration:
            effect_audio = effect_audio.subclipped(0, main_audio.duration)
        
        # Mix audio
        from moviepy import CompositeAudioClip
        mixed_audio = CompositeAudioClip([main_audio, effect_audio])
        
        # Save result
        mixed_audio.write_audiofile(output_path, logger=None)
        
        # Cleanup
        main_audio.close()
        effect_audio.close()
        mixed_audio.close()
        
        return True
        
    except Exception as e:
        logger.error(f"Error adding sound effect: {e}")
        return False


def analyze_audio_volume(audio_path: str) -> Dict[str, float]:
    """Analyze volume characteristics of an audio file."""
    try:
        y, sr = librosa.load(audio_path)
        
        # Calculate various volume metrics
        rms = librosa.feature.rms(y=y)[0]
        peak = np.max(np.abs(y))
        avg_rms = np.mean(rms)
        max_rms = np.max(rms)
        
        # Convert to dB
        peak_db = 20 * np.log10(peak) if peak > 0 else -np.inf
        avg_db = 20 * np.log10(avg_rms) if avg_rms > 0 else -np.inf
        max_db = 20 * np.log10(max_rms) if max_rms > 0 else -np.inf
        
        return {
            "peak_amplitude": float(peak),
            "peak_db": float(peak_db),
            "avg_rms": float(avg_rms),
            "avg_db": float(avg_db),
            "max_rms": float(max_rms),
            "max_db": float(max_db),
            "dynamic_range_db": float(max_db - avg_db)
        }
        
    except Exception as e:
        logger.error(f"Error analyzing audio volume: {e}")
        return {}


def trim_audio(
    input_path: str,
    output_path: str,
    start_time: float = 0.0,
    end_time: Optional[float] = None
) -> bool:
    """Trim audio file to specified time range."""
    try:
        audio = AudioFileClip(input_path)
        
        if end_time is None:
            end_time = audio.duration
        
        # Ensure valid time range
        start_time = max(0, start_time)
        end_time = min(audio.duration, end_time)
        
        if start_time >= end_time:
            logger.error("Invalid time range for audio trimming")
            return False
        
        # Trim audio
        trimmed = audio.subclipped(start_time, end_time)
        trimmed.write_audiofile(output_path, logger=None)
        
        # Cleanup
        audio.close()
        trimmed.close()
        
        return True
        
    except Exception as e:
        logger.error(f"Error trimming audio: {e}")
        return False


def detect_silence_moments(
    audio_path: str,
    min_silence_duration: float = 0.5,
    silence_threshold_db: float = -40.0,
    window_size: float = 0.1
) -> List[Dict[str, float]]:
    """
    Detect silence moments in an audio file.
    
    Args:
        audio_path: Path to the audio file
        min_silence_duration: Minimum duration of silence to consider (seconds)
        silence_threshold_db: Threshold in dB below which audio is considered silent
        window_size: Size of analysis window in seconds
        
    Returns:
        List of silence periods with 'start' and 'end' times
    """
    try:
        # Validate input parameters
        if not os.path.exists(audio_path):
            logger.error(f"Audio file not found: {audio_path}")
            return []
        
        if min_silence_duration <= 0 or window_size <= 0:
            logger.error(f"Invalid parameters: min_silence_duration={min_silence_duration}, window_size={window_size}")
            return []
        
        logger.debug(f"Analyzing audio: {audio_path} (threshold: {silence_threshold_db}dB, min_duration: {min_silence_duration}s)")
        
        # Load audio with librosa
        y, sr = librosa.load(audio_path)
        
        if len(y) == 0:
            logger.warning(f"Audio file appears to be empty: {audio_path}")
            return []
        
        # Calculate RMS energy in windows
        hop_length = int(window_size * sr)
        frame_length = hop_length * 2
        
        # Calculate RMS energy
        rms = librosa.feature.rms(y=y, frame_length=frame_length, hop_length=hop_length)[0]
        
        if len(rms) == 0:
            logger.warning(f"No RMS data calculated for audio: {audio_path}")
            return []
        
        # Convert to dB
        rms_db = 20 * np.log10(rms + 1e-6)  # Add small value to avoid log(0)
        
        # Find silence frames
        silence_frames = rms_db < silence_threshold_db
        
        # Convert frame indices to time
        times = librosa.frames_to_time(np.arange(len(silence_frames)), sr=sr, hop_length=hop_length)
        
        # Group consecutive silence frames into periods
        silence_periods = []
        in_silence = False
        silence_start = 0.0
        
        for i, is_silent in enumerate(silence_frames):
            current_time = times[i]
            
            if is_silent and not in_silence:
                # Start of silence period
                silence_start = current_time
                in_silence = True
            elif not is_silent and in_silence:
                # End of silence period
                silence_duration = current_time - silence_start
                if silence_duration >= min_silence_duration:
                    silence_periods.append({
                        "start": silence_start,
                        "end": current_time,
                        "duration": silence_duration
                    })
                    logger.debug(f"Found silence: {silence_start:.2f}s-{current_time:.2f}s ({silence_duration:.2f}s)")
                in_silence = False
        
        # Handle case where audio ends in silence
        if in_silence:
            final_time = len(y) / sr
            silence_duration = final_time - silence_start
            if silence_duration >= min_silence_duration:
                silence_periods.append({
                    "start": silence_start,
                    "end": final_time,
                    "duration": silence_duration
                })
                logger.debug(f"Found final silence: {silence_start:.2f}s-{final_time:.2f}s ({silence_duration:.2f}s)")
        
        logger.info(f"Detected {len(silence_periods)} silence periods in audio")
        return silence_periods
        
    except Exception as e:
        logger.error(f"Error detecting silence moments in {audio_path}: {e}")
        logger.debug(f"Silence detection error details:", exc_info=True)
        return []


def find_sentence_boundaries(audio_path: str, transcription_segments: Optional[List[Dict]] = None) -> List[float]:
    """
    Find sentence boundaries in audio using transcription data.
    
    Args:
        audio_path: Path to the audio file
        transcription_segments: Pre-existing transcription segments
        
    Returns:
        List of sentence boundary times in seconds
    """
    try:
        # If no transcription segments provided, transcribe the audio
        if transcription_segments is None:
            processor = AudioProcessor()
            if not processor.load_whisper_model("base"):
                logger.warning("Could not load Whisper model for sentence boundary detection")
                return []
            
            result = processor.transcribe_audio(audio_path)
            if not result or "segments" not in result:
                logger.warning("Could not transcribe audio for sentence boundary detection")
                return []
            
            transcription_segments = result["segments"]
        
        sentence_boundaries = []
        
        for segment in transcription_segments:
            try:
                # Handle both object attributes and dictionary keys
                if hasattr(segment, 'end') and hasattr(segment, 'text'):
                    # Whisper segment object with attributes
                    end_time = float(segment.end)
                    text = segment.text.strip()
                elif isinstance(segment, dict) and "end" in segment and "text" in segment:
                    # Dictionary format segment
                    end_time = float(segment["end"])
                    text = segment["text"].strip()
                else:
                    # Skip malformed segments
                    logger.debug(f"Skipping malformed segment: {segment}")
                    continue
                
                # Check if segment ends with sentence-ending punctuation
                if text.endswith(('.', '!', '?', ':', ';')):
                    sentence_boundaries.append(end_time)
                    
            except (ValueError, TypeError, AttributeError) as e:
                logger.debug(f"Error processing segment {segment}: {e}")
                continue
        
        # Sort boundaries by time
        sentence_boundaries.sort()
        
        logger.info(f"Found {len(sentence_boundaries)} sentence boundaries")
        return sentence_boundaries
        
    except Exception as e:
        logger.error(f"Error finding sentence boundaries: {e}")
        return []


def create_artificial_silence(
    audio_path: str,
    output_path: str,
    insertion_time: float,
    silence_duration: float = 1.0
) -> bool:
    """
    Create artificial silence in audio by inserting a silence gap.
    
    Args:
        audio_path: Path to the original audio file
        output_path: Path to save the modified audio
        insertion_time: Time where to insert silence (seconds)
        silence_duration: Duration of silence to insert (seconds)
        
    Returns:
        True if successful, False otherwise
    """
    try:
        # Load original audio
        audio = AudioFileClip(audio_path)
        
        # Ensure insertion time is valid
        insertion_time = max(0.0, min(insertion_time, audio.duration))
        
        # Split audio at insertion point
        if insertion_time <= 0:
            # Insert at beginning
            silence_clip = AudioFileClip("silence", duration=silence_duration)
            final_audio = silence_clip.concatenate_audioclips([audio])
        elif insertion_time >= audio.duration:
            # Insert at end
            silence_clip = AudioFileClip("silence", duration=silence_duration)
            final_audio = audio.concatenate_audioclips([silence_clip])
        else:
            # Insert in middle
            before_clip = audio.subclipped(0, insertion_time)
            after_clip = audio.subclipped(insertion_time)
            silence_clip = AudioFileClip("silence", duration=silence_duration)
            
            final_audio = before_clip.concatenate_audioclips([silence_clip, after_clip])
            
            before_clip.close()
            after_clip.close()
            silence_clip.close()
        
        # Save modified audio
        final_audio.write_audiofile(output_path, logger=None)
        
        # Cleanup
        audio.close()
        final_audio.close()
        
        return True
        
    except Exception as e:
        logger.error(f"Error creating artificial silence: {e}")
        return False


def find_optimal_clip_insertion_times(
    voiceover_duration: float,
    num_clips: int,
    silence_periods: List[Dict[str, float]],
    min_distance_between_clips: float = 30.0,
    preference_window: float = 15.0,
    audio_path: Optional[str] = None
) -> List[float]:
    """
    Find optimal times to insert clips, creating artificial silences when natural ones aren't found.
    
    Args:
        voiceover_duration: Total duration of voiceover in seconds
        num_clips: Number of clips to insert
        silence_periods: List of detected natural silence periods
        min_distance_between_clips: Minimum distance between clips in seconds
        preference_window: Window around mathematical positions to prefer silences
        audio_path: Path to audio file for sentence boundary detection
        
    Returns:
        List of optimal insertion times in seconds
    """
    if num_clips <= 0:
        return []
    
    if voiceover_duration <= 0:
        logger.error(f"Invalid voiceover duration: {voiceover_duration}")
        return []
    
    try:
        # Calculate mathematical insertion times as baseline
        mathematical_times = []
        segment_duration = voiceover_duration / (num_clips + 1)
        
        for i in range(1, num_clips + 1):
            math_time = i * segment_duration
            mathematical_times.append(math_time)
        
        logger.info(f"Mathematical insertion times: {[f'{t:.1f}s' for t in mathematical_times]}")
        
        optimal_times = []
        used_silences = set()
        
        # Validate silence_periods input
        if not isinstance(silence_periods, list):
            logger.warning(f"Invalid silence_periods type: {type(silence_periods)}, using mathematical times only")
            silence_periods = []
        
        # Try to find natural silences near mathematical positions
        for math_time in mathematical_times:
            best_silence = None
            best_distance = float('inf')
            
            # Look for silences within preference window
            for i, silence in enumerate(silence_periods):
                if i in used_silences:
                    continue
                
                try:
                    # Validate silence period structure
                    if not isinstance(silence, dict) or "start" not in silence or "end" not in silence:
                        logger.debug(f"Skipping malformed silence period: {silence}")
                        continue
                    
                    silence_center = (float(silence["start"]) + float(silence["end"])) / 2
                    distance = abs(silence_center - math_time)
                    
                    if distance <= preference_window and distance < best_distance:
                        # Check minimum distance from already selected times
                        too_close = False
                        for existing_time in optimal_times:
                            if abs(silence_center - existing_time) < min_distance_between_clips:
                                too_close = True
                                break
                        
                        if not too_close:
                            best_silence = i
                            best_distance = distance
                            
                except (ValueError, TypeError, KeyError) as e:
                    logger.debug(f"Error processing silence period {silence}: {e}")
                    continue
            
            if best_silence is not None:
                # Use natural silence
                try:
                    silence = silence_periods[best_silence]
                    silence_center = (float(silence["start"]) + float(silence["end"])) / 2
                    optimal_times.append(silence_center)
                    used_silences.add(best_silence)
                    logger.info(f"Using natural silence at {silence_center:.1f}s for clip insertion")
                except (ValueError, TypeError, KeyError) as e:
                    logger.warning(f"Error using selected silence: {e}, falling back to mathematical time")
                    optimal_times.append(math_time)
                    logger.info(f"No suitable silence found, using mathematical time {math_time:.1f}s")
            else:
                # No suitable natural silence found, create artificial one
                artificial_time = math_time
                
                # Try to find nearest sentence boundary if audio path provided
                if audio_path and os.path.exists(audio_path):
                    try:
                        sentence_boundaries = find_sentence_boundaries(audio_path)
                        if sentence_boundaries:
                            # Find closest sentence boundary to mathematical time
                            closest_boundary = min(sentence_boundaries, 
                                                 key=lambda x: abs(x - math_time))
                            
                            # Use sentence boundary if it's within reasonable distance
                            if abs(closest_boundary - math_time) <= preference_window * 2:
                                # Check minimum distance from already selected times
                                too_close = False
                                for existing_time in optimal_times:
                                    if abs(closest_boundary - existing_time) < min_distance_between_clips:
                                        too_close = True
                                        break
                                
                                if not too_close:
                                    artificial_time = closest_boundary
                                    logger.info(f"Creating artificial silence at sentence boundary {artificial_time:.1f}s")
                                else:
                                    logger.info(f"Sentence boundary {closest_boundary:.1f}s too close to existing clips, using mathematical time {artificial_time:.1f}s")
                            else:
                                logger.info(f"No suitable sentence boundary found, using mathematical time {artificial_time:.1f}s")
                        else:
                            logger.info(f"No sentence boundaries detected, using mathematical time {artificial_time:.1f}s")
                    except Exception as e:
                        logger.warning(f"Error finding sentence boundaries: {e}, using mathematical time")
                        artificial_time = math_time
                
                optimal_times.append(artificial_time)
                logger.info(f"No suitable silence found, using mathematical time {artificial_time:.1f}s")
        
        # Sort times to ensure proper order and validate results
        optimal_times.sort()
        
        # Validate that all times are within valid range
        valid_times = []
        for time in optimal_times:
            if 0 <= time <= voiceover_duration:
                valid_times.append(time)
            else:
                logger.warning(f"Skipping invalid insertion time: {time:.1f}s (duration: {voiceover_duration:.1f}s)")
        
        if len(valid_times) != len(optimal_times):
            logger.warning(f"Some insertion times were invalid, using {len(valid_times)} out of {len(optimal_times)} times")
        
        logger.info(f"Final optimal insertion times: {[f'{t:.1f}s' for t in valid_times]}")
        return valid_times
        
    except Exception as e:
        logger.error(f"Error finding optimal clip insertion times: {e}")
        logger.debug("Optimal insertion error details:", exc_info=True)
        # Fallback to mathematical positioning
        try:
            fallback_times = []
            segment_duration = voiceover_duration / (num_clips + 1)
            for i in range(1, num_clips + 1):
                fallback_times.append(i * segment_duration)
            logger.info(f"Using fallback mathematical times: {[f'{t:.1f}s' for t in fallback_times]}")
            return fallback_times
        except Exception as fallback_error:
            logger.error(f"Even fallback positioning failed: {fallback_error}")
            return []


def slide_in(
    clip: ImageClip,
    duration: float,
    side: str = "right",
    frame_size: Tuple[int,int] = None
) -> ImageClip:
    """
    Slide‐in che parte dal centro del frame.
    - clip: ImageClip da animare
    - duration: durata del movimento
    - side: 'left' o 'right' (definisce se fa un piccolissimo spostamento verso sinistra o destra)
    - frame_size: (video_w, video_h), obbligatorio per centrare
    """
    # Validate inputs
    if clip is None:
        raise ValueError("clip cannot be None")
    
    if not hasattr(clip, 'size') or not hasattr(clip, 'duration'):
        raise ValueError("clip must be a valid MoviePy clip with size and duration attributes")
    
    if frame_size is None:
        raise ValueError("Devi passare frame_size=(video_w, video_h)")
    
    video_w, video_h = frame_size
    clip_w, clip_h = clip.size

    # centro esatto del frame
    final_x = (video_w - clip_w) / 2
    final_y = (video_h - clip_h) / 2

    # da che lato "effettivamente" vogliamo spostarci?
    # qui partiamo dal centro e facciamo un piccolo movimento
    offset = clip_w * 0.2  # per esempio il 20% della larghezza del clip
    if side == "right":
        start_x = final_x - offset
    elif side == "left":
        start_x = final_x + offset
    else:
        raise ValueError("side deve essere 'left' o 'right'")

    def position(t):
        # t ∈ [0, duration]
        prog = min(max(t / duration, 0), 1)
        current_x = start_x + (final_x - start_x) * prog
        return (current_x, final_y)

    return (
        clip
        .with_position(position)
        .with_duration(clip.duration)
    )


def zoom_in(
    clip: ImageClip,
    duration: float = 0.3,
    frame_size: Tuple[int, int] = None,
    start_scale: float = 0.3,
    end_scale: float = 1.0,
    start_x: int = 50,
    start_y: int = None
) -> ImageClip:
    """
    Zoom in animation veloce dalla posizione iniziale (non dal centro).
    - clip: ImageClip da animare
    - duration: durata del zoom
    - frame_size: (video_w, video_h), obbligatorio per centrare
    - start_scale: scala iniziale (default 0.3)
    - end_scale: scala finale (default 1.0)
    - start_x: posizione x iniziale (default 50 per sinistra)
    - start_y: posizione y iniziale (se None, calcolata in basso)
    """
    from moviepy import VideoClip
    import numpy as np
    from PIL import Image
    
    if clip is None:
        raise ValueError("clip cannot be None")
    
    if not hasattr(clip, 'size') or not hasattr(clip, 'duration'):
        raise ValueError("clip must be a valid MoviePy clip with size and duration attributes")
    
    if frame_size is None:
        raise ValueError("Devi passare frame_size=(video_w, video_h)")
    
    video_w, video_h = frame_size
    clip_w, clip_h = clip.size
    
    # Posizione finale (già nella posizione corretta, sinistra in basso)
    if start_y is None:
        # Calcola y in basso
        bottom_margin = 100
        estimated_height = clip_h
        start_y = video_h - bottom_margin - estimated_height
    
    final_x = start_x
    final_y = max(0, int(start_y))  # Assicura che sia positivo
    
    # Ottieni fps dal clip se disponibile, altrimenti usa default
    fps = getattr(clip, 'fps', 24.0)

    # Conserva eventuale maschera di trasparenza del clip originale
    original_mask = getattr(clip, "mask", None)
    
    def make_frame(t):
        if t < duration:
            progress = min(max(t / duration, 0), 1)
            # Zoom da start_scale a end_scale
            scale = start_scale + (progress * (end_scale - start_scale))
            # Calcola nuova dimensione
            new_w = int(clip_w * scale)
            new_h = int(clip_h * scale)
            # Mantieni la posizione iniziale (sinistra in basso)
            # L'angolo in basso a sinistra rimane fisso - calcola y dal basso
            bottom_y = final_y + clip_h  # Posizione del bordo inferiore originale
            y = bottom_y - new_h  # Mantieni il bordo inferiore fisso
            y = max(0, int(y))  # Assicura che sia positivo e dentro il frame
            x = int(final_x)
            
            # Ottieni frame originale
            frame = clip.get_frame(t)
            # Applica zoom
            pil_img = Image.fromarray(frame)
            pil_img = pil_img.resize((new_w, new_h), Image.LANCZOS)
            # Crea nuovo frame
            new_frame = np.zeros((video_h, video_w, 3), dtype=np.uint8)
            # Assicura che l'immagine sia dentro i bordi
            y_end = min(y + new_h, video_h)
            x_end = min(x + new_w, video_w)
            if y >= 0 and x >= 0 and y < video_h and x < video_w:
                img_array = np.array(pil_img)
                # Taglia se necessario
                if y + new_h > video_h:
                    img_array = img_array[:video_h - y, :]
                if x + new_w > video_w:
                    img_array = img_array[:, :video_w - x]
                new_frame[y:y_end, x:x_end] = img_array
            return new_frame
        else:
            # Frame normale dopo l'animazione
            return clip.get_frame(t)
    
    result_clip = VideoClip(make_frame, duration=clip.duration)

    # Se il clip originale ha una mask (trasparenza), applica lo stesso zoom anche alla mask,
    # così lo sfondo rimane visibile dove non c'è testo.
    if original_mask is not None:
        def make_mask_frame(t):
            if t < duration:
                progress = min(max(t / duration, 0), 1)
                scale = start_scale + (progress * (end_scale - start_scale))
                new_w = int(clip_w * scale)
                new_h = int(clip_h * scale)

                bottom_y = final_y + clip_h
                y = bottom_y - new_h
                y = max(0, int(y))
                x = int(final_x)

                mask_frame = original_mask.get_frame(t)
                # mask_frame è 2D float [0,1]
                pil_mask = Image.fromarray((mask_frame * 255).astype(np.uint8))
                pil_mask = pil_mask.resize((new_w, new_h), Image.LANCZOS)

                new_mask = np.zeros((video_h, video_w), dtype=np.float32)
                y_end = min(y + new_h, video_h)
                x_end = min(x + new_w, video_w)
                if y >= 0 and x >= 0 and y < video_h and x < video_h:
                    mask_array = np.array(pil_mask).astype(np.float32) / 255.0
                    if y + new_h > video_h:
                        mask_array = mask_array[:video_h - y, :]
                    if x + new_w > video_w:
                        mask_array = mask_array[:, :video_w - x]
                    new_mask[y:y_end, x:x_end] = mask_array
                return new_mask
            else:
                return original_mask.get_frame(t)

        mask_clip = VideoClip(make_mask_frame, duration=clip.duration, ismask=True)
        # Allinea fps della mask
        mask_clip = mask_clip.with_fps(fps)
        result_clip = result_clip.with_mask(mask_clip)

    # Imposta fps se disponibile, altrimenti usa default
    if hasattr(clip, 'fps') and clip.fps:
        result_clip = result_clip.with_fps(clip.fps)
    else:
        result_clip = result_clip.with_fps(24.0)  # Default fps

    return result_clip
