"""Audio processing functions for video generation."""

import logging
import os
import subprocess
import tempfile
import uuid
from typing import List, Optional, Tuple, Dict, Any, Callable

import numpy as np
from moviepy import AudioFileClip, VideoFileClip, CompositeAudioClip
from pydub import AudioSegment
from faster_whisper import WhisperModel
import pysrt
from modules.video.translation_fallback import translate_texts_google_with_fallback

# Global Whisper model instances
whisper_model = None
_whisper_lang = None

try:
    from config import SUBTITLE_BURNIN_TIMEOUT_DEFAULT
    from video_core import safe_close_moviepy_object, create_safe_temp_filename
    from video_ffmpeg import get_audio_duration_ffmpeg, get_audio_codec_ffmpeg
    from modules.video.video_generation import get_clip_duration, run_ffmpeg_command , generate_stock_segment
except ImportError:
    # Fallback values if modules are not available
    SUBTITLE_BURNIN_TIMEOUT_DEFAULT = 300  # Increased from 30 to 300 seconds (5 minutes) to prevent subtitle timeout
    
    def safe_close_moviepy_object(obj):
        if hasattr(obj, 'close'):
            obj.close()
    
    def create_safe_temp_filename(prefix="temp", suffix=".tmp"):
        return f"{prefix}_{uuid.uuid4()}{suffix}"
    
    def get_audio_duration_ffmpeg(path):
        return 0.0
    
    def get_audio_codec_ffmpeg(path):
        return "unknown"
    
def generate_stock_segment(*args, **kwargs):
    # Lazy import to avoid circular dependency; raises if real function unavailable
    from modules.video.video_generation import generate_stock_segment as _real_generate_stock_segment
    return _real_generate_stock_segment(*args, **kwargs)

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

try:
    from transition_downloader import get_all_available_transitions
except ImportError:
    get_all_available_transitions = None  # type: ignore

logger = logging.getLogger(__name__)

# Global Whisper model instance
whisper_model = None

AVAILABLE_TRANSITIONS: List[str] = []

# Use a uniform output sample rate so that concatenated segments share identical audio specs.
# Up-sampling from 24 kHz to 44.1 kHz preserves pitch while avoiding concat copy issues.
PREFERRED_OUTPUT_SAMPLE_RATE = 44100


def _unique_existing_paths(paths: List[str]) -> List[str]:
    unique: List[str] = []
    seen: set[str] = set()
    for path in paths:
        if not path or not os.path.exists(path):
            continue
        name = os.path.basename(path)
        if name in seen:
            continue
        unique.append(path)
        seen.add(name)
    return unique


def _load_transitions(force_refresh: bool) -> List[str]:
    if get_all_available_transitions is None:
        return []
    try:
        return get_all_available_transitions(force_refresh=force_refresh)
    except TypeError:
        try:
            return get_all_available_transitions()
        except TypeError:
            return []

def _get_cached_transitions(force_refresh: bool = False) -> List[str]:
    global AVAILABLE_TRANSITIONS
    if get_all_available_transitions is None:
        return []

    if force_refresh:
        AVAILABLE_TRANSITIONS = []

    if AVAILABLE_TRANSITIONS:
        AVAILABLE_TRANSITIONS = _unique_existing_paths(AVAILABLE_TRANSITIONS)

    if not AVAILABLE_TRANSITIONS:
        try:
            AVAILABLE_TRANSITIONS = _unique_existing_paths(
                _load_transitions(force_refresh=False)
            )
        except Exception as e:
            logger.warning(f"[Transitions] Unable to load cached transitions: {e}")
            AVAILABLE_TRANSITIONS = []

    if not AVAILABLE_TRANSITIONS:
        try:
            AVAILABLE_TRANSITIONS = _unique_existing_paths(
                _load_transitions(force_refresh=True)
            )
        except Exception as e:
            logger.error(f"[Transitions] Unable to download transitions: {e}")
            AVAILABLE_TRANSITIONS = []

    return AVAILABLE_TRANSITIONS

def _prepend_random_transition_to_stock(
    stock_segment_path: str,
    temp_dir: str,
    status_callback: Callable[[str, bool], None],
    base_w: int,
    base_h: int,
    base_fps: float,
    base_duration: float,
    transition_duration: float = 0.5,
) -> Tuple[str, float]:
    """Prepend a cached transition clip to the stock segment if available."""
    transitions = _get_cached_transitions()
    if not transitions:
        status_callback("[Transitions] Nessuna transizione disponibile, skip.", True)
        return stock_segment_path, 0.0

    import random
    chosen_transition = random.choice(transitions)
    status_callback(f"[Transitions] Applicazione transizione '{os.path.basename(chosen_transition)}'", False)

    trimmed_transition_path = os.path.join(temp_dir, f"transition_intro_{uuid.uuid4().hex[:8]}.mp4")
    combined_path = os.path.join(temp_dir, f"transition_prefixed_{uuid.uuid4().hex[:8]}.mp4")

    scale_filter = (
        f"scale={base_w}:{base_h}:force_original_aspect_ratio=decrease,"
        f"pad={base_w}:{base_h}:(ow-iw)/2:(oh-ih)/2"
    )

    trim_cmd = [
        "ffmpeg",
        "-y",
        "-i",
        chosen_transition,
        "-t",
        f"{transition_duration:.3f}",
        "-filter_complex",
        f"[0:v]{scale_filter},fps={base_fps}[v];anullsrc=channel_layout=stereo:sample_rate=48000[a]",
        "-map",
        "[v]",
        "-map",
        "[a]",
        "-c:v",
        "libx264",

        "-preset",
        "fast",
        "-crf",
        "18",
        "-pix_fmt",
        "yuv420p",
        "-shortest",
        trimmed_transition_path,
    ]

    trim_result = subprocess.run(trim_cmd, capture_output=True, text=True)
    if trim_result.returncode != 0 or not os.path.exists(trimmed_transition_path):
        status_callback(f"[Transitions] Errore preparazione transizione: {trim_result.stderr.strip()}", True)
        return stock_segment_path, 0.0

    # Determina se il video stock ha audio
    probe_cmd = [
        'ffprobe', '-v', 'error', '-select_streams', 'a:0',
        '-show_entries', 'stream=index', '-of', 'csv=p=0', stock_segment_path
    ]
    probe_result = subprocess.run(probe_cmd, capture_output=True, text=True)
    base_has_audio = probe_result.returncode == 0 and bool(probe_result.stdout.strip())

    if base_has_audio:
        # Concat filter requires all video streams first, then all audio streams
        # Format: [v0][v1]...[a0][a1]...concat=n=N:v=1:a=1[outv][outa]
        filter_concat = (
            f"[0:v]{scale_filter},fps={base_fps},setsar=1[v0];"
            f"[1:v]{scale_filter},fps={base_fps},setsar=1[v1];"
            '[0:a]aresample=48000[a0];'
            '[1:a]aresample=48000[a1];'
            '[v0][v1][a0][a1]concat=n=2:v=1:a=1[v][a]'
        )
    else:
        filter_concat = (
            f"[0:v]{scale_filter},fps={base_fps},setsar=1[v0];"
            f"[1:v]{scale_filter},fps={base_fps},setsar=1[v1];"
            '[0:a]aresample=48000[a0];'
            f"anullsrc=channel_layout=stereo:sample_rate=48000,atrim=0:{base_duration:.3f}[a1];"
            '[v0][v1][a0][a1]concat=n=2:v=1:a=1[v][a]'
        )

    concat_cmd = [
        'ffmpeg',
        '-y',
        '-i',
        trimmed_transition_path,
        '-i',
        stock_segment_path,
        '-filter_complex',
        filter_concat,
        '-map',
        '[v]',
        '-map',
        '[a]',
        '-c:v',
        'libx264',
        '-preset',
        'fast',
        '-crf',
        '18',
        '-pix_fmt',
        'yuv420p',
        '-c:a',
        'aac',
        '-b:a',
        '192k',
        combined_path,
    ]

    concat_result = subprocess.run(concat_cmd, capture_output=True, text=True)

    try:
        if os.path.exists(trimmed_transition_path):
            os.remove(trimmed_transition_path)
    except Exception:
        pass

    if concat_result.returncode != 0 or not os.path.exists(combined_path):
        status_callback(f"[Transitions] Errore concatenazione: {concat_result.stderr.strip()}", True)
        return stock_segment_path, 0.0

    try:
        os.remove(stock_segment_path)
    except Exception:
        pass

    status_callback('[Transitions] Transizione iniziale applicata.', False)
    return combined_path, transition_duration





def get_whisper_model():
    """Get or initialize the Whisper model."""
    global whisper_model
    if whisper_model is None:
        try:
            whisper_model = WhisperModel("base", device="cpu", compute_type="int8")
            logger.info("Whisper model initialized successfully")
        except Exception as e:
            logger.error(f"Error initializing Whisper model: {e}")
            whisper_model = None
    return whisper_model


def get_whisper_language_model():
    """Get or initialize the Whisper language detection model."""
    global _whisper_lang
    if _whisper_lang is None:
        try:
            _whisper_lang = WhisperModel("small", device="cpu", compute_type="int8")
            logger.info("Whisper language detection model initialized successfully")
        except Exception as e:
            logger.error(f"Error initializing Whisper language detection model: {e}")
            _whisper_lang = None
    return _whisper_lang


def detect_clip_language_safe(
    clip_path: str,
    status_callback: Callable[[str], None],
    main_label: str
) -> Optional[str]:
    """Rileva la lingua parlata in una clip (primi 10s), o ritorna None in caso di errore."""
    tmp_audio = clip_path + ".langdet.wav"
    try:
        # estraiamo i primi 10 secondi di audio in WAV
        import subprocess
        subprocess.run(
            ["ffmpeg", "-y", "-loglevel", "error",
             "-i", clip_path,
             "-t", "10", "-vn",
             "-acodec", "pcm_s16le", tmp_audio],
            check=True,
        )
        # language detection using transcription
        model = get_whisper_language_model()
        if model is None:
            status_callback(f"{main_label} ⚠️ Language detection model not available")
            return None
            
        # Use transcribe method and get language from info object
        segments, info = model.transcribe(tmp_audio, language=None)
        lang = info.language
        os.remove(tmp_audio)
        return lang
    except Exception as e:
        status_callback(f"{main_label} ⚠️ Language detection fallita per {os.path.basename(clip_path)}: {e}")
        try:
            if os.path.exists(tmp_audio):
                os.remove(tmp_audio)
        except OSError:
            pass
        return None


def transcribe_audio_with_whisper(
    audio_path: str,
    language: Optional[str] = None,
    status_callback: Optional[Callable[[str], None]] = None
) -> List[Dict[str, Any]]:
    """Transcribe audio using Whisper model."""
    try:
        if not os.path.exists(audio_path):
            logger.error(f"Audio file not found: {audio_path}")
            return []
        
        if status_callback:
            status_callback("Transcribing audio with Whisper...")
        
        # Get Whisper model
        model = get_whisper_model()
        if not model:
            logger.error("Whisper model not available")
            return []
        
        # Auto-detect language if not provided
        if not language:
            # Create a dummy callback for language detection
            def dummy_callback(msg):
                pass
            detected_language = detect_clip_language_safe(audio_path, dummy_callback, "Audio Transcription")
            if detected_language:
                language = detected_language
        
        # Create a robust transcription with multiple attempts and audio preprocessing
        audio_properties = analyze_audio_properties(audio_path)
        if not audio_properties or audio_properties.get('duration', 0) < 0.5:
            logger.warning(f"Audio file {audio_path} appears to be empty or too short")
            if status_callback:
                status_callback("Audio file is empty or too short for transcription")
            return []

        # Check if audio has detectable speech (simple RMS check)
        if audio_properties.get('db_level', -np.inf) < -50:
            logger.warning(f"Audio file {audio_path} appears to be silent (RMS: {audio_properties.get('db_level', 'N/A')} dB)")
            if status_callback:
                status_callback("Audio appears to be silent or background noise only")
            return [{"start": 0.0, "end": audio_properties.get('duration', 0), "text": "[MUSIC/BACKGROUND]", "words": []}]

        # Try transcription with improved parameters
        try:
            segments, info = model.transcribe(
                audio_path,
                language=language,
                task="transcribe",
                vad_filter=True,
                vad_parameters=dict(min_silence_duration_ms=500),
                word_timestamps=True,
                temperature=[0.0, 0.2, 0.4],  # Multiple temperatures for robustness
                best_of=3,  # Consider top 3 hypotheses
                no_speech_threshold=0.3,  # Lower threshold to catch potential speech
                condition_on_previous_text=False,  # Start fresh
                initial_prompt=""  # No initial prompt to avoid bias
            )
        except Exception as transcription_error:
            # Fallback transcription attempt with simpler parameters
            logger.warning(f"Initial transcription failed for {audio_path}: {transcription_error}")
            if status_callback:
                status_callback("Initial transcription failed, trying fallback method...")

            try:
                segments, info = model.transcribe(
                    audio_path,
                    language=language,
                    task="transcribe",
                    vad_filter=False,
                    no_speech_threshold=0.5,
                    temperature=0.0
                )
            except Exception as fallback_error:
                logger.error(f"Fallback transcription also failed for {audio_path}: {fallback_error}")
                return []
        
        # Convert segments to list of dictionaries
        transcription_segments = []
        
        for segment in segments:
            segment_dict = {
                'start': segment.start,
                'end': segment.end,
                'text': segment.text.strip(),
                'words': []
            }
            
            # Add word-level timestamps if available
            if hasattr(segment, 'words') and segment.words:
                for word in segment.words:
                    word_dict = {
                        'start': word.start,
                        'end': word.end,
                        'word': word.word,
                        'probability': word.probability
                    }
                    segment_dict['words'].append(word_dict)
            
            transcription_segments.append(segment_dict)
        
        if status_callback:
            status_callback(f"Transcription completed: {len(transcription_segments)} segments")
        
        logger.info(f"Transcribed {len(transcription_segments)} segments from audio")
        return transcription_segments
        
    except Exception as e:
        logger.error(f"Error transcribing audio: {e}")
        if status_callback:
            status_callback(f"Transcription error: {str(e)}")
        return []


def translate_text_segments(
    segments: List[Dict[str, Any]],
    target_language: str = "it",
    source_language: str = "auto",
    status_callback: Optional[Callable[[str], None]] = None
) -> List[Dict[str, Any]]:
    """Translate text segments to target language."""
    try:
        if not segments:
            return []
        
        if status_callback:
            status_callback(f"Translating {len(segments)} segments to {target_language}...")
        
        translated_segments = []
        texts = [seg.get("text", "") for seg in segments]
        translated_texts = translate_texts_google_with_fallback(
            texts=texts,
            source_language=source_language,
            target_language=target_language,
            status_callback=status_callback,
        )

        for i, segment in enumerate(segments):
            original_text = segment.get("text", "")
            translated_text = translated_texts[i] if i < len(translated_texts) else original_text
            translated_segment = segment.copy()
            translated_segment["text"] = translated_text
            translated_segment["original_text"] = original_text
            translated_segments.append(translated_segment)
            if status_callback and (i + 1) % 10 == 0:
                status_callback(f"Translated {i + 1}/{len(segments)} segments")

        if status_callback:
            status_callback(f"Translation completed: {len(translated_segments)} segments")
        
        return translated_segments
        
    except Exception as e:
        logger.error(f"Error translating segments: {e}")
        if status_callback:
            status_callback(f"Translation error: {str(e)}")
        return segments


def create_srt_file(
    segments: List[Dict[str, Any]],
    output_path: str,
    status_callback: Optional[Callable[[str], None]] = None
) -> bool:
    """Create SRT subtitle file from segments."""
    try:
        if not segments:
            logger.warning("No segments provided for SRT creation")
            return False
        
        if status_callback:
            status_callback("Creating SRT subtitle file...")
        
        # Create SRT subtitle file
        subs = pysrt.SubRipFile()
        
        for i, segment in enumerate(segments):
            start_time = segment.get('start', 0)
            end_time = segment.get('end', start_time + 1)
            text = segment.get('text', '').strip()
            
            if not text:
                continue
            
            # Convert seconds to SRT time format
            start_srt = pysrt.SubRipTime(seconds=start_time)
            end_srt = pysrt.SubRipTime(seconds=end_time)
            
            # Create subtitle item
            sub_item = pysrt.SubRipItem(
                index=i + 1,
                start=start_srt,
                end=end_srt,
                text=text
            )
            
            subs.append(sub_item)
        
        # Save SRT file
        subs.save(output_path, encoding='utf-8')
        
        if status_callback:
            status_callback(f"SRT file created: {output_path}")
        
        logger.info(f"Created SRT file with {len(subs)} subtitles: {output_path}")
        return True
        
    except Exception as e:
        logger.error(f"Error creating SRT file: {e}")
        if status_callback:
            status_callback(f"SRT creation error: {str(e)}")
        return False


def extract_audio_from_video_clip(
    video_clip: VideoFileClip,
    output_path: str = None,
    status_callback: Optional[Callable[[str], None]] = None
) -> Optional[str]:
    """Extract audio from video clip."""
    try:
        if not video_clip.audio:
            logger.warning("Video clip has no audio track")
            return None
        
        if status_callback:
            status_callback("Extracting audio from video...")
        
        # Create output path if not provided
        if not output_path:
            output_path = create_safe_temp_filename("extracted_audio", ".wav")
        
        # Extract audio
        audio_clip = video_clip.audio
        audio_clip.write_audiofile(
            output_path,
            verbose=False,
            logger=None
        )
        
        # Cleanup
        safe_close_moviepy_object(audio_clip, "extracted_audio")
        
        if os.path.exists(output_path):
            if status_callback:
                status_callback(f"Audio extracted: {output_path}")
            return output_path
        else:
            logger.error("Failed to extract audio")
            return None
            
    except Exception as e:
        logger.error(f"Error extracting audio: {e}")
        if status_callback:
            status_callback(f"Audio extraction error: {str(e)}")
        return None


def normalize_audio_levels(
    audio_clip: AudioFileClip,
    target_db: float = -20.0,
    status_callback: Optional[Callable[[str], None]] = None
) -> AudioFileClip:
    """Normalize audio levels to target dB."""
    try:
        if status_callback:
            status_callback("Normalizing audio levels...")
        
        # Get audio as numpy array
        audio_array = audio_clip.to_soundarray()
        
        # Calculate current RMS level
        rms = np.sqrt(np.mean(audio_array ** 2))
        
        if rms == 0:
            logger.warning("Audio clip is silent, cannot normalize")
            return audio_clip
        
        # Calculate current dB level
        current_db = 20 * np.log10(rms)
        
        # Calculate gain needed
        gain_db = target_db - current_db
        gain_linear = 10 ** (gain_db / 20)
        
        # Apply gain
        normalized_clip = audio_clip.multiply_volume(gain_linear)
        
        if status_callback:
            status_callback(f"Audio normalized: {current_db:.1f}dB -> {target_db:.1f}dB")
        
        return normalized_clip
        
    except Exception as e:
        logger.error(f"Error normalizing audio: {e}")
        if status_callback:
            status_callback(f"Audio normalization error: {str(e)}")
        return audio_clip


def apply_audio_fade(
    audio_clip: AudioFileClip,
    fade_in_duration: float = 0.5,
    fade_out_duration: float = 0.5
) -> AudioFileClip:
    """Apply fade in/out effects to audio clip."""
    try:
        # Apply fade in
        if fade_in_duration > 0:
            audio_clip = audio_clip.audio_fadein(fade_in_duration)
        
        # Apply fade out
        if fade_out_duration > 0:
            audio_clip = audio_clip.audio_fadeout(fade_out_duration)
        
        return audio_clip
        
    except Exception as e:
        logger.error(f"Error applying audio fade: {e}")
        return audio_clip


def mix_audio_tracks(
    main_audio: AudioFileClip,
    background_audio: AudioFileClip,
    background_volume: float = 0.5,
    status_callback: Optional[Callable[[str], None]] = None
) -> AudioFileClip:
    """Mix main audio with background audio."""
    try:
        if status_callback:
            status_callback("Mixing audio tracks...")
        
        # Adjust background volume
        background_audio = background_audio.multiply_volume(background_volume)
        
        # Match durations
        if background_audio.duration > main_audio.duration:
            background_audio = background_audio.subclip(0, main_audio.duration)
        elif background_audio.duration < main_audio.duration:
            # Loop background audio if needed
            loops_needed = int(main_audio.duration / background_audio.duration) + 1
            background_clips = [background_audio] * loops_needed
            from moviepy import concatenate_audioclips
            background_audio = concatenate_audioclips(background_clips).subclip(0, main_audio.duration)
        
        # Create composite audio
        mixed_audio = CompositeAudioClip([main_audio, background_audio])
        
        if status_callback:
            status_callback("Audio tracks mixed successfully")
        
        return mixed_audio
        
    except Exception as e:
        logger.error(f"Error mixing audio tracks: {e}")
        if status_callback:
            status_callback(f"Audio mixing error: {str(e)}")
        return main_audio


def create_silence_clip(duration: float, sample_rate: int = 44100) -> AudioFileClip:
    """Create a silent audio clip of specified duration."""
    try:
        # Create silent audio array
        samples = int(duration * sample_rate)
        silent_array = np.zeros((samples, 2))  # Stereo
        
        # Create temporary audio file
        temp_path = create_safe_temp_filename("silence", ".wav")
        
        # Use pydub to create silent audio
        silent_audio = AudioSegment.silent(duration=int(duration * 1000))  # pydub uses milliseconds
        silent_audio.export(temp_path, format="wav")
        
        # Create MoviePy audio clip
        silence_clip = AudioFileClip(temp_path)
        
        return silence_clip
        
    except Exception as e:
        logger.error(f"Error creating silence clip: {e}")
        return None


def analyze_audio_properties(
    audio_path: str,
    status_callback: Optional[Callable[[str], None]] = None
) -> Dict[str, Any]:
    """Analyze audio file properties."""
    try:
        if not os.path.exists(audio_path):
            logger.error(f"Audio file not found: {audio_path}")
            return {}
        
        if status_callback:
            status_callback("Analyzing audio properties...")
        
        properties = {}
        
        # Get basic properties using FFmpeg
        properties['duration'] = get_audio_duration_ffmpeg(audio_path)
        properties['codec'] = get_audio_codec_ffmpeg(audio_path)
        
        # Load with MoviePy for additional analysis
        try:
            audio_clip = AudioFileClip(audio_path)
            properties['fps'] = audio_clip.fps
            properties['nchannels'] = audio_clip.nchannels
            
            # Get audio array for analysis
            audio_array = audio_clip.to_soundarray()
            
            # Calculate RMS level
            rms = np.sqrt(np.mean(audio_array ** 2))
            properties['rms_level'] = rms
            properties['db_level'] = 20 * np.log10(rms) if rms > 0 else -np.inf
            
            # Calculate peak level
            properties['peak_level'] = np.max(np.abs(audio_array))
            properties['peak_db'] = 20 * np.log10(properties['peak_level']) if properties['peak_level'] > 0 else -np.inf
            
            # Cleanup
            safe_close_moviepy_object(audio_clip, "audio_analysis")
            
        except Exception as e:
            logger.warning(f"Error analyzing audio with MoviePy: {e}")
        
        if status_callback:
            status_callback("Audio analysis completed")
        
        return properties
        
    except Exception as e:
        logger.error(f"Error analyzing audio properties: {e}")
        if status_callback:
            status_callback(f"Audio analysis error: {str(e)}")
        return {}



def mux_stock_with_voiceover(
    silent_video_path: str,
    full_audio_path: str,
    audio_start_offset: float,
    audio_segment_duration: float,
    output_path: str,
    status_callback: Callable[[str, bool], None] = lambda m, e=False: None,
    add_sound_effect: bool = False,
    sound_effect_duration: float = 0.8
) -> str:
    """
    (Versione Rivista)
    Muxa un segmento video silente con una porzione di audio usando FFmpeg.
    Combina la validazione della versione 'nuova' con il posizionamento
    di -ss e -t *prima* dell'input audio, come nella versione 'vecchia',
    per velocità e per ripristinare potenzialmente il comportamento preferito.

    Args:
        silent_video_path: Path del video silente.
        full_audio_path: Path del file audio completo (voiceover).
        audio_start_offset: Tempo (in secondi) da cui iniziare a leggere l'audio.
        audio_segment_duration: Durata (in secondi) dell'audio da estrarre.
        output_path: Path del file video risultante.
        status_callback: Funzione per loggare i messaggi.

    Returns:
        Il percorso del file di output.

    Raises:
        ValueError: Se i parametri di durata/offset non sono validi.
        FileNotFoundError: Se i file di input non vengono trovati.
        RuntimeError: Se FFmpeg fallisce o il file output non è valido.
    """
    func_name = "mux_stock_with_voiceover_revised" # Nome interno per logging
    original_output_path = output_path
    base_output_name = os.path.basename(original_output_path)
    status_callback(f"\n[{func_name}] --- Avvio Muxing (seek PRE-input) per: {base_output_name} ---", False)

    # Se output == input, ffmpeg fallisce ("Output same as Input").
    # In quel caso, scrivi su un temp file e poi rimpiazza l'originale.
    temp_output_path = None
    try:
        if os.path.abspath(original_output_path) == os.path.abspath(silent_video_path):
            temp_output_path = os.path.join(
                os.path.dirname(original_output_path),
                f"{os.path.splitext(os.path.basename(original_output_path))[0]}_mux_{uuid.uuid4().hex}.tmp.mp4"
            )
            output_path = temp_output_path
            status_callback(
                f"[{func_name}] Output uguale all'input: uso temp file {os.path.basename(temp_output_path)}",
                False
            )
    except Exception:
        # Se fallisce la normalizzazione path, procedi con output_path originale.
        output_path = original_output_path

    # Ensure numeric parameters are properly typed
    audio_start_offset = float(audio_start_offset)
    audio_segment_duration = float(audio_segment_duration)
    sound_effect_duration = float(sound_effect_duration)

    # SFX viene ora inserito nei segmenti stock; evita doppia applicazione in fase di mux
    if add_sound_effect:
        try:
            status_callback(f"[{func_name}] Ignoro parametro add_sound_effect: SFX gia presente nei segmenti stock", False)
        except Exception:
            pass
        add_sound_effect = False

    # --- Validazione Input Essenziale (dalla versione 'nuova') ---
    min_duration_threshold = 0.01
    if audio_segment_duration < min_duration_threshold:
        raise ValueError(f"Durata segmento audio ({audio_segment_duration:.3f}s) troppo breve.")
    if not os.path.isfile(silent_video_path):
        raise FileNotFoundError(f"Video silente non trovato: {silent_video_path}")
    if not os.path.isfile(full_audio_path):
        raise FileNotFoundError(f"Audio non trovato: {full_audio_path}")

    # --- Ottieni Metadati Audio e Valida Offset/Durata (dalla versione 'nuova') ---
    try:
        audio_total_duration = get_audio_duration_ffmpeg(full_audio_path)
        audio_codec_original = get_audio_codec_ffmpeg(full_audio_path) # Rileva codec originale
        if audio_total_duration and audio_total_duration > 0:
            status_callback(f"[{func_name}] Durata audio totale: {audio_total_duration:.3f}s, Codec originale: {audio_codec_original}", False)
        else:
            audio_total_duration = None
            status_callback(f"[{func_name}] Durata audio non disponibile, procedo usando richiesta {audio_segment_duration:.3f}s", True)
    except Exception as e_meta:
         # Rilancia come RuntimeError per coerenza
         raise RuntimeError(f"[{func_name}] Impossibile leggere metadati audio da '{full_audio_path}': {e_meta}") from e_meta

    # Correzione offset negativo
    if audio_start_offset < 0:
        status_callback(f"[{func_name}] 📋 Offset audio negativo ({audio_start_offset:.3f}s), imposto a 0.", False)
        audio_start_offset = 0.0

    # Verifica che l'offset sia valido
    # Permette offset = durata totale per prendere l'ultimissimo istante (raro ma possibile)
    if audio_total_duration is not None and audio_start_offset > audio_total_duration:
        raise ValueError(f"Offset audio ({audio_start_offset:.2f}s) non valido, supera la durata totale ({audio_total_duration:.2f}s).")

    # Calcola la durata effettiva da prendere, senza superare la fine dell'audio
    if audio_total_duration is not None:
        max_possible_duration = max(0.0, audio_total_duration - audio_start_offset)
    else:
        max_possible_duration = audio_segment_duration
    adjusted_audio_duration = min(audio_segment_duration, max_possible_duration) if max_possible_duration > 0 else audio_segment_duration

    # Verifica che la durata calcolata sia ancora significativa
    if adjusted_audio_duration < min_duration_threshold:
         raise ValueError(f"Durata audio calcolata ({adjusted_audio_duration:.3f}s) dopo offset troppo breve.")

    status_callback(f"[{func_name}] Offset audio: {audio_start_offset:.3f}s, Durata da muxare: {adjusted_audio_duration:.3f}s", False)

    # --- Parametri Codec Audio (normalizza sample rate per compatibilita concat) ---
    # Rileva il sample rate originale dell'audio e applica ricampionamento uniforme
    try:
        import subprocess
        import json
        probe_cmd = [
            'ffprobe', '-v', 'quiet', '-print_format', 'json',
            '-show_streams', '-select_streams', 'a:0', full_audio_path
        ]
        result = subprocess.run(probe_cmd, capture_output=True, text=True, check=True)
        probe_data = json.loads(result.stdout)
        original_sample_rate = int(probe_data['streams'][0]['sample_rate'])
        # Mantieni il sample rate originale del voiceover per evitare artefatti "robotici".
        # Override manuale con VELOX_AUDIO_TARGET_SAMPLE_RATE se necessario.
        target_override = os.environ.get("VELOX_AUDIO_TARGET_SAMPLE_RATE", "").strip()
        if target_override.isdigit():
            target_sample_rate_int = int(target_override)
        else:
            target_sample_rate_int = original_sample_rate
        status_callback(
            f"[{func_name}] Sample rate originale rilevato: {original_sample_rate} Hz (target: {target_sample_rate_int} Hz)",
            False
        )
        # Codec per intermedi: AAC con sample rate uniforme per evitare errori di concat
        target_sample_rate = str(target_sample_rate_int)
        audio_params = ['-c:a','aac','-b:a','192k','-ar',target_sample_rate,'-ac','2']
    except Exception as e:
        status_callback(f"[{func_name}] ❌ Impossibile rilevare sample rate originale: {e}, uso {PREFERRED_OUTPUT_SAMPLE_RATE} Hz come fallback", True)
        target_sample_rate_int = PREFERRED_OUTPUT_SAMPLE_RATE
        target_sample_rate = str(target_sample_rate_int)
        audio_params = ['-c:a','aac','-b:a','192k','-ar',target_sample_rate,'-ac','2']
    
    status_callback(f"[{func_name}] Codifica audio AAC fissata a {target_sample_rate_int} Hz per garantire compatibilita tra segmenti.", False)

    # Helper: check if a file has at least one audio stream
    def _has_audio_stream_ffprobe(path: str) -> bool:
        try:
            probe_cmd = [
                'ffprobe', '-v', 'error', '-select_streams', 'a:0',
                '-show_entries', 'stream=index', '-of', 'csv=p=0', path
            ]
            res = subprocess.run(probe_cmd, capture_output=True, text=True, timeout=5)
            return res.returncode == 0 and bool(res.stdout.strip())
        except Exception:
            return False

    # Prefer keeping voiceover clean: by default do NOT mix stock audio.
    keep_stock_audio = str(os.environ.get("VELOX_MUX_KEEP_STOCK_AUDIO", "1")).strip() == "1"

    # --- Costruzione Comando FFmpeg ---
    # Per file MP3/AAC, usa un approccio più robusto con pre-estrazione audio
    needs_robust_handling = (('mp3' in str(audio_codec_original).lower()) or ('aac' in str(audio_codec_original).lower()) or full_audio_path.lower().endswith(('.mp3', '.aac', '.m4a')))
    
    if needs_robust_handling:
        status_callback(f"[{func_name}] Pre-estrazione segmento audio in WAV per robustezza MP3", False)
         
        # Crea un file audio temporaneo WAV per evitare problemi con seek su MP3
        temp_audio_wav = os.path.join(os.path.dirname(output_path), f"temp_audio_{uuid.uuid4().hex}.wav")
        
        # Estrai il segmento audio in formato PCM WAV
        extract_cmd = [
            "ffmpeg", "-y", "-loglevel", "error",
            "-ss", f"{audio_start_offset:.6f}",
            "-i", full_audio_path,
            "-t", f"{adjusted_audio_duration:.6f}",
            "-c:a", "pcm_s16le",
            "-ar", target_sample_rate,
            "-ac", "2",
            temp_audio_wav
        ]
        
        try:
            status_callback(f"[{func_name}] Estrazione audio: {' '.join(extract_cmd)}", False)
            result = subprocess.run(extract_cmd, capture_output=True, text=True, check=True)
            
            # Ora muxa con il file WAV estratto
            # Usa la durata del video silente per evitare tagli prematuri del video
            try:
                video_dur = float(get_clip_duration(silent_video_path))
            except Exception:
                video_dur = float(adjusted_audio_duration)
            cflags = [
                "-hide_banner","-loglevel","error","-nostdin",
                "-fflags","+genpts","-avoid_negative_ts","make_zero",
                "-muxpreload","0","-muxdelay","0"
            ]
            cmd_mux = [
                "ffmpeg", "-y",
                "-i", silent_video_path,
                "-i", temp_audio_wav,
            ] + cflags + [
                "-map", "0:v:0",
                # We'll map filtered audio later via filter_complex
                "-c:v", "copy",
            ] + audio_params + [
                "-shortest",
                "-t", f"{video_dur:.6f}",
                output_path
            ]

            # Mix stock original audio (0:a) with extracted VO (1:a) only if explicitly enabled.
            if keep_stock_audio and _has_audio_stream_ffprobe(silent_video_path):
                mix_filter = (
                    f"[0:a]aresample=sample_rate={target_sample_rate},aformat=channel_layouts=stereo:sample_fmts=s16[stock];"
                    f"[1:a]aresample=sample_rate={target_sample_rate},aformat=channel_layouts=stereo:sample_fmts=s16[vo];"
                    f"[stock][vo]amix=inputs=2:weights='0.15 1.0':duration=first:normalize=0:dropout_transition=0[aout]"
                )
            else:
                # Default: use voiceover only (cleaner)
                mix_filter = (
                    f"[1:a]aresample=sample_rate={target_sample_rate},aformat=channel_layouts=stereo:sample_fmts=s16[aout]"
                )
            for i, arg in enumerate(cmd_mux):
                if isinstance(arg, str) and arg.startswith('-') and arg not in ['-i', '-y']:
                    cmd_mux.insert(i, mix_filter)
                    cmd_mux.insert(i, "-filter_complex")
                    break
            # Replace any direct audio map with filtered output, otherwise ensure mapping [aout]
            replaced = False
            if "-map" in cmd_mux:
                for i in range(len(cmd_mux)-1):
                    if cmd_mux[i] == "-map" and isinstance(cmd_mux[i+1], str) and cmd_mux[i+1].endswith("a:0"):
                        cmd_mux[i+1] = "[aout]"
                        replaced = True
                        break
            if not replaced:
                # Ensure we emit the filtered audio
                try:
                    cv_idx = cmd_mux.index("-c:v")
                except ValueError:
                    cv_idx = max(0, len(cmd_mux)-1)
                cmd_mux.insert(cv_idx, "[aout]")
                cmd_mux.insert(cv_idx, "-map")
            
        except subprocess.CalledProcessError as e:
            status_callback(f"[{func_name}] [!] Estrazione audio fallita: {e}, uso metodo standard", True)
            # Fallback al metodo standard
            try:
                video_dur = get_clip_duration(silent_video_path)
            except Exception:
                video_dur = adjusted_audio_duration
            cflags = [
                "-hide_banner","-loglevel","error","-nostdin",
                "-fflags","+genpts","-avoid_negative_ts","make_zero",
                "-muxpreload","0","-muxdelay","0"
            ]
            cmd_mux = [
                "ffmpeg", "-y",
                "-i", silent_video_path,
                "-i", full_audio_path,
                "-ss", f"{audio_start_offset:.6f}",
                "-t", f"{adjusted_audio_duration:.6f}",
            ] + cflags + [
                "-map", "0:v:0",
                # audio mapped via filter
                "-c:v", "copy"
            ] + audio_params + ["-shortest", "-t", f"{video_dur:.6f}", output_path]

            # Mix stock original audio (0:a) with VO (1:a) only if explicitly enabled.
            if keep_stock_audio and _has_audio_stream_ffprobe(silent_video_path):
                mix_filter = (
                    f"[0:a]aresample=sample_rate={target_sample_rate},aformat=channel_layouts=stereo:sample_fmts=s16[stock];"
                    f"[1:a]aresample=sample_rate={target_sample_rate},aformat=channel_layouts=stereo:sample_fmts=s16[vo];"
                    f"[stock][vo]amix=inputs=2:weights='0.15 1.0':duration=first:normalize=0:dropout_transition=0[aout]"
                )
            else:
                mix_filter = (
                    f"[1:a]aresample=sample_rate={target_sample_rate},aformat=channel_layouts=stereo:sample_fmts=s16[aout]"
                )
            for i, arg in enumerate(cmd_mux):
                if isinstance(arg, str) and arg.startswith('-') and arg not in ['-i', '-y']:
                    cmd_mux.insert(i, mix_filter)
                    cmd_mux.insert(i, "-filter_complex")
                    break
            replaced = False
            if "-map" in cmd_mux:
                for i in range(len(cmd_mux)-1):
                    if cmd_mux[i] == "-map" and isinstance(cmd_mux[i+1], str) and cmd_mux[i+1].endswith("a:0"):
                        cmd_mux[i+1] = "[aout]"
                        replaced = True
                        break
            if not replaced:
                try:
                    cv_idx = cmd_mux.index("-c:v")
                except ValueError:
                    cv_idx = max(0, len(cmd_mux)-1)
                cmd_mux.insert(cv_idx, "[aout]")
                cmd_mux.insert(cv_idx, "-map")
            temp_audio_wav = None
    else:
        # Per altri formati, usa il metodo standard con seek PRE-input
        try:
            video_dur = float(get_clip_duration(silent_video_path))
        except Exception:
            video_dur = float(adjusted_audio_duration)
        cflags = [
            "-hide_banner","-loglevel","error","-nostdin",
            "-fflags","+genpts","-avoid_negative_ts","make_zero",
            "-muxpreload","0","-muxdelay","0"
        ]
        cmd_mux = [
            "ffmpeg", "-y",
            "-i", silent_video_path,
            "-ss", f"{audio_start_offset:.6f}",
            "-t", f"{adjusted_audio_duration:.6f}",
            "-i", full_audio_path,
        ] + cflags + [
            "-map", "0:v:0",
            # audio mapped via filter
            "-c:v", "copy"
        ] + audio_params + ["-shortest", "-t", f"{video_dur:.6f}", output_path]

        if keep_stock_audio and _has_audio_stream_ffprobe(silent_video_path):
            mix_filter = (
                f"[0:a]aresample=sample_rate={target_sample_rate},aformat=channel_layouts=stereo:sample_fmts=s16[stock];"
                f"[1:a]aresample=sample_rate={target_sample_rate},aformat=channel_layouts=stereo:sample_fmts=s16[vo];"
                f"[stock][vo]amix=inputs=2:weights='0.15 1.0':duration=first:normalize=0:dropout_transition=0[aout]"
            )
        else:
            mix_filter = (
                f"[1:a]aresample=sample_rate={target_sample_rate},aformat=channel_layouts=stereo:sample_fmts=s16[aout]"
            )
        for i, arg in enumerate(cmd_mux):
            if isinstance(arg, str) and arg.startswith('-') and arg not in ['-i', '-y']:
                cmd_mux.insert(i, mix_filter)
                cmd_mux.insert(i, "-filter_complex")
                break
        replaced = False
        if "-map" in cmd_mux:
            for i in range(len(cmd_mux)-1):
                if cmd_mux[i] == "-map" and isinstance(cmd_mux[i+1], str) and cmd_mux[i+1].endswith("a:0"):
                    cmd_mux[i+1] = "[aout]"
                    replaced = True
                    break
        if not replaced:
            try:
                cv_idx = cmd_mux.index("-c:v")
            except ValueError:
                cv_idx = max(0, len(cmd_mux)-1)
            cmd_mux.insert(cv_idx, "[aout]")
            cmd_mux.insert(cv_idx, "-map")
        temp_audio_wav = None

    # --- Ensure all command arguments are strings ---
    cmd_mux = [str(arg) for arg in cmd_mux]
    
    # --- Debug: Stampa comando completo prima dell'esecuzione ---
    cmd_str = ' '.join([f'"{arg}"' if ' ' in arg else arg for arg in cmd_mux])
    status_callback(f"[{func_name}] Comando ffmpeg completo: {cmd_str}", False)
    
    # --- Verifica esistenza file di input prima dell'esecuzione ---
    if not os.path.exists(silent_video_path):
        raise FileNotFoundError(f"[{func_name}] Video silente non trovato: {silent_video_path}")
    if not os.path.exists(full_audio_path):
        raise FileNotFoundError(f"[{func_name}] File audio non trovato: {full_audio_path}")
    
    status_callback(f"[{func_name}] File di input verificati - Video: {os.path.getsize(silent_video_path)} bytes, Audio: {os.path.getsize(full_audio_path)} bytes", False)

    # --- Esecuzione Comando (Usa la tua funzione helper) ---
    try:
        runner = globals().get('run_ffmpeg_command')
        if callable(runner):
            runner(
                cmd_mux,
                f"Mux video+audio per {base_output_name}",
                status_callback,
                progress=False,
                timeout=120
            )
        else:
            result = subprocess.run(
                cmd_mux,
                text=True,
                capture_output=True
            )
            if result.returncode != 0:
                raise RuntimeError(result.stderr.strip() or "FFmpeg command failed")
            if status_callback:
                status_callback(f"[{func_name}] Completato muxing tramite subprocess.run", False)
    except Exception as e_run:
         # Rilancia eccezioni da run_ffmpeg_command o altri errori
         status_callback(f"[{func_name}] ERRORE durante esecuzione FFmpeg: {e_run}", True)
         status_callback(f"[{func_name}] Comando che ha fallito: {cmd_str}", True)
         raise RuntimeError(f"Fallimento esecuzione FFmpeg in {func_name}: {e_run}") from e_run

    # --- Verifica Output Finale ---
    if not os.path.isfile(output_path) or os.path.getsize(output_path) < 100: # Aumentato check dimensione
        raise RuntimeError(f"[{func_name}] File di output muxing non creato o vuoto/corrotto: {output_path}")

    # Se abbiamo usato un file temporaneo, rimpiazza l'output originale.
    if temp_output_path:
        try:
            os.replace(temp_output_path, original_output_path)
            output_path = original_output_path
        except Exception as e:
            raise RuntimeError(f"[{func_name}] Impossibile sostituire output finale: {e}") from e

    # --- Pulizia file temporanei ---
    if 'temp_audio_wav' in locals() and temp_audio_wav and os.path.exists(temp_audio_wav):
        try:
            os.remove(temp_audio_wav)
            status_callback(f"[{func_name}] File audio temporaneo rimosso: {os.path.basename(temp_audio_wav)}", False)
        except OSError as e:
            status_callback(f"[{func_name}] [!] Impossibile rimuovere file temporaneo: {e}", True)

    status_callback(f"[{func_name}] --- Muxing completato: {base_output_name} ---", False)
    return output_path




def _generate_voiced_stock_segment_task(
    segment_index: int,
    duration_needed: float,
    audio_path_main: str,
    audio_offset: float,
    stock_clips_src: List[str],
    temp_dir_segments: str,
    temp_dir_mux: str,
    status_callback: Callable[[str, bool], None],
    base_w: int,
    base_h: int,
    base_fps: float,
    main_label: str,
    config_settings: Optional[Dict[str, Any]] = None,
    followed_by_clip: bool = False,
):
    """Wrapper function that calls the real generate_stock_segment function.
    Adapts parameters from the task format to the real function format.
    Returns (output_path, duration_seconds) or None on failure.
    """
    try:
        duration = max(0.1, float(duration_needed))

        # Create a wrapper status callback that matches the generate_stock_segment signature (str, bool) -> None
        def real_status_callback(message: str, error: bool = False):
            status_callback(message, error)  # Call the parent callback with both message and error flag

        # Call the real generate_stock_segment function
        result_path = generate_stock_segment(
            duration=duration,
            stock_clips=stock_clips_src or [],
            cache_dir=None,  # Let the function use its default cache manager
            temp_dir=temp_dir_segments,
            status_callback=real_status_callback,
            base_w=base_w,
            base_h=base_h,
            base_fps=int(base_fps),
            ffmpeg_preset="medium",
            random_generator=None
        )

        if result_path and os.path.exists(result_path) and os.path.getsize(result_path) > 0:
            # 🎬 TRANSIZIONE ALL'INIZIO DELLO STOCK (sostituisce i primi secondi con transizione casuale)
            config_settings = config_settings or {}
            add_stock_transition = config_settings.get("add_transition_to_stock", False)
            stock_transition_duration = config_settings.get("stock_transition_duration", 0.4)
            
            # Import TRANSITION_PATHS from config
            try:
                from config.config import TRANSITION_PATHS
            except ImportError:
                try:
                    from config.config import TRANSITION_PATHS
                except ImportError:
                    from config import TRANSITION_PATHS
            
            if add_stock_transition and TRANSITION_PATHS and duration > stock_transition_duration:
                import random
                import uuid
                import subprocess
                
            config_settings = config_settings or {}
            add_stock_transition = config_settings.get("add_transition_to_stock", False)
            stock_transition_duration = config_settings.get("stock_transition_duration", 0.5)

            if add_stock_transition and duration > stock_transition_duration and get_all_available_transitions is not None:
                result_path, trans_duration = _prepend_random_transition_to_stock(
                    stock_segment_path=result_path,
                    temp_dir=temp_dir_mux,
                    status_callback=status_callback,
                    base_w=base_w,
                    base_h=base_h,
                    base_fps=base_fps,
                    base_duration=duration,
                    transition_duration=stock_transition_duration,
                )
                if trans_duration > 0:
                    duration += trans_duration

        # Mux voiceover segment onto stock clip (per-segment audio)
        try:
            if result_path and os.path.exists(result_path) and audio_path_main and os.path.exists(audio_path_main):
                import hashlib
                safe_hash = hashlib.md5(f"{audio_path_main}|{audio_offset:.3f}|{duration:.3f}".encode()).hexdigest()[:6]
                muxed_out = os.path.join(temp_dir_mux, f"stock_voiced_{segment_index}_{safe_hash}.mp4")
                try:
                    seg_start = float(audio_offset)
                    seg_end = seg_start + float(duration)
                    status_callback(f"{main_label} [Mux] Segmento #{segment_index}: audio {seg_start:.2f}s → {seg_end:.2f}s (dur {duration:.2f}s)", False)
                except Exception:
                    pass
                result_path = mux_stock_with_voiceover(
                    silent_video_path=result_path,
                    full_audio_path=audio_path_main,
                    audio_start_offset=audio_offset,
                    audio_segment_duration=duration,
                    output_path=muxed_out,
                    status_callback=status_callback,
                )
        except Exception as e:
            status_callback(f"{main_label} ⚠️ Mux audio fallito segmento #{segment_index}: {e}", True)

        return result_path, duration

    except Exception as e:
        status_callback(f"{main_label} Error generating stock segment #{segment_index}: {e}", True)
        return None


def get_clip_duration(filepath: str) -> float:
    """
    Restituisce la durata di un file video in secondi usando cache FFmpeg completa.
    
    Args:
        filepath: Percorso del file video.
    
    Returns:
        Durata in secondi, o 0.0 in caso di errore.
    """
    import json
    import subprocess
    import logging
    import os
    
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





