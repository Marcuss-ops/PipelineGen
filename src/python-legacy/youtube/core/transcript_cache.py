import hashlib
import json
import os
import time
from typing import Optional

VTT_CACHE_DIR = "/tmp/vtt_cache"
VTT_CACHE_TTL = 3600 * 24


def get_vtt_cache_path(youtube_url: str, lang: str) -> str:
    os.makedirs(VTT_CACHE_DIR, exist_ok=True)
    url_hash = hashlib.md5(f"{youtube_url}_{lang}".encode()).hexdigest()
    return os.path.join(VTT_CACHE_DIR, f"{url_hash}.json")


def get_cached_vtt(youtube_url: str, lang: str) -> Optional[str]:
    cache_path = get_vtt_cache_path(youtube_url, lang)
    if not os.path.exists(cache_path):
        return None
    try:
        with open(cache_path, "r", encoding="utf-8") as handle:
            cache_data = json.load(handle)
        if time.time() - cache_data.get("cached_at", 0) > VTT_CACHE_TTL:
            os.remove(cache_path)
            return None
        return cache_data.get("vtt_content")
    except Exception:
        return None


def save_vtt_to_cache(youtube_url: str, lang: str, vtt_content: str) -> None:
    cache_path = get_vtt_cache_path(youtube_url, lang)
    cache_data = {
        "vtt_content": vtt_content,
        "cached_at": time.time(),
        "youtube_url": youtube_url,
        "lang": lang,
    }
    try:
        with open(cache_path, "w", encoding="utf-8") as handle:
            json.dump(cache_data, handle)
    except Exception:
        pass
