import shutil
from typing import List

YT_DLP_COOKIE_ARGS = ["--cookies-from-browser", "chrome"]


def get_yt_dlp_cmd(base_cmd: List[str], use_cookies: bool = True) -> List[str]:
    """Build yt-dlp command with shared defaults."""
    cmd = [shutil.which("yt-dlp") or "yt-dlp"]
    if use_cookies:
        cmd.extend(YT_DLP_COOKIE_ARGS)
    cmd.extend([
        "--force-ipv4",
        "--no-check-certificates",
    ])
    cmd.extend(base_cmd)
    return cmd
