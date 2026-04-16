import os
import sys
import logging
from pathlib import Path
from typing import Optional, Dict, Any, List

# Imports from utils
try:
    from routes.youtube.utils import (
        _download_to_temp_file, 
        _is_quota_exceeded_error, 
        _youtube_quota_reset_info,
        _youtube_modules_dir
    )
except ImportError:
    try:
        from ...utils import (
            _download_to_temp_file, 
            _is_quota_exceeded_error, 
            _youtube_quota_reset_info,
            _youtube_modules_dir
        )
    except ImportError:
        pass

logger = logging.getLogger(__name__)

class YouTubeApiAdapter:
    """
    Adapter for direct YouTube API interactions (using backend scripts/modules).
    """

    @staticmethod
    def apply_thumbnail(channel_id: str, youtube_video_id: str, thumbnail_url: str) -> Dict[str, Any]:
        """
        Downloads thumbnail and applies it to a video via YouTube API.
        """
        try:
            # Locate YouTubePosting module
            youtube_posting_path = Path("/home/pierone/Pyt/YoutubePosting")
            if not youtube_posting_path.exists():
                # Fallback relative path
                base_dir = Path(__file__).parent.parent.parent.parent.parent.parent # Adjust based on depth
                youtube_posting_path = base_dir / "YoutubePosting"
            
            if str(youtube_posting_path) not in sys.path:
                sys.path.insert(0, str(youtube_posting_path))
                
            try:
                from Modules.youtube_uploader import get_service_by_label, set_thumbnail  # type: ignore
            except ImportError:
                try:
                    from modules.youtube_uploader import get_service_by_label, set_thumbnail
                except:
                    return {"ok": False, "error": "YoutubeUploader module not found"}

            tmp_path = None
            downloaded = False
            
            if os.path.isfile(thumbnail_url):
                tmp_path = thumbnail_url
                downloaded = False
            else:
                try:
                    tmp_path = _download_to_temp_file(thumbnail_url)
                    downloaded = True
                except Exception as e:
                    return {"ok": False, "error": f"Download failed: {e}"}

            # Optional: Compression logic could be moved to a helper or kept here
            # For brevity, proceeding to set thumbnail
            
            try:
                service = get_service_by_label(channel_id, use_quota_rotation=False)
                set_thumbnail(service, youtube_video_id, tmp_path)
                return {"ok": True}
            finally:
                if downloaded and tmp_path:
                    try:
                        os.remove(tmp_path)
                    except Exception:
                        pass
        except Exception as e:
            if _is_quota_exceeded_error(e):
                return {
                    "ok": False, 
                    "status": "quota_exceeded", 
                    "error": "quotaExceeded", 
                    "quota": _youtube_quota_reset_info()
                }
            return {"ok": False, "status": "error", "error": str(e)[:500]}
