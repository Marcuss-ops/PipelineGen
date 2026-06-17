import os
from pathlib import Path

_PROJECT_ROOT = Path(__file__).resolve().parent.parent.parent
BOOKS_DRIVE_FOLDER_ID = os.getenv("BOOKS_DRIVE_FOLDER_ID", "1kcAZmBIDVxNdUKO9BFcMHdpX_KFf66xb")

LANGUAGES = {
    "en": "English",
    "es": "Spanish",
    "fr": "French",
    "de": "German",
    "it": "Italian",
    "pt": "Portuguese",
    "pl": "Polish",
    "nl": "Dutch",
    "ja": "Japanese",
    "ko": "Korean",
    "ru": "Russian",
    "tr": "Turkish",
    "id": "Indonesian",
    "zh": "Chinese",
    "ar": "Arabic",
    "hi": "Hindi",
}
