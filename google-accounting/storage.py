import json
import sqlite3
import uuid
import logging
from pathlib import Path
from datetime import datetime
from config import BASE_DIR, DOWNLOAD_DIR
from style_presets import STYLE_FOLDER_NAMES

log = logging.getLogger(__name__)

# Database path (unificato in velox.db.sqlite)
DB_PATH = BASE_DIR / "data" / "velox" / "velox.db.sqlite"
PROJECTS_FILE = DOWNLOAD_DIR.parent / "projects_cache.json"

def _safe_mkdir(path: Path) -> None:
    try:
        path.mkdir(parents=True, exist_ok=True)
    except OSError as exc:
        log.warning("Unable to create directory %s: %s", path, exc)

def ensure_dirs():
    _safe_mkdir(DOWNLOAD_DIR / "videos")
    _safe_mkdir(DOWNLOAD_DIR / "images")
    _safe_mkdir(DOWNLOAD_DIR / "avatars")
    _safe_mkdir(Path("logs"))
    _safe_mkdir(PROJECTS_FILE.parent)
    for style in STYLE_FOLDER_NAMES:
        _safe_mkdir(DOWNLOAD_DIR / style / "general" / "general")

def save_media_asset(
    file_path: Path, 
    source: str, 
    name: str, 
    media_type: str = "image",
    style: str = "", 
    sub_style: str = "", 
    prompt: str = "", 
    project_id: str = "",
    metadata: dict = None,
    drive_file_id: str = "",
    drive_link: str = "",
    drive_folder_id: str = ""
):
    """Saves media asset metadata to the SQLite database."""
    if not DB_PATH.exists():
        print(f"Warning: Database {DB_PATH} not found.")
        return False
    
    asset_id = str(uuid.uuid4())
    metadata_json = json.dumps(metadata or {})
    try:
        relative_path = str(file_path.relative_to(BASE_DIR))
    except ValueError:
        relative_path = str(file_path)
    
    # Normalizza i tag in tags_norm per ricerca full-text
    tags_list = []
    if metadata and "tags" in metadata:
        ts = metadata["tags"]
        if isinstance(ts, list):
            tags_list = ts
        elif isinstance(ts, str):
            tags_list = [t.strip() for t in ts.split(",") if t.strip()]
    tags_json = json.dumps(tags_list)
    tags_norm = " ".join(
        t.lower().replace("à", "a").replace("è", "e").replace("é", "e")
        .replace("ì", "i").replace("ò", "o").replace("ù", "u")
        for t in tags_list
    )

    now = datetime.now().isoformat()
    try:
        conn = sqlite3.connect(str(DB_PATH))
        cursor = conn.cursor()
        cursor.execute("""
            INSERT INTO media_assets (
                id, source, name, tags, tags_norm, local_path, relative_path,
                media_type, metadata_json, created_at, status, updated_at,
                drive_file_id, drive_link, drive_folder_id
            ) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
            ON CONFLICT(id) DO UPDATE SET
                name=excluded.name,
                tags=excluded.tags,
                tags_norm=excluded.tags_norm,
                local_path=excluded.local_path,
                relative_path=excluded.relative_path,
                media_type=excluded.media_type,
                metadata_json=excluded.metadata_json,
                status=excluded.status,
                updated_at=excluded.updated_at,
                drive_file_id=excluded.drive_file_id,
                drive_link=excluded.drive_link,
                drive_folder_id=excluded.drive_folder_id
        """, (
            asset_id, source, name, tags_json, tags_norm,
            str(file_path), relative_path,
            media_type, metadata_json, now, "ready", now,
            drive_file_id, drive_link, drive_folder_id
        ))
        conn.commit()
        conn.close()
        return True
    except Exception as e:
        print(f"Error saving to DB: {e}")
        return False

def get_structured_path(media_type: str, style: str = "", sub_style: str = "", generation_id: str = None) -> Path:
    """Returns a structured path for media types or style-rooted generations."""
    gen_id = generation_id or f"gen_{datetime.now().strftime('%Y%m%d_%H%M%S')}_{uuid.uuid4().hex[:6]}"
    
    # Sanitize names
    if media_type in STYLE_FOLDER_NAMES:
        m = media_type
    else:
        m = f"{media_type}s" if not media_type.endswith("s") else media_type
        if m == "images":
            m = "images"  # consistent
    
    s = style.replace(" ", "_") or "default"
    ss = sub_style.replace(" ", "_") or "general"
    
    path = DOWNLOAD_DIR / m / s / ss / gen_id
    path.mkdir(parents=True, exist_ok=True)
    return path

def save_generation_metadata(folder: Path, metadata: dict):
    """Saves metadata.json in the generation folder."""
    meta_file = folder / "metadata.json"
    try:
        meta_file.write_text(json.dumps(metadata, indent=2))
        return True
    except Exception as e:
        print(f"Error saving metadata.json: {e}")
        return False

def save_project_id(kind: str, project_id: str):
    """Saves a project ID to the local cache."""
    ensure_dirs()
    data = {}
    if PROJECTS_FILE.exists():
        try:
            data = json.loads(PROJECTS_FILE.read_text())
        except Exception as e:
            print(f"Error reading projects file: {e}")
    data[kind] = project_id
    try:
        PROJECTS_FILE.write_text(json.dumps(data, indent=2))
        print(f"Saved {kind} project ID {project_id} to {PROJECTS_FILE}")
    except Exception as e:
        print(f"Error writing projects file: {e}")

def save_character(
    char_id: str,
    name: str,
    image_drive_id: str = "",
    image_drive_link: str = "",
    voice_id: str = "",
    metadata: dict = None
):
    """Saves or updates a character in the registry."""
    if not DB_PATH.exists():
        print(f"Warning: Database {DB_PATH} not found.")
        return False
    
    metadata_json = json.dumps(metadata or {})
    now = datetime.now().isoformat()
    try:
        conn = sqlite3.connect(str(DB_PATH))
        cursor = conn.cursor()
        cursor.execute("""
            INSERT INTO characters (
                id, name, image_drive_id, image_drive_link, voice_id, metadata_json, created_at, updated_at
            ) VALUES (?, ?, ?, ?, ?, ?, ?, ?)
            ON CONFLICT(id) DO UPDATE SET
                name=excluded.name,
                image_drive_id=excluded.image_drive_id,
                image_drive_link=excluded.image_drive_link,
                voice_id=excluded.voice_id,
                metadata_json=excluded.metadata_json,
                updated_at=excluded.updated_at
        """, (
            char_id, name, image_drive_id, image_drive_link, voice_id, metadata_json, now, now
        ))
        conn.commit()
        conn.close()
        print(f"Character '{char_id}' ({name}) saved to DB.")
        return True
    except Exception as e:
        print(f"Error saving character to DB: {e}")
        return False

def get_character(char_id: str) -> dict:
    """Retrieves a character from the registry."""
    if not DB_PATH.exists():
        return None
    try:
        conn = sqlite3.connect(str(DB_PATH))
        conn.row_factory = sqlite3.Row
        cursor = conn.cursor()
        cursor.execute("SELECT * FROM characters WHERE id = ?", (char_id,))
        row = cursor.fetchone()
        conn.close()
        if row:
            char = dict(row)
            char["metadata"] = json.loads(char["metadata_json"])
            return char
        return None
    except Exception as e:
        print(f"Error retrieving character: {e}")
        return None

def get_project_id(kind: str) -> str:
    """Retrieves a project ID from the local cache."""
    if not PROJECTS_FILE.exists():
        return ""
    try:
        data = json.loads(PROJECTS_FILE.read_text())
        return data.get(kind, "")
    except:
        return ""

def list_downloaded_videos() -> list[str]:
    d = DOWNLOAD_DIR / "videos"
    return [f.name for f in d.glob("*.mp4")] if d.exists() else []

def list_downloaded_images() -> list[str]:
    d = DOWNLOAD_DIR / "images"
    return [f.name for f in d.iterdir() if f.is_file()] if d.exists() else []
