from pathlib import Path
from yt_dlp import YoutubeDL

class YouTubeDownloader:
    def __init__(self, output_dir: Path, quality: str = "1080p"):
        self.output_dir = Path(output_dir)
        self.output_dir.mkdir(parents=True, exist_ok=True)
        self.quality = quality
    
    def _get_opts(self) -> dict:
        height = int(self.quality.rstrip("p"))
        return {
            "format": f"bestvideo[height<={height}]+bestaudio/best[height<={height}]",
            "outtmpl": str(self.output_dir / "%(title)s_%(id)s.%(ext)s"),
            "merge_output_format": "mp4",
            "quiet": False,
        }
    
    def download(self, url: str) -> list[Path]:
        downloaded = []
        with YoutubeDL(self._get_opts()) as ydl:
            if "/playlist" in url or ("/watch" in url and "list=" in url):
                info = ydl.extract_info(url, download=False)
                entries = info.get("entries", [])
                for entry in entries:
                    try:
                        video_url = f"https://www.youtube.com/watch?v={entry.get('id')}"
                        info = ydl.extract_info(video_url, download=True)
                        filename = ydl.prepare_filename(info)
                        downloaded.append(Path(filename))
                        print(f"Downloaded: {info.get('title')}")
                    except Exception as e:
                        print(f"Error: {e}")
            else:
                info = ydl.extract_info(url, download=True)
                filename = ydl.prepare_filename(info)
                downloaded.append(Path(filename))
                print(f"Downloaded: {info.get('title')}")
        return downloaded
    
    def download_multiple(self, urls: list[str]) -> list[Path]:
        all_videos = []
        for url in urls:
            videos = self.download(url)
            all_videos.extend(videos)
        return all_videos
