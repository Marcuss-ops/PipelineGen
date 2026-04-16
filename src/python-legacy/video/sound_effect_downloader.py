"""Google Drive sound effects downloader.
Downloads missing sound effects from Google Drive URLs.
Designed to be scalable across multiple computers by using relative paths and automatic downloads.
"""

import os
import logging
import requests
from pathlib import Path
import json
import sys
from typing import Dict, List, Optional, Tuple, Callable

logger = logging.getLogger(__name__)

def download_file_from_google_drive(file_id, destination):
    """Download a file from Google Drive using file ID."""
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
    with open(destination, "wb") as f:
        for chunk in response.iter_content(CHUNK_SIZE):
            if chunk:
                f.write(chunk)

def extract_file_id_from_url(url):
    """Extract file ID from Google Drive URL."""
    if '/file/d/' in url:
        return url.split('/file/d/')[1].split('/')[0]
    elif 'id=' in url:
        return url.split('id=')[1].split('&')[0]
    return None

def get_sound_effects_dir() -> Path:
    """Get the directory for sound effects, creating it if it doesn't exist."""
    # Define a standard location relative to the application directory
    app_dir = Path(os.path.dirname(os.path.abspath(__file__)))
    sound_effects_dir = app_dir / "assets" / "sound_effects"
    
    # Create the directory if it doesn't exist
    os.makedirs(sound_effects_dir, exist_ok=True)
    
    return sound_effects_dir

def get_sound_effects_mapping() -> Dict[str, str]:
    """Get the mapping of sound effect filenames to Google Drive file IDs."""
    # Define the mapping with relative paths
    return {
        "Best SFX Sound Effects Catalog to Download Artlist-01.m4a": "1-RJ9eGyp4EajoSWTh1Zyu5-84qssJgGK",
        "Cameraman Vol 2 by Soundkrampf SFX - Artlist.m4a": "10H1Xt4DuzFdVeNj_uiiIyRWjIu1yn_x2",
        "Creator Kit by Dauzkobza SFX - Artlist.m4a": "1Cil5O692o9xVaig-1-yXWEebQVpzXsgN",
        "Dauzkobza - Creator Kit - Camera Snapshot 01.wav": "1ElFtPZk4vD4CKZjWQRblaGb9HIKFSWjf",
        "Dauzkobza - Creator Kit - Camera Snapshot 02.wav": "1GdRfSZBWHwvReY2Kin6ovh4EPXKIBVzi",
        "Dauzkobza - Creator Kit - Camera Snapshot Shutter Mech-1.wav": "1Kf7ezQRgo5f-EjQl-DfSWNq6gW7WKkMU",
        "Dauzkobza - Creator Kit - Digital Camera Snapshot 02-1.wav": "1T0V9HHGUobjUDOpEQhCVTGdefEKPSRnN",
        "Dauzkobza - Creator Kit - Resonating Camera Snapshot-1.wav": "1aC9Q1jGZi0-jnRD_SIePj_KAleiZO4a_",
        "Dauzkobza - Creator Kit - Snappy Camera Shutter-1.wav": "1axKII_oPdNf2QMnTL07tjCTPX1p2O_Gx",
        "Infographics - Stereo Whoosh Whooshing Royalty Free Sound Effect.m4a": "1d7xjnP50oCRffXTq0qmbxdGsdoryzs1U",
        "Soundkrampf - Cameraman - Camera Flash Closing.wav": "1lo-8m-uVX6ew74-8OzqyjESH2hPNGCz4",
        "Soundkrampf - Cameraman - Flash Pop Open.wav": "1nZ5BEkPDaeGWaMzo7cQnAA7tu8h1ck90",
        "Soundkrampf - Cameraman - Shutter Fast One Shot Taking a Picture.wav": "1nyizk-imxjK5rgidNk0yckUYVIpr0ic7"
    }

def update_config_sound_effects(sound_effects_paths: List[str]) -> None:
    """Update the config module with the new sound effect paths."""
    try:
        # Import the config module dynamically
        import importlib
        import config
        
        # Update the DEFAULT_SOUND_EFFECTS and CATEGORIZED_SOUND_EFFECTS
        config.DEFAULT_SOUND_EFFECTS = sound_effects_paths
        
        # Update categorized sound effects
        camera_effects = [path for path in sound_effects_paths if "Camera" in path or "camera" in path]
        whoosh_effects = [path for path in sound_effects_paths if "Whoosh" in path or "whoosh" in path]
        
        config.CATEGORIZED_SOUND_EFFECTS = {
            "transition": sound_effects_paths,
            "general": sound_effects_paths,
            "camera": camera_effects,
            "whoosh": whoosh_effects
        }
        
        print("Updated config with new sound effect paths.")
    except ImportError:
        print("Warning: Could not import config module to update sound effect paths.")
    except Exception as e:
        print(f"Error updating config: {str(e)}")

def save_sound_effects_paths(sound_effects_paths: List[str]) -> None:
    """Save the sound effects paths to a JSON file for persistence."""
    sound_effects_dir = get_sound_effects_dir()
    paths_file = sound_effects_dir / "sound_effects_paths.json"
    
    try:
        with open(paths_file, 'w') as f:
            json.dump(sound_effects_paths, f, indent=2)
        print(f"Saved sound effects paths to {paths_file}")
    except Exception as e:
        print(f"Error saving sound effects paths: {str(e)}")

def load_sound_effects_paths() -> List[str]:
    """Load the sound effects paths from a JSON file if it exists."""
    sound_effects_dir = get_sound_effects_dir()
    paths_file = sound_effects_dir / "sound_effects_paths.json"
    
    if paths_file.exists():
        try:
            with open(paths_file, 'r') as f:
                return json.load(f)
        except Exception as e:
            print(f"Error loading sound effects paths: {str(e)}")
    
    return []

def check_and_download_sound_effects() -> List[str]:
    """Check if sound effects exist and download missing ones.
    Returns a list of all available sound effect paths.
    """
    return check_and_download_sound_effects_with_logging()


def check_and_download_sound_effects_with_logging(
    status_callback: Optional[Callable[[str, bool], None]] = None,
    label: str = "[SFX]",
    quiet: bool = True,
) -> List[str]:
    """Check if sound effects exist and download missing ones.

    - If `quiet=True`, avoids printing per-file progress to stdout.
    - If `status_callback` is provided, emits only a few high-signal status messages.

    Returns a list of all sound effect absolute paths (including ones that failed to download).
    """
    sound_effects_dir = get_sound_effects_dir()
    sound_effects_mapping = get_sound_effects_mapping()
    
    # Try to load existing paths first
    existing_paths = load_sound_effects_paths()
    if existing_paths and all(os.path.exists(path) for path in existing_paths):
        msg = f"{label} ✅ Effetti sonori già disponibili: {len(existing_paths)}"
        logger.info(msg)
        if status_callback:
            status_callback(msg, False)
        return existing_paths
    
    # Download all sound effects
    downloaded_count = 0
    sound_effects_paths = []
    missing_files = []
    
    for filename, file_id in sound_effects_mapping.items():
        # Create the full path for the sound effect
        file_path = str(sound_effects_dir / filename)
        sound_effects_paths.append(file_path)
        
        # Check if the file already exists
        if os.path.exists(file_path):
            if not quiet:
                logger.info("%s Sound effect already exists: %s", label, filename)
            continue
        
        missing_files.append(filename)
        if not quiet:
            logger.info("%s Downloading sound effect: %s", label, filename)
        
        try:
            download_file_from_google_drive(file_id, file_path)
            if not quiet:
                logger.info("%s Successfully downloaded: %s", label, filename)
            downloaded_count += 1
        except Exception as e:
            logger.warning("%s Failed to download %s: %s", label, filename, str(e))
    
    if downloaded_count > 0:
        msg = f"{label} ✅ Scaricati {downloaded_count} effetti sonori"
        logger.info(msg)
        if status_callback:
            status_callback(msg, False)
    else:
        if missing_files:
            msg = f"{label} ⚠️ Nessun effetto sonoro scaricato (mancanti: {len(missing_files)})"
            logger.warning(msg)
            if status_callback:
                status_callback(msg, True)
    
    # Save the paths for future use
    save_sound_effects_paths(sound_effects_paths)
    
    # Update the config module with the new paths
    update_config_sound_effects(sound_effects_paths)
    
    return sound_effects_paths

def find_system_sound_effects() -> List[str]:
    """Search for sound effects in common system locations."""
    system_paths = []
    
    # Define common locations to search for sound effects
    common_locations = [
        # Windows locations
        os.path.expanduser("~\\Documents"),
        os.path.expanduser("~\\Music"),
        os.path.expanduser("~\\OneDrive\\Documenti"),
        # Add more common locations as needed
    ]
    
    # Get filenames we're looking for
    sound_effects_mapping = get_sound_effects_mapping()
    filenames = list(sound_effects_mapping.keys())
    
    print("Searching for sound effects in system locations...")
    
    for location in common_locations:
        if not os.path.exists(location):
            continue
            
        for root, _, files in os.walk(location):
            for filename in files:
                if filename in filenames:
                    full_path = os.path.join(root, filename)
                    system_paths.append(full_path)
                    print(f"Found system sound effect: {full_path}")
    
    return system_paths

def get_all_available_sound_effects() -> List[str]:
    """Get all available sound effects, combining downloaded and system files."""
    # First check and download missing sound effects
    downloaded_paths = check_and_download_sound_effects_with_logging()
    
    # Then search for sound effects in system locations
    system_paths = find_system_sound_effects()
    
    # Combine and deduplicate paths (keeping the first occurrence)
    all_paths = []
    seen_filenames = set()
    
    for path in downloaded_paths + system_paths:
        filename = os.path.basename(path)
        if filename not in seen_filenames and os.path.exists(path):
            all_paths.append(path)
            seen_filenames.add(filename)
    
    logger.info("Total available sound effects: %s", len(all_paths))
    
    # Update config with all available paths
    update_config_sound_effects(all_paths)
    
    return all_paths

if __name__ == "__main__":
    get_all_available_sound_effects()