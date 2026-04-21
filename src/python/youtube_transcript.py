import glob
import re
import shutil
import subprocess
import tempfile
from typing import Optional

from .yt_dlp_utils import get_yt_dlp_cmd


def extract_vtt_from_youtube(youtube_url: str, lang_code: str = "en") -> Optional[str]:
    tmp_dir = tempfile.mkdtemp(prefix="yt_vtt_")
    cmd = get_yt_dlp_cmd([
        "--write-auto-sub",
        "--write-sub",
        "--sub-lang", lang_code,
        "--sub-format", "vtt",
        "--skip-download",
        "--no-warnings",
        "--quiet",
        "-o", f"{tmp_dir}/%(id)s.%(ext)s",
        youtube_url,
    ], use_cookies=False)
    try:
        subprocess.run(cmd, capture_output=True, text=True, timeout=35)
    except Exception:
        shutil.rmtree(tmp_dir, ignore_errors=True)
        return None

    vtts = glob.glob(f"{tmp_dir}/*.vtt")
    if not vtts:
        shutil.rmtree(tmp_dir, ignore_errors=True)
        return None

    try:
        with open(vtts[0], "r", encoding="utf-8", errors="ignore") as handle:
            content = handle.read()
    finally:
        shutil.rmtree(tmp_dir, ignore_errors=True)
    return content


def parse_vtt_to_text(vtt_content: str) -> str:
    out = []
    for line in (vtt_content or "").split("\n"):
        if "-->" in line:
            continue
        line = re.sub(r"<[^>]+>", "", line).strip()
        if line and not line.isdigit():
            out.append(line)
    return " ".join(out)
