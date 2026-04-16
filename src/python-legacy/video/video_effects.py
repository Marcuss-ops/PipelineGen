"""
Video effects module - Central exports for video effects functions.
This module re-exports functions from various modules to maintain backwards compatibility.
"""

# Use absolute imports by default (works when imported directly or as part of package)
from modules.video.video_core import (
    comprehensive_moviepy_cleanup,
    hard_cleanup
)
from modules.video.video_clips import (
    create_transition_clip,
    create_random_transition_with_sound,
    add_single_sound_effect_to_clip
)
# Fallback function for add_special_names_sound_effect (placeholder)
def add_special_names_sound_effect(*args, **kwargs):
    """Fallback function for add_special_names_sound_effect"""
    if args:
        return args[0]
    return None

# Stub for add_background_music since it doesn't exist anywhere
def add_background_music(video_clip, music_path, volume=0.3):
    """
    Add background music to a video clip.
    
    Args:
        video_clip: VideoFileClip to add music to
        music_path: Path to the music file
        volume: Volume level for the music (0.0 to 1.0)
        
    Returns:
        VideoFileClip with background music added
    """
    try:
        from moviepy import AudioFileClip, CompositeAudioClip
        
        # Load music
        music = AudioFileClip(music_path)
        
        # Adjust volume
        music = music.multiply_volume(volume)
        
        # Loop music if shorter than video
        if music.duration < video_clip.duration:
            from moviepy import concatenate_audioclips
            loops_needed = int(video_clip.duration / music.duration) + 1
            music = concatenate_audioclips([music] * loops_needed).subclip(0, video_clip.duration)
        else:
            music = music.subclip(0, video_clip.duration)
        
        # Mix with existing audio if present
        if video_clip.audio:
            final_audio = CompositeAudioClip([video_clip.audio, music])
        else:
            final_audio = music
        
        # Set audio on video
        video_clip = video_clip.set_audio(final_audio)
        
        return video_clip
        
    except Exception as e:
        import logging
        logging.error(f"Error adding background music: {e}")
        return video_clip

# Export all functions
__all__ = [
    'comprehensive_moviepy_cleanup',
    'hard_cleanup',
    'create_transition_clip',
    'create_random_transition_with_sound',
    'add_single_sound_effect_to_clip',
    'add_special_names_sound_effect',
    'add_background_music'
]
