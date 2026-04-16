"""Google Drive transitions downloader.
Downloads missing transitions from Google Drive URLs.
Designed to be scalable across multiple computers by using relative paths and automatic downloads.
"""

import os
import requests
from pathlib import Path
import json
import sys
from typing import Dict, List, Optional, Tuple

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

def get_transitions_dir() -> Path:
    """Get the directory for transitions, creating it if it doesn't exist."""
    # Define a standard location relative to the application directory
    app_dir = Path(os.path.dirname(os.path.abspath(__file__)))
    transitions_dir = app_dir / "assets" / "transitions"
    
    # Create the directory if it doesn't exist
    os.makedirs(transitions_dir, exist_ok=True)
    
    return transitions_dir

def get_transitions_mapping() -> Dict[str, str]:
    """Get the mapping of transition filenames to Google Drive file IDs.
    This function should be modified to load mappings from a configuration file instead of hardcoding.
    """
    # Google Drive mapping for transitions download
    return {
        # Nuove transizioni
        "tra1.mp4": "1CONoSOSoKfXvPItgme5AFKiaJBkBSgyy",
        "tra2.mp4": "1GSakJuy-oSNtg-4HpWEVVZw4l8T8-HX1",
        "tra3.mp4": "1KWydO5qRMePy81Z5ihtHsZpAcibSnmCU",
        "tra4.mp4": "1MskYpTMorW4bIf_UtN0ax6CbWb-gGqNb",
        "tra5.mp4": "1O6OvBnUSaOZ_4oCfeKGze9rfWJ-tBgf5",
        "tra6.mp4": "1Rix7AbafrXU8Ejn7vSVL5KToPw4Xn924",
        "tra7.mp4": "1aJv_CJc1RKTkPLsIyGFNjXTchxqTwLhU",
        "tra8.mp4": "1gwTvvtto6HlFZ6XXkKyZkRZvs1K3FqH_",
        "tra9.mp4": "1yoJALffW6IGeCx2xVqnou3FqV-Fj29mr",
    }

def update_config_transitions(transition_paths: List[str]) -> None:
    """Update the config module with the new transition paths."""
    try:
        # Import the config module dynamically
        import importlib
        import config
        
        # Update the TRANSITION_PATHS
        config.TRANSITION_PATHS = transition_paths
        
        print("Updated config with new transition paths.")
    except ImportError:
        print("Warning: Could not import config module to update transition paths.")
    except Exception as e:
        print(f"Error updating config: {str(e)}")

def save_transition_paths(transition_paths: List[str]) -> None:
    """Save the transition paths to a JSON file for persistence."""
    transitions_dir = get_transitions_dir()
    paths_file = transitions_dir / "transition_paths.json"
    
    try:
        with open(paths_file, 'w') as f:
            json.dump(transition_paths, f, indent=2)
        print(f"Saved transition paths to {paths_file}")
    except Exception as e:
        print(f"Error saving transition paths: {str(e)}")

def load_transition_paths() -> List[str]:
    """Load the transition paths from a JSON file if it exists."""
    transitions_dir = get_transitions_dir()
    paths_file = transitions_dir / "transition_paths.json"
    
    if paths_file.exists():
        try:
            with open(paths_file, 'r') as f:
                return json.load(f)
        except Exception as e:
            print(f"Error loading transition paths: {str(e)}")
    
    return []

def check_and_download_transitions() -> List[str]:
    """Check if transitions exist and download missing ones.
    Returns a list of all available transition paths.
    """
    transitions_dir = get_transitions_dir()
    transitions_mapping = get_transitions_mapping()
    
    # Try to load existing paths first
    existing_paths = load_transition_paths()
    
    # Check if all transitions in the mapping exist in the existing paths
    all_transitions_exist = True
    if existing_paths:
        existing_filenames = [os.path.basename(path) for path in existing_paths]
        for filename in transitions_mapping.keys():
            if filename not in existing_filenames:
                all_transitions_exist = False
                break
    else:
        all_transitions_exist = False
    
    if existing_paths and all_transitions_exist and all(os.path.exists(path) for path in existing_paths):
        print(f"All {len(existing_paths)} transitions already available.")
        return existing_paths
    
    # Download all transitions
    downloaded_count = 0
    transition_paths = []
    
    for filename, file_id in transitions_mapping.items():
        # Create the full path for the transition
        file_path = str(transitions_dir / filename)
        transition_paths.append(file_path)
        
        # Check if the file already exists
        if os.path.exists(file_path):
            print(f"Transition already exists: {filename}")
            continue
        
        print(f"Downloading transition: {filename}")
        
        try:
            download_file_from_google_drive(file_id, file_path)
            print(f"Successfully downloaded: {filename}")
            downloaded_count += 1
        except Exception as e:
            print(f"Failed to download {filename}: {str(e)}")
    
    if downloaded_count > 0:
        print(f"Downloaded {downloaded_count} transitions.")
    
    # Save the paths for future use
    save_transition_paths(transition_paths)
    
    # Update the config module with the new paths
    update_config_transitions(transition_paths)
    
    return transition_paths

def find_system_transitions() -> List[str]:
    """Search for transitions in common system locations and specific paths."""
    system_paths = []
    
    # Define common locations to search for transitions
    common_locations = [
        # Windows locations
        os.path.expanduser("~\\Documents"),
        os.path.expanduser("~\\Videos"),
        os.path.expanduser("~\\OneDrive\\Documenti"),
        # Add more common locations as needed
    ]
    
    # No specific paths hardcoded - will be discovered dynamically
    
    # Get filenames we're looking for
    transitions_mapping = get_transitions_mapping()
    filenames = list(transitions_mapping.keys())
    
    print("Searching for transitions in system locations...")
    
    for location in common_locations:
        if not os.path.exists(location):
            continue
            
        for root, _, files in os.walk(location):
            for filename in files:
                if filename in filenames:
                    full_path = os.path.join(root, filename)
                    system_paths.append(full_path)
                    print(f"Found system transition: {full_path}")
    
    return system_paths

def get_all_available_transitions() -> List[str]:
    """Get all available transitions, combining downloaded and system files."""
    # First check and download missing transitions
    downloaded_paths = check_and_download_transitions()
    
    # Then search for transitions in system locations
    system_paths = find_system_transitions()
    
    # Combine and deduplicate paths (keeping the first occurrence)
    all_paths = []
    seen_filenames = set()
    
    for path in downloaded_paths + system_paths:
        filename = os.path.basename(path)
        if filename not in seen_filenames and os.path.exists(path):
            all_paths.append(path)
            seen_filenames.add(filename)
    
    print(f"Total available transitions: {len(all_paths)}")
    
    # Save the paths for future use
    save_transition_paths(all_paths)
    
    # Update config with all available paths
    update_config_transitions(all_paths)
    
    return all_paths

if __name__ == "__main__":
    get_all_available_transitions()
