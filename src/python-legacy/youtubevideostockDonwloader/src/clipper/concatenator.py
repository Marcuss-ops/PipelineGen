from pathlib import Path
import subprocess
import random

class VideoConcatenator:
    def __init__(self, output_dir: Path):
        self.output_dir = Path(output_dir)
        self.output_dir.mkdir(parents=True, exist_ok=True)
    
    def create_concat_list(self, clips: list[Path], list_path: Path):
        with open(list_path, "w") as f:
            for clip in clips:
                f.write(f"file '{clip.absolute()}'\n")
    
    def concatenate(self, clips: list[Path], output_name: str, segment_duration: int = 25) -> list[Path]:
        segments = []
        clips_per_segment = segment_duration // 5
        
        for i in range(0, len(clips), clips_per_segment):
            segment_clips = clips[i:i + clips_per_segment]
            if not segment_clips:
                continue
            
            output_path = self.output_dir / f"{output_name}_part{i // clips_per_segment + 1}.mp4"
            list_path = self.output_dir / f"concat_list_{i}.txt"
            
            self.create_concat_list(segment_clips, list_path)
            
            cmd = [
                "ffmpeg", "-y",
                "-f", "concat",
                "-safe", "0",
                "-i", str(list_path),
                "-c:v", "libx264", "-preset", "fast", "-movflags", "+faststart",
                "-c:a", "aac", "-b:a", "128k",
                str(output_path)
            ]
            
            subprocess.run(cmd, capture_output=True)
            if output_path.exists():
                segments.append(output_path)
                print(f"Created segment: {output_path.name}")
            
            list_path.unlink(missing_ok=True)
        
        return segments
    
    def shuffle_and_concatenate(self, clips: list[Path], output_name: str, segment_duration: int = 25) -> list[Path]:
        shuffled = clips.copy()
        random.shuffle(shuffled)
        return self.concatenate(shuffled, output_name, segment_duration)
