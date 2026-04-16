from pathlib import Path
from typing import Optional
import subprocess
import random

class VideoClipper:
    def __init__(self, output_dir: Path, clip_duration: int = 5):
        self.output_dir = Path(output_dir)
        self.output_dir.mkdir(parents=True, exist_ok=True)
        self.clip_duration = clip_duration
    
    def split_video(self, video_path: Path) -> list[Path]:
        clips = []
        clip_index = 0
        
        while True:
            start_time = clip_index * self.clip_duration
            output_path = self.output_dir / f"clip_{video_path.stem}_{clip_index:03d}.mp4"
            
            cmd = [
                "ffmpeg", "-y",
                "-i", str(video_path),
                "-ss", str(start_time),
                "-t", str(self.clip_duration),
                "-c:v", "libx264", "-preset", "fast",
                "-c:a", "aac", "-b:a", "128k",
                str(output_path)
            ]
            
            result = subprocess.run(cmd, capture_output=True)
            if result.returncode != 0:
                break
            
            if output_path.exists() and output_path.stat().st_size > 0:
                clips.append(output_path)
                clip_index += 1
            else:
                break
        
        return clips
    
    def split_multiple(self, video_paths: list[Path]) -> list[Path]:
        all_clips = []
        for video in video_paths:
            print(f"Splitting: {video.name}")
            clips = self.split_video(video)
            all_clips.extend(clips)
            print(f"  Created {len(clips)} clips")
        return all_clips
    
    def apply_random_transition(self, clip_path: Path, output_path: Optional[Path] = None) -> Path:
        if output_path is None:
            output_path = clip_path.with_stem(clip_path.stem + "_trans")
        
        transitions = ["fade", "wipe_left", "wipe_right", "dissolve"]
        transition = random.choice(transitions)
        
        cmd = [
            "ffmpeg", "-y",
            "-i", str(clip_path),
            "-vf", f"fade=t={transition}:d=0.5",
            "-c:v", "libx264", "-preset", "fast",
            "-c:a", "aac",
            str(output_path)
        ]
        subprocess.run(cmd, capture_output=True)
        return output_path
    
    def apply_random_effect(self, clip_path: Path, output_path: Optional[Path] = None) -> Path:
        if output_path is None:
            output_path = clip_path.with_stem(clip_path.stem + "_effect")
        
        effects = ["hue=s=0", "eq=brightness=0.1", "eq=contrast=1.2", "boxblur=1:0"]
        effect = random.choice(effects)
        
        cmd = [
            "ffmpeg", "-y",
            "-i", str(clip_path),
            "-vf", effect,
            "-c:v", "libx264", "-preset", "fast",
            "-c:a", "aac",
            str(output_path)
        ]
        subprocess.run(cmd, capture_output=True)
        return output_path
