"""Google Drive fonts downloader.
Downloads missing fonts from Google Drive URLs.
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

def get_fonts_dir() -> Path:
    """Get the directory for fonts, creating it if it doesn't exist."""
    # Define a standard location relative to the application directory
    app_dir = Path(os.path.dirname(os.path.abspath(__file__)))
    fonts_dir = app_dir / "assets" / "fonts"
    
    # Create the directory if it doesn't exist
    os.makedirs(fonts_dir, exist_ok=True)
    
    return fonts_dir

def download_montserrat_from_google_fonts(fonts_dir: Path, variant: str = "Bold") -> Optional[str]:
    """Download Montserrat font from Google Fonts directly."""
    try:
        font_name = f"Montserrat-{variant}"
        font_path = fonts_dir / f"{font_name}.ttf"
        
        if font_path.exists():
            return str(font_path)
        
        # Try multiple URLs for Google Fonts - corrected paths
        urls = [
            # Try GitHub Google Fonts repository (correct path)
            f"https://raw.githubusercontent.com/google/fonts/main/ofl/montserrat/static/{font_name}.ttf",
            f"https://github.com/google/fonts/raw/main/ofl/montserrat/static/{font_name}.ttf",
            # Try alternative repository
            f"https://raw.githubusercontent.com/JulietaUla/Montserrat/master/fonts/ttf/{font_name}.ttf",
            # Try Google Fonts API (if available)
            f"https://fonts.gstatic.com/s/montserrat/v26/JTUHjIg1_i6t8kCHKm4532VJOt5-QNFgpCtr6Hw5aXpsog.woff2",
        ]
        
        print(f"Downloading {font_name} from Google Fonts...")
        
        for url in urls:
            try:
                response = requests.get(url, timeout=30, allow_redirects=True, headers={'User-Agent': 'Mozilla/5.0'})
                if response.status_code == 200 and len(response.content) > 1000:  # Check if it's a valid font file
                    with open(font_path, 'wb') as f:
                        f.write(response.content)
                    print(f"Successfully downloaded {font_name} from {url}")
                    return str(font_path)
            except Exception as e:
                continue
        
        # If all URLs fail, use Black as fallback (it's already available)
        black_path = fonts_dir / "Montserrat-Black.ttf"
        if black_path.exists():
            print(f"Using Montserrat-Black as fallback for {font_name} (download failed, but Black is available)")
            return str(black_path)
        
        print(f"Failed to download {font_name} from all sources")
        return None
    except Exception as e:
        print(f"Error downloading {font_name} from Google Fonts: {e}")
        # Try Black as fallback
        black_path = fonts_dir / "Montserrat-Black.ttf"
        if black_path.exists():
            return str(black_path)
        return None

def get_fonts_mapping() -> Dict[str, str]:
    """Get the mapping of font filenames to Google Drive file IDs."""
    # Define the mapping with relative paths
    # Montserrat fonts per gli effetti video
    return {
        "Montserrat-Black.ttf": "1e6xJA-Ka09GtPmpW9MWVjENMV0u6BEOo",
        # Bold e Medium verranno scaricati da Google Fonts se non disponibili
    }

def update_config_fonts(font_paths: List[str]) -> None:
    """Update the config module with the new font paths."""
    try:
        # Import the config module dynamically
        import importlib
        import config
        
        # Update the FONT_PATHS
        config.FONT_PATHS = font_paths
        
        # Update the default font path if it exists in the new paths
        if font_paths and os.path.exists(font_paths[0]):
            config.FONT_PATH_DEFAULT = font_paths[0]
        
        print("Updated config with new font paths.")
    except ImportError:
        print("Warning: Could not import config module to update font paths.")
    except Exception as e:
        print(f"Error updating config: {str(e)}")

def save_font_paths(font_paths: List[str]) -> None:
    """Save the font paths to a JSON file for persistence."""
    fonts_dir = get_fonts_dir()
    paths_file = fonts_dir / "font_paths.json"
    
    try:
        with open(paths_file, 'w') as f:
            json.dump(font_paths, f, indent=2)
        print(f"Saved font paths to {paths_file}")
    except Exception as e:
        print(f"Error saving font paths: {str(e)}")

def load_font_paths() -> List[str]:
    """Load the font paths from a JSON file if it exists."""
    fonts_dir = get_fonts_dir()
    paths_file = fonts_dir / "font_paths.json"
    
    if paths_file.exists():
        try:
            with open(paths_file, 'r') as f:
                return json.load(f)
        except Exception as e:
            print(f"Error loading font paths: {str(e)}")
    
    return []

def check_and_download_fonts() -> List[str]:
    """Check if fonts exist and download missing ones.
    Returns a list of all available font paths.
    Downloads Montserrat variants from Google Fonts if needed.
    """
    fonts_dir = get_fonts_dir()
    fonts_mapping = get_fonts_mapping()
    
    # Try to load existing paths first
    existing_paths = load_font_paths()
    if existing_paths and all(os.path.exists(path) for path in existing_paths):
        # Check if Montserrat-Bold and Medium are available
        bold_path = fonts_dir / "Montserrat-Bold.ttf"
        medium_path = fonts_dir / "Montserrat-Medium.ttf"
        
        if not bold_path.exists():
            download_montserrat_from_google_fonts(fonts_dir, "Bold")
        if not medium_path.exists():
            download_montserrat_from_google_fonts(fonts_dir, "Medium")
        
        # Re-check all paths
        all_paths = list(existing_paths)
        if bold_path.exists() and str(bold_path) not in all_paths:
            all_paths.append(str(bold_path))
        if medium_path.exists() and str(medium_path) not in all_paths:
            all_paths.append(str(medium_path))
        
        if all(os.path.exists(path) for path in all_paths):
            print(f"All {len(all_paths)} fonts already available.")
            return all_paths
    
    # Download all fonts from Google Drive mapping
    downloaded_count = 0
    font_paths = []
    
    for filename, file_id in fonts_mapping.items():
        # Create the full path for the font
        file_path = str(fonts_dir / filename)
        font_paths.append(file_path)
        
        # Check if the file already exists
        if os.path.exists(file_path):
            print(f"Font already exists: {filename}")
            continue
        
        print(f"Downloading font: {filename}")
        
        try:
            download_file_from_google_drive(file_id, file_path)
            print(f"Successfully downloaded: {filename}")
            downloaded_count += 1
        except Exception as e:
            print(f"Failed to download {filename}: {str(e)}")
    
    # Download Montserrat-Bold and Medium from Google Fonts if not present
    bold_path = fonts_dir / "Montserrat-Bold.ttf"
    medium_path = fonts_dir / "Montserrat-Medium.ttf"
    
    if not bold_path.exists():
        bold_downloaded = download_montserrat_from_google_fonts(fonts_dir, "Bold")
        if bold_downloaded:
            font_paths.append(bold_downloaded)
            downloaded_count += 1
    
    if not medium_path.exists():
        medium_downloaded = download_montserrat_from_google_fonts(fonts_dir, "Medium")
        if medium_downloaded:
            font_paths.append(medium_downloaded)
            downloaded_count += 1
    
    if downloaded_count > 0:
        print(f"Downloaded {downloaded_count} fonts.")
    
    # Save the paths for future use
    save_font_paths(font_paths)
    
    # Update the config module with the new paths
    update_config_fonts(font_paths)
    
    return font_paths

def find_system_fonts() -> List[str]:
    """Search for fonts in common system locations."""
    system_paths = []
    
    # Define common locations to search for fonts
    common_locations = [
        # Windows locations
        os.path.expanduser("~\\Documents"),
        os.path.expanduser("~\\Downloads"),
        os.path.expanduser("~\\OneDrive\\Documenti"),
        os.path.expanduser("~\\Desktop"),
        # Add more common locations as needed
    ]
    
    # Get filenames we're looking for
    fonts_mapping = get_fonts_mapping()
    filenames = list(fonts_mapping.keys())
    
    print("Searching for fonts in system locations...")
    
    for location in common_locations:
        if not os.path.exists(location):
            continue
            
        for root, _, files in os.walk(location):
            for filename in files:
                if filename in filenames:
                    full_path = os.path.join(root, filename)
                    system_paths.append(full_path)
                    print(f"Found system font: {full_path}")
    
    return system_paths

def get_all_available_fonts() -> List[str]:
    """Get all available fonts, combining downloaded and system files."""
    # First check and download missing fonts
    downloaded_paths = check_and_download_fonts()
    
    # Then search for fonts in system locations
    system_paths = find_system_fonts()
    
    # Combine and deduplicate paths (keeping the first occurrence)
    all_paths = []
    seen_filenames = set()
    
    for path in downloaded_paths + system_paths:
        filename = os.path.basename(path)
        if filename not in seen_filenames and os.path.exists(path):
            all_paths.append(path)
            seen_filenames.add(filename)
    
    print(f"Total available fonts: {len(all_paths)}")
    
    # Update config with all available paths
    update_config_fonts(all_paths)
    
    return all_paths

if __name__ == "__main__":
    get_all_available_fonts()