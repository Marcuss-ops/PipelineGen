import os
from pathlib import Path
from dotenv import load_dotenv

load_dotenv()

GOOGLE_CREDENTIALS_PATH = os.getenv("GOOGLE_CREDENTIALS_PATH", "credentials.json")
SESSIONS_DIR = Path(os.getenv("SESSIONS_DIR", "sessions"))
DEFAULT_ACCOUNT = os.getenv("DEFAULT_ACCOUNT", "default")
DOWNLOAD_DIR = Path(os.getenv("DOWNLOAD_DIR", "./downloads"))
SCHEDULE_CRON = os.getenv("SCHEDULE_CRON", "0 2 * * *")
HOST = os.getenv("HOST", "0.0.0.0")
PORT = int(os.getenv("PORT", "8000"))

DRIVE_SCOPES = [
    "https://www.googleapis.com/auth/drive.readonly",
]

VIDS_MIME_TYPE = "application/vnd.google-apps.vids"
GOOGLE_VIDS_BASE_URL = "https://docs.google.com/videos/d"
GOOGLE_VIDS_HOME_URL = "https://vids.google.com"

def get_session_path(account: str = None) -> Path:
    """Returns the path to the session file for a specific account."""
    if not account:
        account = DEFAULT_ACCOUNT
    SESSIONS_DIR.mkdir(parents=True, exist_ok=True)
    return SESSIONS_DIR / f"{account}.json"
