"""
Performance Optimized Configuration for Fast Video Generation
This file contains aggressive speed optimizations that prioritize generation speed over quality.
Use this when you need quick previews or fast iterations.
"""

import os

# Performance Settings
MAX_WORKERS_SEGMENT_GEN = max(4, os.cpu_count())  # Use all CPU cores
MAX_WORKERS_GENERAL = max(8, os.cpu_count() * 2)  # Aggressive parallelization

# FFmpeg Speed Optimizations
FFMPEG_SPEED_PRESETS = {
    'ultra_fast': {
        'preset': 'medium',
        'crf': 28,  # Lower quality for speed
        'threads': 0,  # Use all CPU cores
        'tune': 'fastdecode',
    },
    'fast': {
        'preset': 'superfast',
        'crf': 25,
        'threads': 0,
        'tune': 'fastdecode',
    },
    'balanced': {
        'preset': 'veryfast',
        'crf': 23,
        'threads': max(4, os.cpu_count() // 2),
    }
}

# Cache Settings for Speed
CACHE_SETTINGS = {
    'enable_transcription_cache': True,
    'enable_audio_cache': True,
    'enable_video_cache': True,
    'cache_compression': False,  # Skip compression for speed
    'cache_cleanup': False,  # Skip cleanup during generation
}

# GPU Acceleration Settings
GPU_SETTINGS = {
    'use_gpu': True,
    'gpu_codec': 'h264_nvenc',
    'gpu_preset': 'fast',
    'gpu_quality': 28,  # Lower quality for speed
}

# Memory Management
MEMORY_SETTINGS = {
    'max_memory_usage': 0.8,  # Use 80% of available RAM
    'enable_memory_monitoring': False,  # Skip monitoring overhead
    'aggressive_cleanup': True,  # Clean memory aggressively
}

# Quick Preview Mode
QUICK_PREVIEW = {
    'enabled': True,
    'reduce_resolution': True,  # Generate at 720p instead of 1080p
    'reduce_fps': False,  # Keep original FPS
    'skip_audio_processing': False,  # Keep audio processing
    'skip_effects': False,  # Keep effects
}

# Progress Reporting
PROGRESS_SETTINGS = {
    'enable_detailed_logging': False,  # Reduce logging overhead
    'show_progress_bar': True,
    'update_frequency': 0.1,  # Update every 10%
}