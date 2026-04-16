#!/usr/bin/env python3
"""
Fast Video Generator - Optimized for Speed
This script provides a quick way to generate videos with maximum performance settings.
"""

import os
import sys
import time
from pathlib import Path
from performance_config import *

# Add current directory to path for imports
sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))

try:
    from workflow import VideoGenerationWorkflow
    from config import *
except ImportError as e:
    print(f"Import error: {e}")
    print("Make sure you're running from the correct directory")
    sys.exit(1)

def fast_video_generator(input_path, output_path=None, use_gpu=True):
    """
    Generate video with maximum speed optimization.
    
    Args:
        input_path: Path to input file (audio or video)
        output_path: Output video path (optional)
        use_gpu: Whether to use GPU acceleration
    """
    
    start_time = time.time()
    
    if not output_path:
        output_path = str(Path(input_path).with_suffix('.mp4'))
    
    print(f"🚀 Starting FAST video generation...")
    print(f"📁 Input: {input_path}")
    print(f"📁 Output: {output_path}")
    print(f"💻 CPU Cores: {os.cpu_count()}")
    print(f"🎮 GPU: {'Enabled' if use_gpu else 'Disabled'}")
    
    # Override config with performance settings
    config_overrides = {
        'base_w': 1280,  # 720p for speed
        'base_h': 720,
        'base_fps': 30,
        'max_workers_segment_gen': MAX_WORKERS_SEGMENT_GEN,
        'max_workers_general': MAX_WORKERS_GENERAL,
        'use_gpu': use_gpu,
        'ffmpeg_preset': 'medium',
        'crf': 28,
        'threads': 0,  # Use all cores
    }
    
    workflow = VideoGenerationWorkflow()
    
    try:
        # Generate video with optimized settings
        result = workflow.process_single_video(
            input_path=input_path,
            output_path=output_path,
            **config_overrides
        )
        
        elapsed_time = time.time() - start_time
        
        if result and os.path.exists(output_path):
            file_size = os.path.getsize(output_path) / (1024 * 1024)  # MB
            print(f"✅ Video generated successfully!")
            print(f"⏱️  Time: {elapsed_time:.1f} seconds")
            print(f"📊 Size: {file_size:.1f} MB")
            print(f"🎯 Speed: {file_size/elapsed_time:.1f} MB/s")
            return output_path
        else:
            print("❌ Generation failed")
            return None
            
    except Exception as e:
        print(f"❌ Error during generation: {e}")
        return None

def main():
    """CLI interface for fast video generation."""
    
    if len(sys.argv) < 2:
        print("Usage: python fast_video_generator.py <input_file> [output_file]")
        print("Example: python fast_video_generator.py input.mp3 output.mp4")
        sys.exit(1)
    
    input_file = sys.argv[1]
    output_file = sys.argv[2] if len(sys.argv) > 2 else None
    
    if not os.path.exists(input_file):
        print(f"❌ Input file not found: {input_file}")
        sys.exit(1)
    
    # Check if GPU is available
    use_gpu = False
    try:
        import subprocess
        result = subprocess.run(['ffmpeg', '-hwaccels'], capture_output=True, text=True)
        use_gpu = 'cuda' in result.stdout.lower() or 'nvenc' in result.stdout.lower()
    except:
        pass
    
    print("🚀 FAST VIDEO GENERATOR 🚀")
    print("=" * 50)
    
    result = fast_video_generator(input_file, output_file, use_gpu)
    
    if result:
        print(f"\n🎉 Video ready: {result}")
    else:
        print("\n💥 Generation failed. Check logs for details.")

if __name__ == "__main__":
    main()