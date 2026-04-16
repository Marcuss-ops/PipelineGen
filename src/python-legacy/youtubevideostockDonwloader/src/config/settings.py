from dataclasses import dataclass
from pathlib import Path

@dataclass
class ProjectConfig:
    drive_folder_id: str = ""
    output_dir: Path = Path("output")
    downloads_dir: Path = Path("downloads")
    clips_dir: Path = Path("clips")
    segments_dir: Path = Path("segments")
    
    clip_duration: int = 5
    segment_duration: int = 25
    final_duration: int = 1200
    
    video_quality: str = "1080p"
    trans_every: int = 4
    effect_every: int = 5

@dataclass
class VideoInput:
    url: str
    clip_duration: int = 5
