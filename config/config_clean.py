"""Configuration module for VideoCollegaTanti application."""

import os
from typing import Dict, Any, List
import subprocess
import sys
import json

# --- CONFIGURATION CONSTANTS ---
BACKGROUND_MUSIC_PATHS = [r"C:\Users\pater\OneDrive\Documenti\cupcaut\Background\FixedTransitions\MusicaFinita.MP3"]
BACKGROUND_MUSIC_VOLUME = 0.5
USE_GPU = True

# Video dimensions and frame rate defaults
BASE_FPS_DEFAULT = 30
BASE_W_DEFAULT = 1920
BASE_H_DEFAULT = 1080

# Date overlay defaults
DATE_FONT_SIZE_DEFAULT = 180           # Default font size for date overlays
DATE_TYPEWRITER_RATIO_DEFAULT = 0.15   # Portion of overlay duration used for typing (0..1)



USER_DATA_DIR = r"C:\Users\pater\AppData\Local\Google\Chrome\User Data"
PROFILE_DIRECTORY = "Profile 8"
TEMP_PROFILE_DIR = r"C:\Users\pater\Pyt\temp_chrome_profile"

FONT_PATH_DEFAULT = r"C:\Users\pater\OneDrive\Desktop\Montserrat\static\Montserrat-Black.ttf"
FONT_PATHS = [FONT_PATH_DEFAULT]
# FFmpeg timeout settings (in seconds)
FFMPEG_TIMEOUT_INTRO_DEFAULT = 120   # 2 minutes for intro clips
FFMPEG_TIMEOUT_MIDDLE_DEFAULT = 300  # 5 minutes for middle clips
SUBTITLE_BURNIN_TIMEOUT_DEFAULT = 300  # Increased from 180 to 300 seconds (5 minutes) for subtitle burn-in to prevent timeout

# Audio transcription timeout settings
TRANSCRIPTION_TIMEOUT_PER_CHUNK_DEFAULT = 60   # seconds per chunk
TRANSCRIPTION_TIMEOUT_BUFFER_DEFAULT = 120     # buffer seconds

# --- Directories ---
ASSETS_BASE_DIR = os.path.abspath("media_assets")
SOUND_EFFECTS_DIR = os.path.join(ASSETS_BASE_DIR, "effects")
FONTS_DIR = os.path.join(ASSETS_BASE_DIR, "fonts")
MUSIC_DIR = os.path.join(ASSETS_BASE_DIR, "music")
TRANSITIONS_DIR = os.path.join(ASSETS_BASE_DIR, "transitions")
# Directory per audio degli effetti video (nomi speciali, frasi importanti, etc.)
VIDEO_STYLE_AUDIO_DIR = os.path.join(os.path.dirname(__file__), "assets", "audio")

# Background music settings
BACKGROUND_MUSIC_PATHS = []  # vuoto, l'utente può aggiungere i propri file
BACKGROUND_MUSIC_VOLUME = 0.8

# GPU settings - Optimized for speed
if USE_GPU:
    DEFAULT_FFMPEG_PARAMS = [
        "-vcodec", "h264_nvenc", "-rc", "vbr", "-cq", "28",
        "-preset", "fast", "-acodec", "aac", "-strict", "-2",
        "-threads", "0"  # Use all available CPU cores
    ]
    MOVIEPY_NVENC_PARAMS = {
        "codec": "h264_nvenc",
        "ffmpeg_params": ["-rc", "vbr", "-cq", "28", "-preset", "fast", "-threads", "0"]
    }
else:
    DEFAULT_FFMPEG_PARAMS = [
        "-vcodec", "libx264", "-crf", "28", "-preset", "medium",
        "-acodec", "aac", "-strict", "-2",
        "-threads", "0"  # Use all available CPU cores
    ]
    MOVIEPY_NVENC_PARAMS = None

# Initialize FONT_PATHS as empty list, will be populated by font_downloader
FONT_PATHS = []
FONT_PATH_DEFAULT = None

# Try to load font paths from JSON file if it exists
try:
    font_paths_file = os.path.join(FONTS_DIR, "font_paths.json")
    if os.path.exists(font_paths_file):
        with open(font_paths_file, 'r') as f:
            FONT_PATHS = json.load(f)
            if FONT_PATHS and os.path.exists(FONT_PATHS[0]):
                FONT_PATH_DEFAULT = FONT_PATHS[0]
except Exception as e:
    print(f"Error loading font paths from JSON: {str(e)}")

# Font download information moved to font_downloader.py
SOUND_EFFECT_FILES = [
    "Best SFX Sound Effects Catalog to Download Artlist-01.m4a",
    "Cameraman Vol 2 by Soundkrampf SFX - Artlist.m4a",
    "Creator Kit by Dauzkobza SFX - Artlist.m4a",
    "Dauzkobza - Creator Kit - Camera Snapshot 01.wav",
    "Dauzkobza - Creator Kit - Camera Snapshot 02.wav",
    "Dauzkobza - Creator Kit - Camera Snapshot Shutter Mech-1.wav",
    "Dauzkobza - Creator Kit - Digital Camera Snapshot 02-1.wav",
    "Dauzkobza - Creator Kit - Resonating Camera Snapshot-1.wav",
    "Dauzkobza - Creator Kit - Snappy Camera Shutter-1.wav",
    "Infographics - Stereo Whoosh Whooshing Royalty Free Sound Effect.m4a",
    "Soundkrampf - Cameraman - Camera Flash Closing.wav",
    "Soundkrampf - Cameraman - Flash Pop Open.wav",
    "Soundkrampf - Cameraman - Shutter Fast One Shot Taking a Picture.wav"
]

DEFAULT_SOUND_EFFECTS = [os.path.join(SOUND_EFFECTS_DIR, fname) for fname in SOUND_EFFECT_FILES]

# Categorized sound effects for different contexts
SOUND_EFFECTS_CATEGORIES = {
    "camera": [  # Camera/photo related sounds
        "Dauzkobza - Creator Kit - Camera Snapshot 01.wav",
        "Dauzkobza - Creator Kit - Camera Snapshot 02.wav",
        "Dauzkobza - Creator Kit - Camera Snapshot Shutter Mech-1.wav",
        "Dauzkobza - Creator Kit - Digital Camera Snapshot 02-1.wav",
        "Dauzkobza - Creator Kit - Resonating Camera Snapshot-1.wav",
        "Dauzkobza - Creator Kit - Snappy Camera Shutter-1.wav",
        "Soundkrampf - Cameraman - Camera Flash Closing.wav",
        "Soundkrampf - Cameraman - Flash Pop Open.wav",
        "Soundkrampf - Cameraman - Shutter Fast One Shot Taking a Picture.wav"
    ],
    "transition": [  # Transition/movement sounds
        "Infographics - Stereo Whoosh Whooshing Royalty Free Sound Effect.m4a","Dauzkobza - Creator Kit - Camera Snapshot 01.wav",
        "Dauzkobza - Creator Kit - Camera Snapshot 02.wav",
        "Dauzkobza - Creator Kit - Camera Snapshot Shutter Mech-1.wav",
        "Dauzkobza - Creator Kit - Digital Camera Snapshot 02-1.wav",
        "Dauzkobza - Creator Kit - Resonating Camera Snapshot-1.wav",
        "Dauzkobza - Creator Kit - Snappy Camera Shutter-1.wav",
        "Soundkrampf - Cameraman - Camera Flash Closing.wav",
        "Soundkrampf - Cameraman - Flash Pop Open.wav",
        "Soundkrampf - Cameraman - Shutter Fast One Shot Taking a Picture.wav"
        
    ],
    "special_names": [  # Special sound effect for special names (Nomi_Speciali)
            "Soundkrampf - Cameraman - Camera Flash Closing.wav",
            "Soundkrampf - Cameraman - Flash Pop Open.wav",
            "Soundkrampf - Cameraman - Shutter Fast One Shot Taking a Picture.wav"

    ],
    "general": [  # General purpose effects
        "Cameraman Vol 2 by Soundkrampf SFX - Artlist.m4a",
        "Creator Kit by Dauzkobza SFX - Artlist.m4a",
        "Best SFX Sound Effects Catalog to Download Artlist-01.m4a",
        "Cameraman Vol 2 by Soundkrampf SFX - Artlist.m4a",
        "Creator Kit by Dauzkobza SFX - Artlist.m4a",
        "Dauzkobza - Creator Kit - Camera Snapshot 01.wav",
        "Dauzkobza - Creator Kit - Camera Snapshot 02.wav",
        "Dauzkobza - Creator Kit - Camera Snapshot Shutter Mech-1.wav",
        "Dauzkobza - Creator Kit - Digital Camera Snapshot 02-1.wav",
        "Dauzkobza - Creator Kit - Resonating Camera Snapshot-1.wav",
        "Dauzkobza - Creator Kit - Snappy Camera Shutter-1.wav",
        "Infographics - Stereo Whoosh Whooshing Royalty Free Sound Effect.m4a",
        "Soundkrampf - Cameraman - Camera Flash Closing.wav",
        "Soundkrampf - Cameraman - Flash Pop Open.wav",
        "Soundkrampf - Cameraman - Shutter Fast One Shot Taking a Picture.wav"
    ]
}


# Build categorized sound effect paths
CATEGORIZED_SOUND_EFFECTS = {
    category: [os.path.join(SOUND_EFFECTS_DIR, fname) for fname in files]
    for category, files in SOUND_EFFECTS_CATEGORIES.items()
}

# Sound effect configuration
SOUND_EFFECT_CONFIG = {
    "frequency": 2,  # Add sound effect every N segments (changed from 3 to 2 for more frequent effects)
    "volume": 0.25,  # Sound effect volume reduced to prevent overpowering voiceover (0.0 to 1.0)
    "max_duration": 3.0,  # Maximum sound effect duration in seconds
    "prefer_category": "camera",  # Preferred category for stock footage
    "random_timing": True,  # If True, vary the start time of sound effects
    "timing_variance": 2.0  # Maximum seconds to vary the start time
}

# Special configuration for special names (Nomi_Speciali)
SPECIAL_NAMES_SOUND_CONFIG = {
    "enabled": True,  # Enable special sound effect for special names
    "volume": 0.8,  # Higher volume for special names
    "start_time": 0.0,  # Always start at the beginning
    "max_duration": 5.0,  # Allow longer duration for impact
    "category": "special_names"  # Use the special_names category
}


SOUND_EFFECTS_GOOGLE_DRIVE = {
    "Best SFX Sound Effects Catalog to Download Artlist-01.m4a": "1-RJ9eGyp4EajoSWTh1Zyu5-84qssJgGK",
    "Cameraman Vol 2 by Soundkrampf SFX - Artlist.m4a": "10H1Xt4DuzFdVeNj_uiiIyRWjIu1yn_x2",
    "Creator Kit by Dauzkobza SFX - Artlist.m4a": "1Cil5O692o9xVaig-1-yXWEebQVpzXsgN",
    "Dauzkobza - Creator Kit - Camera Snapshot 01.wav": "1ElFtPZk4vD4CKZjWQRblaGb9HIKFSWjf",
    "Dauzkobza - Creator Kit - Camera Snapshot 02.wav": "1GdRfSZBWHwvReY2Kin6ovh4EPXKIBVzi",
    "Dauzkobza - Creator Kit - Camera Snapshot Shutter Mech-1.wav": "1Kf7ezQRgo5f-EjQl-DfSWNq6gW7WKkMU",
    "Dauzkobza - Creator Kit - Digital Camera Snapshot 02-1.wav": "1T0V9HHGUobjUDOpEQhCVTGdefEKPSRnN",
    "Dauzkobza - Creator Kit - Resonating Camera Snapshot-1.wav": "1MlDgulzJ9dXJmMvT81bs6pACEfv5O3fI",
    "Dauzkobza - Creator Kit - Snappy Camera Shutter-1.wav": "1Vo0EmHIheuziJnHgw3pJ7kEO-L-tfjBP",
    "Infographics - Stereo Whoosh Whooshing Royalty Free Sound Effect.m4a": "1d7xjnP50oCRffXTq0qmbxdGsdoryzs1U",
    "Soundkrampf - Cameraman - Camera Flash Closing.wav": "1lo-8m-uVX6ew74-8OzqyjESH2hPNGCz4",
    "Soundkrampf - Cameraman - Flash Pop Open.wav": "1nZ5BEkPDaeGWaMzo7cQnAA7tu8h1ck90",
    "Soundkrampf - Cameraman - Shutter Fast One Shot Taking a Picture.wav": "1nyizk-imxjK5rgidNk0yckUYVIpr0ic7",
    # Sound effects per nomi speciali / parole importanti (nuovi e vecchi)
    "nomi_speciali_typewriter.mp3": "1Yaqar7xgrSMebxUt-uxA3D_KfcJzRe6-",  # Vecchio audio per typewriter nomi speciali
    "parole_importanti.mp3": "1pXwvpZXUO9jFr6mObIDlONC4wxU29D1C",  # Nuovo audio per parole importanti (nomi speciali)
    "frasi_importanti.mp3": "1qacZAEpPxCMG9nPRfjoqjazPvOpAhYzd"  # Nuovo audio per frasi importanti
}

def download_file_from_google_drive(file_id, destination):
    """Download a file from Google Drive using file ID."""
    import requests
    
    URL = "https://docs.google.com/uc?export=download"
    
    session = requests.Session()
    response = session.get(URL, params={'id': file_id}, stream=True)
    
    # Handle the Google Drive virus scan warning
    token = None
    for key, value in response.cookies.items():
        if key.startswith('download_warning'):
            token = value
            break
    
    if token:
        params = {'id': file_id, 'confirm': token}
        response = session.get(URL, params=params, stream=True)
    
    # Save the file
    CHUNK_SIZE = 32768
    os.makedirs(os.path.dirname(destination), exist_ok=True)
    with open(destination, "wb") as f:
        for chunk in response.iter_content(CHUNK_SIZE):
            if chunk:
                f.write(chunk)

def check_and_download_missing_sound_effects():
    """Check if sound effects exist and download missing ones from Google Drive."""
    missing_count = 0
    downloaded_count = 0
    os.makedirs(SOUND_EFFECTS_DIR, exist_ok=True)
    
    for file_path in DEFAULT_SOUND_EFFECTS:
        if not os.path.exists(file_path):
            missing_count += 1
            file_name = os.path.basename(file_path)
            
            # Check if we have a Google Drive mapping for this file
            if file_name in SOUND_EFFECTS_GOOGLE_DRIVE:
                file_id = SOUND_EFFECTS_GOOGLE_DRIVE[file_name]
                print(f"🔄 Downloading missing sound effect: {os.path.basename(file_path)}")
                
                try:
                    download_file_from_google_drive(file_id, file_path)
                    print(f"✅ Successfully downloaded: {os.path.basename(file_path)}")
                    downloaded_count += 1
                except Exception as e:
                    print(f"❌ Failed to download {os.path.basename(file_path)}: {str(e)}")
            else:
                print(f"⚠️  No download URL found for: {os.path.basename(file_path)}")
    
    # Check and download video style audio files (nomi speciali, frasi importanti, etc.)
    os.makedirs(VIDEO_STYLE_AUDIO_DIR, exist_ok=True)
    video_style_audio_files = [
        "nomi_speciali_typewriter.mp3",
        "parole_importanti.mp3",
        "frasi_importanti.mp3"
    ]
    
    for audio_file in video_style_audio_files:
        audio_path = os.path.join(VIDEO_STYLE_AUDIO_DIR, audio_file)
        if not os.path.exists(audio_path):
            if audio_file in SOUND_EFFECTS_GOOGLE_DRIVE:
                file_id = SOUND_EFFECTS_GOOGLE_DRIVE[audio_file]
                print(f"🔄 Downloading missing video style audio: {audio_file}")
                try:
                    download_file_from_google_drive(file_id, audio_path)
                    print(f"✅ Successfully downloaded: {audio_file}")
                    downloaded_count += 1
                except Exception as e:
                    print(f"❌ Failed to download {audio_file}: {str(e)}")
            else:
                print(f"⚠️  No download URL found for: {audio_file}")
    
    if downloaded_count > 0:
        print(f"🎵 Downloaded {downloaded_count} missing sound effects.")
    
    if missing_count > 0 and downloaded_count == 0:
        print(f"⚠️  {missing_count} sound effects are missing but could not be downloaded.")
    
    return downloaded_count

# Font download functionality moved to font_downloader.py

# Transition paths - inizializzato vuoto, verrà popolato da transition_downloader.py
TRANSITION_PATHS = []

# Carica i percorsi delle transizioni da file JSON se esiste
transition_paths_json = os.path.join(TRANSITIONS_DIR, "transition_paths.json")
if os.path.exists(transition_paths_json):
    try:
        with open(transition_paths_json, 'r') as f:
            TRANSITION_PATHS = json.load(f)
            print(f"Loaded {len(TRANSITION_PATHS)} transition paths from JSON file.")
    except Exception as e:
        print(f"Error loading transition paths from JSON: {str(e)}")

# Auto-check and download missing sound effects, fonts and transitions on import
try:
    check_and_download_missing_sound_effects()
    
    # Import and execute font downloader
    try:
        from font_downloader import get_all_available_fonts
        # Esegui il downloader e aggiorna FONT_PATHS con i risultati
        FONT_PATHS = get_all_available_fonts()
        if FONT_PATHS and os.path.exists(FONT_PATHS[0]):
            FONT_PATH_DEFAULT = FONT_PATHS[0]
    except ImportError:
        print("Warning: font_downloader module not found. Fonts may not be available.")
    except Exception as e:
        print(f"Warning: Could not download fonts: {e}")
    
    # Import and execute transition downloader
    try:
        from transition_downloader import get_all_available_transitions
        # Esegui il downloader e aggiorna TRANSITION_PATHS con i risultati
        TRANSITION_PATHS = get_all_available_transitions()
    except ImportError:
        print("Warning: transition_downloader module not found. Transitions may not be available.")
    except Exception as e:
        print(f"Warning: Could not download transitions: {e}")
except Exception as e:
    print(f"Warning: Could not check/download assets: {e}")


class ConfigWrapper:
    """Configuration wrapper class for managing application settings."""
    
    def __init__(self, settings_dict: Dict[str, Any]):
        self._settings = settings_dict
        self._defaults = {
            "min_date_duration": 2.0,
            "date_font_size": 90,
            "text_color": "white",
            "nomi_font_size": 70,
            "frasi_font_size": 50,  # Increased from 30 to 50 for better visibility
            "min_frasi_duration": 1.0,
            "parole_font_size": 60,
            "parole_text_color": "yellow",
            "text_overlay_delay": 0.3,
            "image_overlay_position": "center",
            "image_fade_duration": 0.5,
            "image_overlay_default_duration": 3.0,
            "image_overlay_min_duration": 1.0,
            "image_overlay_max_duration": 7.0,
            "ffmpeg_timeout_intro": FFMPEG_TIMEOUT_INTRO_DEFAULT,
            "ffmpeg_timeout_middle": FFMPEG_TIMEOUT_MIDDLE_DEFAULT,
            "transcription_timeout_per_chunk": TRANSCRIPTION_TIMEOUT_PER_CHUNK_DEFAULT,
            "transcription_timeout_buffer": TRANSCRIPTION_TIMEOUT_BUFFER_DEFAULT,
            # Sound effect configuration
            "sound_effect_frequency": 7,  # Add sound effect every N segments (changed from 3 to 7)
            "sound_effect_volume": 0.9,  # Sound effect volume
            "sound_effect_enable_middle_clips": True,  # Enable sound effects in middle clips
            "sound_effect_enable_stock_clips": True   # Enable sound effects in stock clips
        }
    
    def get(self, key: str, default_override=None):
        """Get configuration value with fallback to defaults."""
        if key in self._settings:
            return self._settings[key]
        if default_override is not None:
            return self._settings.get(key, default_override)
        return self._settings.get(key, self._defaults.get(key))
