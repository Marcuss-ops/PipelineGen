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
        result = subprocess.run(cmd, capture_output=True, text=True, timeout=35)
        if result.returncode != 0:
            print(f"yt-dlp failed with return code {result.returncode}")
            print(f"stderr: {result.stderr}")
            shutil.rmtree(tmp_dir, ignore_errors=True)
            return None
    except Exception as e:
        print(f"yt-dlp exception: {e}")
        shutil.rmtree(tmp_dir, ignore_errors=True)
        return None

    vtts = glob.glob(f"{tmp_dir}/*.vtt")
    if not vtts:
        print(f"yt-dlp ran but no VTT files found. stderr: {result.stderr if 'result' in locals() else 'N/A'}")
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
