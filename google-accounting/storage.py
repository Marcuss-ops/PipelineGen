from pathlib import Path
from config import DOWNLOAD_DIR


def ensure_dirs():
    (DOWNLOAD_DIR / "videos").mkdir(parents=True, exist_ok=True)
    (DOWNLOAD_DIR / "images").mkdir(parents=True, exist_ok=True)
    Path("logs").mkdir(exist_ok=True)


def list_downloaded_videos() -> list[str]:
    d = DOWNLOAD_DIR / "videos"
    return [f.name for f in d.glob("*.mp4")] if d.exists() else []


def list_downloaded_images() -> list[str]:
    d = DOWNLOAD_DIR / "images"
    return [f.name for f in d.iterdir() if f.is_file()] if d.exists() else []
