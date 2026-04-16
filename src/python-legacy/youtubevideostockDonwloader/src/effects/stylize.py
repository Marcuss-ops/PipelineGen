from pathlib import Path
import subprocess

class TransitionEffects:
    TRANSITIONS = {
        "fade": "fade=t=in:st=0:d=0.5,fade=t=out:st=2:d=0.5",
        "wipe_left": "fade=t=in:st=0:d=0.3",
        "wipe_right": "fade=t=in:st=0:d=0.3",
        "dissolve": "fade=t=in:st=0:d=0.5",
        "zoom_in": "zoompan=z='min(zoom+0.001,1.5)':d=25",
    }
    
    @classmethod
    def apply(cls, clip_path: Path, output_path: Path, transition: str = "fade") -> Path:
        if transition not in cls.TRANSITIONS:
            transition = "fade"
        
        cmd = [
            "ffmpeg", "-y",
            "-i", str(clip_path),
            "-vf", cls.TRANSITIONS[transition],
            "-c:v", "libx264", "-preset", "fast",
            "-c:a", "aac",
            str(output_path)
        ]
        subprocess.run(cmd, capture_output=True)
        return output_path

class VideoEffects:
    EFFECTS = {
        "grayscale": "hue=s=0",
        "brightness": "eq=brightness=0.15",
        "contrast": "eq=contrast=1.3",
        "blur": "boxblur=2:1",
        "sharpen": "unsharp=5:5:1.0",
        "vintage": "hue=s=0:x=0.1",
        "warm": "colortemperature=temperature=6500",
        "cool": "colortemperature=temperature=9000",
    }
    
    @classmethod
    def apply(cls, clip_path: Path, output_path: Path, effect: str = "contrast") -> Path:
        if effect not in cls.EFFECTS:
            effect = "contrast"
        
        cmd = [
            "ffmpeg", "-y",
            "-i", str(clip_path),
            "-vf", cls.EFFECTS[effect],
            "-c:v", "libx264", "-preset", "fast",
            "-c:a", "aac",
            str(output_path)
        ]
        subprocess.run(cmd, capture_output=True)
        return output_path
    
    @classmethod
    def apply_random(cls, clip_path: Path, output_path: Path) -> Path:
        import random
        effect = random.choice(list(cls.EFFECTS.keys()))
        return cls.apply(clip_path, output_path, effect)
