import subprocess
import os
import argparse
import sys

def create_zoom_out(input_img, output_video, duration=7, fps=30):
    """
    Creates a zoom-out animation from a static image and upscales it to 1080p.
    """
    if not os.path.exists(input_img):
        print(f"Error: Input image {input_img} not found.")
        return False

    # ffmpeg zoompan filter explanation:
    # z: zoom level. We start at 1.5 and decrease slightly each frame.
    # d: duration in frames (duration * fps).
    # s: output resolution (1920x1080).
    # x, y: centering the zoom.
    
    # Calculate step to go from 1.5 to 1.0 over 'd' frames
    total_frames = duration * fps
    zoom_start = 1.5
    zoom_end = 1.0
    step = (zoom_start - zoom_end) / total_frames

    vf = (
        f"scale=iw*2:-1,zoompan=z='max({zoom_end}, {zoom_start}-on*{step})':"
        f"d={total_frames}:s=1920x1080:fps={fps}:"
        f"x='iw/2-(iw/zoom/2)':y='ih/2-(ih/zoom/2)'"
    )
    
    cmd = [
        'ffmpeg', '-y',
        '-loop', '1',
        '-i', input_img,
        '-vf', vf,
        '-c:v', 'libx264',
        '-t', str(duration),
        '-pix_fmt', 'yuv420p',
        output_video
    ]
    
    print(f"Executing: {' '.join(cmd)}")
    try:
        # Using capture_output=False to show progress in logs if needed, 
        # but for now we keep it quiet unless error
        subprocess.run(cmd, check=True, capture_output=True, text=True)
        print(f"Success! Animation saved to {output_video}")
        return True
    except subprocess.CalledProcessError as e:
        print(f"Error executing ffmpeg: {e}")
        print(f"ffmpeg output: {e.stderr}")
        return False

if __name__ == "__main__":
    parser = argparse.ArgumentParser(description="Animate an image with zoom-out effect and upscale to 1080p")
    parser.add_argument("input", help="Path to input image")
    parser.add_argument("--output", default="animation.mp4", help="Path to output video")
    parser.add_argument("--duration", type=int, default=7, help="Duration in seconds")
    
    args = parser.parse_args()
    
    if not create_zoom_out(args.input, args.output, args.duration):
        sys.exit(1)
