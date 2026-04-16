import os
import shutil
import subprocess
from typing import Dict

YT_DLP_COOKIE_ARGS = ["--cookies-from-browser", "chrome"]


def get_yt_dlp_base_cmd() -> list:
    return [shutil.which("yt-dlp") or "yt-dlp"] + YT_DLP_COOKIE_ARGS + [
        "--force-ipv4",
        "--no-check-certificates",
    ]


def get_video_info(video_url: str) -> Dict:
    yt_dlp_path = shutil.which("yt-dlp") or "yt-dlp"
    cmd = [
        yt_dlp_path,
        "--cookies-from-browser", "chrome",
        "--quiet", "--no-warnings",
        "--print", "%(duration)s|%(title)s|%(id)s",
        video_url,
    ]
    try:
        result = subprocess.run(cmd, capture_output=True, text=True, timeout=30, errors="replace")
        if result.returncode == 0 and result.stdout.strip():
            parts = result.stdout.strip().split('|')
            return {
                "duration": int(parts[0]) if parts[0].strip().isdigit() else 0,
                "title": parts[1] if len(parts) > 1 else "",
                "video_id": parts[2] if len(parts) > 2 else "",
            }
    except Exception:
        pass
    return {"duration": 0, "title": "", "video_id": ""}


def smart_download_video(video_url: str, needed_duration: int, output_dir: str, quality: str = "1080p", loop_if_short: bool = True) -> Dict:
    yt_dlp_path = shutil.which("yt-dlp") or "yt-dlp"
    ffmpeg_path = shutil.which("ffmpeg") or "ffmpeg"
    ffprobe_path = shutil.which("ffprobe") or "ffprobe"
    info = get_video_info(video_url)
    video_duration = info["duration"]
    video_id = info["video_id"]
    if video_duration == 0:
        video_duration = needed_duration * 2
    download_duration = min(needed_duration, video_duration)
    temp_file = os.path.join(output_dir, f"{video_id}_partial.mp4")
    height_limit = "720" if quality == "720p" else "1080"
    cmd_download = [
        yt_dlp_path,
        "--cookies-from-browser", "chrome",
        "-f", f"bestvideo[height<={height_limit}]+bestaudio/best[height<={height_limit}]",
        "--download-sections", f"*0:{download_duration}",
        "--merge-output-format", "mp4",
        "-o", temp_file,
        "--force-ipv4", "--no-check-certificates",
        video_url,
    ]
    result = subprocess.run(cmd_download, capture_output=True, text=True, timeout=300, errors="replace")
    if result.returncode != 0:
        return {"success": False, "error": result.stderr[:200]}
    downloaded_files = [f for f in os.listdir(output_dir) if f.startswith(video_id) and f.endswith('.mp4')]
    if not downloaded_files:
        return {"success": False, "error": "File non trovato dopo download"}
    downloaded_file = os.path.join(output_dir, downloaded_files[0])
    cmd_probe = [
        ffprobe_path, "-v", "error", "-show_entries", "format=duration",
        "-of", "default=noprint_wrappers=1:nokey=1", downloaded_file,
    ]
    probe = subprocess.run(cmd_probe, capture_output=True, text=True, timeout=10)
    actual_duration = float(probe.stdout.strip()) if probe.stdout.strip() else 0
    was_looped = False
    final_file = downloaded_file
    if loop_if_short and actual_duration < needed_duration:
        loops_needed = int(needed_duration / actual_duration) + 1
        looped_file = os.path.join(output_dir, f"{video_id}_looped.mp4")
        cmd_loop = [
            ffmpeg_path, "-y", "-stream_loop", str(loops_needed), "-i", downloaded_file,
            "-t", str(needed_duration), "-c:v", "libx264", "-preset", "fast", "-c:a", "aac", looped_file,
        ]
        subprocess.run(cmd_loop, capture_output=True, timeout=120)
        if os.path.exists(looped_file):
            os.remove(downloaded_file)
            final_file = looped_file
            actual_duration = needed_duration
            was_looped = True
    return {
        "success": True,
        "file_path": final_file,
        "actual_duration": actual_duration,
        "original_duration": video_duration,
        "was_looped": was_looped,
        "video_id": video_id,
        "downloaded_seconds": download_duration,
    }
