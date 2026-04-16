"""
Video Processing Module - Modularized Version
Questo file importa e riorganizza le funzioni dai moduli specializzati.
"""

import os
import sys
import re
import logging
from typing import List, Dict, Any, Optional, Tuple, Callable

# Import from specialized modules with fallback
try:
    from modules.video.video_core import (
        validate_audio_stream,
        create_safe_audio_clip,
        safe_moviepy_write_videofile_pure,
        safe_moviepy_write_videofile,
        get_cross_platform_subprocess_env,
        sanitize_ffmpeg_path,
        sanitize_text_for_filename,
        create_safe_temp_filename,
        safe_moviepy_operation,
        aggressive_memory_cleanup,
        safe_close_moviepy_object,
        cleanup_ffmpeg_processes,
        test_ffmpeg_subprocess_health,
        comprehensive_moviepy_cleanup,
        close_clip,
        hard_cleanup,
        log_mem,
        force_gc_and_cleanup,
        _validate_resources,
        has_video_stream,
        get_clip_duration,
        extract_audio_from_video,
        get_video_info,
        create_video_preview
    )

    from video_ffmpeg import (
        get_audio_duration_ffmpeg,
        get_audio_codec_ffmpeg,
        run_ffmpeg_command,
        merge_overlays_ffmpeg,
        format_timestamp,
        _safe_float
    )

    from video_clips import (
        concatenate_video_clips,
        resize_video_to_standard,
        loop_video_clip,
        loop_audio_clip,
        apply_gaussian_blur
    )

    from video_effects import (
        add_single_sound_effect_to_clip,
        add_special_names_sound_effect,
        create_transition_clip,
        create_random_transition_with_sound,
        add_background_music
    )

    from video_overlays import (
        make_text_image_date,
        create_text_overlay_clip,
        create_image_overlay_clip,
        _render_single_image_overlay_task_worker,
        create_typewriter_effect_clip,
        crea_effetto_typewriting_nomi,
        create_subtitle_overlay,
        create_logo_watermark,
        apply_text_animation,
        make_important_words_image
        )

        # Removed circular import from video_generation to avoid ImportError
        # These functions should be imported directly where needed
        # from .video_generation import (
        #     generate_video_parallelized,
        #     generate_video_from_segments,
        #     create_video_clip_from_segment,
        #     create_video_segment_clip,
        #     create_image_segment_clip,
        #     create_text_segment_clip,
        #     create_stock_segment_clip,
        #     add_transitions_between_clips,
        #     add_subtitles_to_video,
        #     write_final_video
        # )

    from modules.video.video_audio import (
        get_whisper_model,
        detect_clip_language_safe,
        transcribe_audio_with_whisper,
        translate_text_segments,
        create_srt_file,
        extract_audio_from_video_clip,
        normalize_audio_levels,
        apply_audio_fade,
        mix_audio_tracks,
        create_silence_clip,
        analyze_audio_properties
    )

    # Import configuration and utilities
    try:
        from config.config import (
            BACKGROUND_MUSIC_PATHS,
            BACKGROUND_MUSIC_VOLUME,
            BASE_FPS_DEFAULT as VIDEO_FPS,
            BASE_H_DEFAULT as VIDEO_HEIGHT,
            BASE_W_DEFAULT as VIDEO_WIDTH,
            DEFAULT_SOUND_EFFECTS,
            FONT_PATH_DEFAULT,
            FONT_PATHS,
            TRANSITION_PATHS,
            SUBTITLE_BURNIN_TIMEOUT_DEFAULT,
        )
        from modules.utils.utils import safe_remove, download_image, clean_text
        from modules.audio.audio_processing import trascrivi_audio_parallel
    except ImportError:
        # Fallback imports if modules are not found
        print("Warning: Some configuration modules not found, using defaults")
        BACKGROUND_MUSIC_PATHS = []
        BACKGROUND_MUSIC_VOLUME = 0.3
        VIDEO_FPS = 24
        VIDEO_HEIGHT = 1080
        VIDEO_WIDTH = 1920
        DEFAULT_SOUND_EFFECTS = {}
        FONT_PATH_DEFAULT = ""
        FONT_PATHS = []
        TRANSITION_PATHS = []
        SUBTITLE_BURNIN_TIMEOUT_DEFAULT = 300  # Increased from 30 to 300 seconds (5 minutes) to prevent subtitle timeout
except ImportError:
    # Fallback to absolute imports when run as a script
    try:
            from modules.video.video_core import (
                validate_audio_stream,
                create_safe_audio_clip,
        safe_moviepy_write_videofile_pure,
        safe_moviepy_write_videofile,
        get_cross_platform_subprocess_env,
        sanitize_ffmpeg_path,
        sanitize_text_for_filename,
        create_safe_temp_filename,
        safe_moviepy_operation,
        aggressive_memory_cleanup,
        safe_close_moviepy_object,
        cleanup_ffmpeg_processes,
        test_ffmpeg_subprocess_health,
        comprehensive_moviepy_cleanup,
        close_clip,
        hard_cleanup,
        log_mem,
        force_gc_and_cleanup,
        _validate_resources,
        has_video_stream,
        get_clip_duration,
        extract_audio_from_video,
        get_video_info,
        create_video_preview
            )
            from modules.video.video_ffmpeg import (
                get_audio_duration_ffmpeg,
                get_audio_codec_ffmpeg,
                run_ffmpeg_command,
                merge_overlays_ffmpeg,
                format_timestamp,
                _safe_float
            )
            from modules.video.video_clips import (
                concatenate_video_clips,
                resize_video_to_standard,
                loop_video_clip,
                loop_audio_clip,
                apply_gaussian_blur
            )
            from modules.video.video_effects import (
                add_single_sound_effect_to_clip,
                add_special_names_sound_effect,
                create_transition_clip,
                create_random_transition_with_sound,
                add_background_music
            )
            from modules.video.video_overlays import (
                make_text_image_date,
                create_text_overlay_clip,
                create_image_overlay_clip,
                _render_single_image_overlay_task_worker,
                create_typewriter_effect_clip,
                crea_effetto_typewriting_nomi,
                create_subtitle_overlay,
                create_logo_watermark,
                apply_text_animation,
                make_important_words_image
            )

            # Removed circular import from video_generation to avoid ImportError
            # These functions will be imported lazily where needed to prevent circular dependencies
            # from video_generation import (
            #     generate_video_parallelized,
            #     generate_video_from_segments,
            #     create_video_clip_from_segment,
            #     create_video_segment_clip,
            #     create_image_segment_clip,
            #     create_text_segment_clip,
            #     create_stock_segment_clip,
            #     add_transitions_between_clips,
            #     add_subtitles_to_video,
            #     write_final_video
            # )

            from modules.video.video_audio import (
                get_whisper_model,
                detect_clip_language_safe,
                transcribe_audio_with_whisper,
                translate_text_segments,
                create_srt_file,
                extract_audio_from_video_clip,
                normalize_audio_levels,
                apply_audio_fade,
                mix_audio_tracks,
                create_silence_clip,
                analyze_audio_properties
            )

            # Import configuration and utilities
            try:
                from config.config import (
                    BACKGROUND_MUSIC_PATHS,
                    BACKGROUND_MUSIC_VOLUME,
                    BASE_FPS_DEFAULT as VIDEO_FPS,
                    BASE_H_DEFAULT as VIDEO_HEIGHT,
                    BASE_W_DEFAULT as VIDEO_WIDTH,
                    DEFAULT_SOUND_EFFECTS,
                    FONT_PATH_DEFAULT,
                    FONT_PATHS,
                    TRANSITION_PATHS,
                    SUBTITLE_BURNIN_TIMEOUT_DEFAULT,
                )
                from modules.utils.utils import safe_remove, download_image, clean_text
                from modules.audio.audio_processing import trascrivi_audio_parallel
            except ImportError:
                # Fallback imports if modules are not found
                print("Warning: Some configuration modules not found, using defaults")
                BACKGROUND_MUSIC_PATHS = []
                BACKGROUND_MUSIC_VOLUME = 0.3
                VIDEO_FPS = 24
                VIDEO_HEIGHT = 1080
                VIDEO_WIDTH = 1920
                DEFAULT_SOUND_EFFECTS = {}
                FONT_PATH_DEFAULT = ""
                FONT_PATHS = []
                TRANSITION_PATHS = []
                SUBTITLE_BURNIN_TIMEOUT_DEFAULT = 30
    except ImportError:
        # Final fallback - use defaults
        pass

logger = logging.getLogger(__name__)

# Funzioni specifiche che rimangono in questo file
def get_year_from_date_text(date_text_input: Any) -> str:
    """
    Estrae l'anno a 4 cifre da una stringa di data, se possibile.
    Restituisce la stringa originale se non viene identificato un anno valido.
    """
    if not isinstance(date_text_input, str):
        return str(date_text_input)

    for delimiter in ['/', '-', '.']:
        if delimiter in date_text_input:
            parts = date_text_input.split(delimiter)
            potential_year = parts[-1].strip()
            if potential_year.isdigit() and len(potential_year) == 4:
                year_value = int(potential_year)
                if 1000 <= year_value < 3000:
                    return potential_year

    match = re.search(r'\b(\d{4})\b', date_text_input)
    if match:
        potential_year = match.group(1)
        year_value = int(potential_year)
        if 1000 <= year_value < 3000:
            return potential_year

    cleaned_input = date_text_input.strip()
    if cleaned_input.isdigit() and len(cleaned_input) == 4:
        year_value = int(cleaned_input)
        if 1000 <= year_value < 3000:
            return cleaned_input

    return date_text_input


def _find_entity_timestamps(
    transcription_segments: List[Dict[str, Any]],
    entity_text: str
) -> Optional[Tuple[float, float]]:
    """
    Finds start and end timestamps for a given entity text within transcription segments.
    """
    import re
    
    # Helper function to get text from segment (handle both dict and object formats)
    def get_seg_text(seg):
        if hasattr(seg, 'text'):
            return seg.text
        elif isinstance(seg, dict) and 'text' in seg:
            return seg['text']
        else:
            return str(seg)
    
    # Helper function to get start time from segment
    def get_seg_start(seg):
        if hasattr(seg, 'start'):
            return seg.start
        elif isinstance(seg, dict) and 'start' in seg:
            return seg['start']
        else:
            return 0.0
    
    # Helper function to get end time from segment
    def get_seg_end(seg):
        if hasattr(seg, 'end'):
            return seg.end
        elif isinstance(seg, dict) and 'end' in seg:
            return seg['end']
        else:
            return 0.0
    
    full_text = " ".join([get_seg_text(seg) for seg in transcription_segments])
    
    # Cerca l'entità nel testo completo
    match = re.search(re.escape(entity_text), full_text, re.IGNORECASE)
    if not match:
        return None

    start_char_index = match.start()
    end_char_index = match.end()
    
    start_time, end_time = -1.0, -1.0
    
    current_char_pos = 0
    for seg in transcription_segments:
        seg_text = get_seg_text(seg)
        seg_len = len(seg_text)
        
        # Controlla se l'inizio dell'entità è in questo segmento
        if start_time < 0 and current_char_pos <= start_char_index < current_char_pos + seg_len:
            start_time = get_seg_start(seg)
            
        # Controlla se la fine dell'entità è in questo segmento
        if end_time < 0 and current_char_pos <= end_char_index <= current_char_pos + seg_len:
            end_time = get_seg_end(seg)
            break # Trovato l'intervallo completo
            
        current_char_pos += seg_len + 1 # +1 per lo spazio

    if start_time >= 0 and end_time >= 0:
        return start_time, end_time
        
    return None

# Esporta tutte le funzioni principali per compatibilità
__all__ = [
    # Core functions
    'validate_audio_stream',
    'create_safe_audio_clip', 
    'safe_moviepy_write_videofile_pure',
    'safe_moviepy_write_videofile',
    'get_cross_platform_subprocess_env',
    'sanitize_ffmpeg_path',
    'sanitize_text_for_filename',
    'create_safe_temp_filename',
    'safe_moviepy_operation',
    'aggressive_memory_cleanup',
    'safe_close_moviepy_object',
    'cleanup_ffmpeg_processes',
    'test_ffmpeg_subprocess_health',
    'comprehensive_moviepy_cleanup',
    'close_clip',
    'hard_cleanup',
    'log_mem',
    'force_gc_and_cleanup',
    '_validate_resources',
    'has_video_stream',
    'get_clip_duration',
    'extract_audio_from_video',
    'get_video_info',
    'create_video_preview',
    
    # FFmpeg functions
    'get_audio_duration_ffmpeg',
    'get_audio_codec_ffmpeg',
    'run_ffmpeg_command',
    'merge_overlays_ffmpeg',
    'format_timestamp',
    
    # Clip functions
    'concatenate_video_clips',
    'resize_video_to_standard',
    'loop_video_clip',
    'loop_audio_clip',
    'apply_gaussian_blur',
    
    # Effects functions
    'add_single_sound_effect_to_clip',
    'add_special_names_sound_effect',
    'create_transition_clip',
    'create_random_transition_with_sound',
    'add_background_music',
    
    # Overlay functions
    'make_text_image_date',
    'create_text_overlay_clip',
    'create_image_overlay_clip',
    'create_typewriter_effect_clip',
    'crea_effetto_typewriting_nomi',
    'create_subtitle_overlay',
    'create_logo_watermark',
    'apply_text_animation',
    'make_important_words_image',
    
    # Audio functions
    'get_whisper_model',
    'detect_clip_language_safe',
    'transcribe_audio_with_whisper',
    'translate_text_segments',
    'create_srt_file',
    'extract_audio_from_video_clip',
    'normalize_audio_levels',
    'apply_audio_fade',
    'mix_audio_tracks',
    'create_silence_clip',
    'analyze_audio_properties',
    
    # Generation functions removed due to circular import
    # These should be imported directly from video_generation where needed
    
    # Utility functions
    'get_year_from_date_text',
    '_find_entity_timestamps'
]
