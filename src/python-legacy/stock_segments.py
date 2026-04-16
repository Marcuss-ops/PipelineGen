"""
Video Stock Segments Module

Creates video segments from YouTube downloads with transitions.
NO EFFECTS - effects are external layers, only fade transitions here.
Default: 5-second clips, 25-second segments (5 clips per segment)
"""

import os
import sys
import json
import tempfile
import subprocess
import shutil
import time
import random
from pathlib import Path
from typing import List, Dict, Optional

# Default configuration
DEFAULT_CLIP_DURATION = 5  # seconds
DEFAULT_SEGMENT_DURATION = 25  # seconds (5 clips)
DEFAULT_TRANSITION_EVERY = 4  # Apply fade transition every N clips


class SegmentConfig:
    """Configuration for segment creation"""
    
    def __init__(
        self,
        clip_duration: float = DEFAULT_CLIP_DURATION,
        segment_duration: float = DEFAULT_SEGMENT_DURATION,
        transition_every: int = DEFAULT_TRANSITION_EVERY,
    ):
        self.clip_duration = clip_duration
        self.segment_duration = segment_duration
        self.transition_every = transition_every
    
    @property
    def clips_per_segment(self) -> int:
        return int(self.segment_duration / self.clip_duration)
    
    def to_dict(self) -> dict:
        return {
            "clip_duration": self.clip_duration,
            "segment_duration": self.segment_duration,
            "transition_every": self.transition_every,
            "clips_per_segment": self.clips_per_segment
        }


class VideoSegmentCreator:
    """Creates video segments from source videos with transitions only"""
    
    def __init__(self, config: Optional[SegmentConfig] = None):
        self.config = config or SegmentConfig()
        self.ffmpeg = shutil.which("ffmpeg") or "ffmpeg"
        self.ffprobe = shutil.which("ffprobe") or "ffprobe"
        self.yt_dlp = shutil.which("yt-dlp") or "yt-dlp"
    
    def get_video_duration(self, video_path: str) -> float:
        """Get video duration in seconds"""
        cmd = [
            self.ffprobe, "-v", "error",
            "-show_entries", "format=duration",
            "-of", "default=noprint_wrappers=1:nokey=1",
            video_path
        ]
        result = subprocess.run(cmd, capture_output=True, text=True, timeout=30)
        try:
            return float(result.stdout.strip())
        except:
            return 0.0
    
    def download_youtube(self, url: str, output_dir: str, use_cookies: bool = True) -> Optional[str]:
        """Download video from YouTube with cookies support"""
        os.makedirs(output_dir, exist_ok=True)
        
        cmd = [
            self.yt_dlp,
            "-f", "bestvideo[height<=1080]+bestaudio/best[height<=1080]",
            "--merge-output-format", "mp4",
            "-o", os.path.join(output_dir, "%(id)s.%(ext)s"),
            "--force-ipv4", "--no-check-certificates",
        ]
        
        if use_cookies:
            cmd.extend(["--cookies-from-browser", "chrome"])
        
        cmd.append(url)
        
        print(f"⬇️ Downloading: {url}")
        result = subprocess.run(cmd, capture_output=True, text=True, timeout=600, errors="replace")
        
        if result.returncode != 0:
            print(f"❌ Download error: {result.stderr[:200]}")
            return None
        
        # Find downloaded file
        for f in os.listdir(output_dir):
            if f.endswith('.mp4'):
                return os.path.join(output_dir, f)
        
        return None
    
    def split_into_clips(
        self,
        video_path: str,
        output_dir: str,
        max_clips: Optional[int] = None
    ) -> List[str]:
        """Split video into clips of exactly clip_duration seconds"""
        os.makedirs(output_dir, exist_ok=True)
        
        duration = self.get_video_duration(video_path)
        if duration <= 0:
            return []
        
        num_clips = int(duration // self.config.clip_duration)
        if max_clips:
            num_clips = min(num_clips, max_clips)
        
        print(f"✂️ Creating {num_clips} clips of {self.config.clip_duration}s each")
        
        clips = []
        for i in range(num_clips):
            clip_path = os.path.join(output_dir, f"clip_{i:04d}.mp4")
            start_time = i * self.config.clip_duration
            
            # Skip if exists and valid
            if os.path.exists(clip_path) and os.path.getsize(clip_path) > 0:
                clips.append(clip_path)
                continue
            
            cmd = [
                self.ffmpeg, "-y", "-i", video_path,
                "-ss", str(start_time),
                "-t", str(self.config.clip_duration),
                "-vf", "scale=1920:1080:force_original_aspect_ratio=decrease,pad=1920:1080:(ow-iw)/2:(oh-ih)/2",
                "-c:v", "libx264", "-preset", "fast", "-an",
                clip_path
            ]
            
            subprocess.run(cmd, capture_output=True, timeout=60)
            
            if os.path.exists(clip_path) and os.path.getsize(clip_path) > 0:
                clips.append(clip_path)
                if (i + 1) % 20 == 0:
                    print(f"   Created {i+1}/{num_clips} clips...")
        
        return clips
    
    def apply_fade_transition(self, input_clip: str, output_clip: str) -> bool:
        """Apply fade in/out transition to a clip"""
        duration = self.config.clip_duration
        fade_duration = 0.2
        fade_out_start = duration - fade_duration
        
        cmd = [
            self.ffmpeg, "-y", "-i", input_clip,
            "-vf", f"fade=t=in:st=0:d={fade_duration},fade=t=out:st={fade_out_start}:d={fade_duration}",
            "-c:v", "libx264", "-preset", "fast", "-an",
            output_clip
        ]
        
        result = subprocess.run(cmd, capture_output=True, timeout=60)
        return result.returncode == 0 and os.path.exists(output_clip)
    
    def copy_clip(self, input_clip: str, output_clip: str) -> bool:
        """Copy clip with re-encoding"""
        cmd = [
            self.ffmpeg, "-y", "-i", input_clip,
            "-c:v", "libx264", "-preset", "fast", "-an",
            output_clip
        ]
        
        result = subprocess.run(cmd, capture_output=True, timeout=60)
        return result.returncode == 0 and os.path.exists(output_clip)
    
    def concatenate_clips(self, clips: List[str], output_path: str) -> bool:
        """Concatenate clips into a segment"""
        if not clips:
            return False
        
        # Create concat list
        list_path = output_path + ".txt"
        with open(list_path, 'w') as f:
            for clip in clips:
                f.write(f"file '{clip}'\n")
        
        cmd = [
            self.ffmpeg, "-y",
            "-f", "concat", "-safe", "0",
            "-i", list_path,
            "-c:v", "libx264", "-preset", "fast", "-an",
            output_path
        ]
        
        result = subprocess.run(cmd, capture_output=True, timeout=120)
        
        # Cleanup
        if os.path.exists(list_path):
            os.remove(list_path)
        
        return result.returncode == 0 and os.path.exists(output_path)
    
    def create_segment(
        self,
        clips: List[str],
        output_path: str,
        segment_idx: int
    ) -> Optional[str]:
        """Create a segment from clips with transitions only"""
        
        temp_dir = output_path + "_temp"
        os.makedirs(temp_dir, exist_ok=True)
        
        processed_clips = []
        
        for i, clip in enumerate(clips):
            # Apply transition every N clips (1-indexed)
            apply_transition = (i + 1) % self.config.transition_every == 0
            
            processed_path = os.path.join(temp_dir, f"processed_{i}.mp4")
            
            if apply_transition:
                print(f"   ✨ Clip {i+1}: fade transition")
                self.apply_fade_transition(clip, processed_path)
            else:
                self.copy_clip(clip, processed_path)
            
            if os.path.exists(processed_path) and os.path.getsize(processed_path) > 0:
                processed_clips.append(processed_path)
        
        # Concatenate
        success = self.concatenate_clips(processed_clips, output_path)
        
        # Cleanup temp
        shutil.rmtree(temp_dir, ignore_errors=True)
        
        if success:
            return output_path
        return None
    
    def create_segments_from_videos(
        self,
        video_urls: List[str],
        output_dir: str,
        num_segments: int = 12,
        drive_folder_id: Optional[str] = None,
        shuffle_clips: bool = True
    ) -> Dict:
        """
        Main function: download videos, create clips, shuffle, create segments, upload to Drive.
        
        Returns a report with all created segments.
        """
        
        project_name = f"stock_{int(time.time())}"
        temp_base = tempfile.mkdtemp(prefix="stock_segments_")
        
        print("=" * 70)
        print("🎬 VIDEO STOCK SEGMENT CREATOR")
        print("=" * 70)
        print(f"📹 URLs: {len(video_urls)}")
        print(f"⏱️ Clip: {self.config.clip_duration}s | Segment: {self.config.segment_duration}s")
        print(f"📊 {self.config.clips_per_segment} clips = 1 segment")
        print(f"🎯 Target: {num_segments} segments")
        print(f"🔀 Shuffle clips: {shuffle_clips}")
        print(f"✨ Transition every {self.config.transition_every} clips")
        print("=" * 70)
        
        all_clips = []
        clips_needed = num_segments * self.config.clips_per_segment
        
        # Phase 1: Download and create clips
        print("\n📥 FASE 1: DOWNLOAD E CREAZIONE CLIP")
        
        for idx, url in enumerate(video_urls):
            video_dir = os.path.join(temp_base, f"video_{idx}")
            
            downloaded = self.download_youtube(url, video_dir)
            if not downloaded:
                continue
            
            clips_dir = os.path.join(temp_base, f"clips_{idx}")
            remaining = clips_needed - len(all_clips)
            
            if remaining <= 0:
                break
            
            clips = self.split_into_clips(downloaded, clips_dir, max_clips=remaining)
            all_clips.extend(clips)
            
            print(f"   ✅ Total clips: {len(all_clips)}")
            
            if len(all_clips) >= clips_needed:
                break
        
        print(f"\n✅ Created {len(all_clips)} clips")
        
        # Shuffle clips if requested
        if shuffle_clips and len(all_clips) > 1:
            print(f"🔀 Shuffling {len(all_clips)} clips...")
            random.shuffle(all_clips)
        
        # Phase 2: Create segments
        print("\n🎬 FASE 2: CREAZIONE SEGMENTI")
        
        segments_dir = os.path.join(temp_base, "segments")
        os.makedirs(segments_dir, exist_ok=True)
        
        segments = []
        clips_per_seg = self.config.clips_per_segment
        
        for seg_idx in range(num_segments):
            start = seg_idx * clips_per_seg
            end = start + clips_per_seg
            
            if end > len(all_clips):
                break
            
            segment_clips = all_clips[start:end]
            segment_path = os.path.join(segments_dir, f"{project_name}_segment_{seg_idx+1:03d}.mp4")
            
            print(f"\n🎬 Segment {seg_idx+1}/{num_segments}:")
            print(f"   📎 {len(segment_clips)} clips")
            
            result = self.create_segment(segment_clips, segment_path, seg_idx)
            
            if result:
                segments.append(result)
                print(f"   ✅ Created: {os.path.basename(result)}")
        
        print(f"\n✅ Created {len(segments)} segments")
        
        # Phase 3: Upload to Drive (if configured)
        uploaded = []
        
        if drive_folder_id and segments:
            print("\n☁️ FASE 3: UPLOAD DRIVE")
            
            try:
                sys.path.insert(0, os.path.dirname(__file__))
                from ScriptPython import GOOGLE_DRIVE_MANAGER
                
                for idx, seg_path in enumerate(segments):
                    seg_name = os.path.basename(seg_path)
                    try:
                        file_id = GOOGLE_DRIVE_MANAGER.upload_file(
                            seg_path,
                            folder_id=drive_folder_id,
                            custom_name=seg_name
                        )
                        if file_id:
                            uploaded.append({
                                "segment": idx + 1,
                                "name": seg_name,
                                "drive_url": f"https://drive.google.com/file/d/{file_id}/view",
                                "drive_file_id": file_id
                            })
                            print(f"   ✅ Uploaded {idx+1}/{len(segments)}: {seg_name}")
                    except Exception as e:
                        print(f"   ❌ Upload error: {e}")
                
            except ImportError:
                print("⚠️ Google Drive Manager not available, saving locally")
        
        # Generate report
        report = {
            "project": project_name,
            "config": self.config.to_dict(),
            "clips_created": len(all_clips),
            "segments_created": len(segments),
            "segments_uploaded": len(uploaded),
            "shuffle_used": shuffle_clips,
            "segments": uploaded,
            "drive_folder": drive_folder_id,
            "temp_dir": temp_base
        }
        
        # Save report
        report_path = os.path.join(temp_base, "report.json")
        with open(report_path, 'w') as f:
            json.dump(report, f, indent=2)
        
        print("\n" + "=" * 70)
        print("✅ COMPLETATO!")
        print("=" * 70)
        print(f"✂️ Clip creati: {len(all_clips)} (da {self.config.clip_duration}s)")
        print(f"🔀 Shuffle: {'Sì' if shuffle_clips else 'No'}")
        print(f"🎬 Segmenti creati: {len(segments)} (da {self.config.segment_duration}s)")
        print(f"☁️ Upload Drive: {len(uploaded)}")
        if drive_folder_id:
            print(f"📁 Drive: https://drive.google.com/drive/folders/{drive_folder_id}")
        print("=" * 70)
        
        return report


# API Endpoint function
def create_stock_segments(
    youtube_urls: List[str],
    drive_folder_id: str,
    num_segments: int = 12,
    clip_duration: float = 5,
    segment_duration: float = 25,
    transition_every: int = 4,
    shuffle_clips: bool = True
) -> Dict:
    """
    API endpoint to create stock video segments.
    
    Args:
        youtube_urls: List of YouTube URLs to download
        drive_folder_id: Google Drive folder ID for uploads
        num_segments: Number of segments to create (default: 12)
        clip_duration: Duration of each clip in seconds (default: 5)
        segment_duration: Duration of each segment in seconds (default: 25)
        transition_every: Apply fade transition every N clips (default: 4)
        shuffle_clips: Shuffle clips randomly (default: True)
    
    Returns:
        Report dictionary with created segments info
    """
    
    config = SegmentConfig(
        clip_duration=clip_duration,
        segment_duration=segment_duration,
        transition_every=transition_every
    )
    
    creator = VideoSegmentCreator(config)
    
    return creator.create_segments_from_videos(
        video_urls=youtube_urls,
        output_dir=tempfile.mkdtemp(),
        num_segments=num_segments,
        drive_folder_id=drive_folder_id,
        shuffle_clips=shuffle_clips
    )


if __name__ == "__main__":
    # Example usage
    import argparse
    
    parser = argparse.ArgumentParser(description="Create video stock segments")
    parser.add_argument("--urls", nargs="+", required=True, help="YouTube URLs")
    parser.add_argument("--drive-folder", required=True, help="Google Drive folder ID")
    parser.add_argument("--num-segments", type=int, default=12, help="Number of segments")
    parser.add_argument("--clip-duration", type=float, default=5, help="Clip duration in seconds")
    parser.add_argument("--segment-duration", type=float, default=25, help="Segment duration in seconds")
    parser.add_argument("--no-shuffle", action="store_true", help="Don't shuffle clips")
    
    args = parser.parse_args()
    
    report = create_stock_segments(
        youtube_urls=args.urls,
        drive_folder_id=args.drive_folder,
        num_segments=args.num_segments,
        clip_duration=args.clip_duration,
        segment_duration=args.segment_duration,
        shuffle_clips=not args.no_shuffle
    )
    
    print(json.dumps(report, indent=2))