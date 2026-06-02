"""Centralized configuration for the google-accounting Python microservice.

All Drive folder IDs are sourced from the project's config.yaml (single source of truth),
with environment variable overrides for development/deployment flexibility.
"""

import os
import yaml
from pathlib import Path
from dotenv import load_dotenv

load_dotenv()

BASE_DIR = Path(__file__).resolve().parent.parent

GOOGLE_CREDENTIALS_PATH = Path(os.getenv("GOOGLE_CREDENTIALS_PATH", str(BASE_DIR / "credentials.json")))
TOKEN_PATH = Path(os.getenv("GOOGLE_TOKEN_PATH", str(BASE_DIR / "token.json")))
SESSIONS_DIR = Path(os.getenv("SESSIONS_DIR", str(BASE_DIR / "google-accounting" / "sessions")))
DEFAULT_ACCOUNT = os.getenv("DEFAULT_ACCOUNT", "favamassimo")
DOWNLOAD_DIR = Path(os.getenv("DOWNLOAD_DIR", str(BASE_DIR / "data" / "google_vids"))).resolve()
MAX_WARM_CONTEXTS = int(os.getenv("MAX_WARM_CONTEXTS", "9"))
SCHEDULE_CRON = os.getenv("SCHEDULE_CRON", "0 2 * * *")
HOST = os.getenv("HOST", "0.0.0.0")
PORT = int(os.getenv("PORT", "8000"))

# ---------------------------------------------------------------------------
# Drive folder IDs — single source of truth is config.yaml
# Env vars can override for ephemeral/testing use.
# ---------------------------------------------------------------------------

def _load_full_config_yaml() -> dict:
    """Load the project's full config.yaml once and cache it."""
    config_path = BASE_DIR / "config.yaml"
    if not config_path.exists():
        return {}
    try:
        with open(config_path, "r") as f:
            return yaml.safe_load(f) or {}
    except Exception:
        return {}

_full_cfg = _load_full_config_yaml()
_drive_cfg = _full_cfg.get("drive") or {}
_ga_cfg = _full_cfg.get("google_accounting") or {}


def _get_drive_folder_id(yaml_key: str, env_var: str, default: str = "") -> str:
    """Resolve a Drive folder ID: env var > config.yaml > default."""
    return os.getenv(env_var) or _drive_cfg.get(yaml_key) or default


# Script/docs generation root (legacy DEFAULT_DRIVE_FOLDER_ID)
DEFAULT_DRIVE_FOLDER_ID = _get_drive_folder_id(
    "scripts_root_folder",
    "DEFAULT_DRIVE_FOLDER_ID",
    "",
)

# Images root folder
DEFAULT_IMAGES_DRIVE_FOLDER_ID = _get_drive_folder_id(
    "images_root_folder",
    "DEFAULT_IMAGES_DRIVE_FOLDER_ID",
    "",
)

# Books (summarized/rewritten) root folder
BOOKS_DRIVE_FOLDER_ID = _get_drive_folder_id(
    "books_root_folder",
    "BOOKS_DRIVE_FOLDER_ID",
    "",
)

# Stock footage root folder
STOCK_DRIVE_FOLDER_ID = _get_drive_folder_id(
    "stock_root_folder",
    "STOCK_DRIVE_FOLDER_ID",
    "",
)

# ---------------------------------------------------------------------------
# Google Vids project ID — from config.yaml (google_accounting.vids_project_id)
# ---------------------------------------------------------------------------
VIDS_PROJECT_ID = os.getenv("VIDS_PROJECT_ID") or (_ga_cfg.get("vids_project_id") or "").strip() or "new"

# ---------------------------------------------------------------------------
# Google API scopes
# ---------------------------------------------------------------------------
DRIVE_SCOPES = [
    "https://www.googleapis.com/auth/drive",
]

VIDS_MIME_TYPE = "application/vnd.google-apps.vids"
GOOGLE_VIDS_BASE_URL = "https://docs.google.com/videos/d"
GOOGLE_VIDS_HOME_URL = "https://vids.google.com"

PROFILES_DIR = Path(os.getenv("PROFILES_DIR", str(BASE_DIR / "google-accounting" / "profiles")))


def get_session_path(account: str = None) -> Path:
    """Returns the path to the session file for a specific account."""
    if not account:
        account = DEFAULT_ACCOUNT
    SESSIONS_DIR.mkdir(parents=True, exist_ok=True)
    return SESSIONS_DIR / f"{account}.json"


def get_profile_path(account: str = None) -> Path:
    """Returns the path to the persistent Chrome profile directory for a specific account."""
    return Path("/home/pierone/snap/chromium/common/chromium")
