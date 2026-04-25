import os
import shutil
from typing import List

# Use the robust wrapper if it exists in the project root
PROJECT_ROOT = os.path.abspath(os.path.join(os.path.dirname(__file__), "..", ".."))
WRAPPER_PATH = os.path.join(PROJECT_ROOT, "scripts", "yt_dlp_wrapper.sh")

def get_yt_dlp_cmd(base_cmd: List[str], use_cookies: bool = True) -> List[str]:
    """Build yt-dlp command with shared defaults and anti-bot measures."""
    if os.path.exists(WRAPPER_PATH):
        cmd = [WRAPPER_PATH]
    else:
        cmd = [shutil.which("yt-dlp") or "yt-dlp"]

    # The wrapper already handles cookies and base args, 
    # but we keep them here as fallback for non-wrapper calls.
    if not os.path.exists(WRAPPER_PATH):
        if use_cookies:
            cmd.extend(["--cookies-from-browser", "chrome"])
        cmd.extend([
            "--force-ipv4",
            "--no-check-certificates",
        ])
        
    cmd.extend(base_cmd)
    return cmd
