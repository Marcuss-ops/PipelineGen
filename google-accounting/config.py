import os
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
DEFAULT_DRIVE_FOLDER_ID = os.getenv("DEFAULT_DRIVE_FOLDER_ID", "1HinlvxnAFknV3wCSB9cuKA4gZVivdXC7")
DEFAULT_IMAGES_DRIVE_FOLDER_ID = os.getenv(
    "DEFAULT_IMAGES_DRIVE_FOLDER_ID",
    os.getenv("VELOX_DRIVE_IMAGES_ROOT", "14gbQ3ucY9TaRs0aGPkJxTpczGv7tQW4C"),
)

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
