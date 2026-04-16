#!/usr/bin/env python3
import argparse
import sys
from pathlib import Path
import json

from src.config.settings import ProjectConfig
from src.downloader.youtube import YouTubeDownloader
from src.clipper.splitter import VideoClipper
from src.clipper.concatenator import VideoConcatenator
from src.effects.stylize import VideoEffects
from src.uploader.drive import DriveUploader
from src.utils.logging_utils import setup_logging, logger
from src.utils.retry_utils import retry, RetryConfig
from src.utils.parallel_utils import parallel_map

def load_input_config(config_file: Path) -> dict:
    with open(config_file, "r") as f:
        return json.load(f)

@retry(max_attempts=3, delay=2.0, backoff=2.0, exceptions=(Exception,))
def download_single_video(downloader, url):
    return downloader.download(url)

def main():
    parser = argparse.ArgumentParser(description="YouTube Video Stock Processor")
    parser.add_argument("--config", type=str, help="Path to input config JSON file")
    parser.add_argument("--drive-folder", type=str, help="Google Drive folder ID (parent)")
    parser.add_argument("--folder-name", type=str, help="Name for the output folder in Drive")
    parser.add_argument("--max-per-folder", type=int, default=49, help="Max files per folder (default: 49)")
    parser.add_argument("--workers", type=int, default=2, help="Parallel download workers (default: 2)")
    parser.add_argument("--log-level", type=str, default="INFO", choices=["DEBUG", "INFO", "WARNING", "ERROR"])
    parser.add_argument("--dry-run", action="store_true", help="Show what would be done")
    args = parser.parse_args()
    
    import logging
    log_level = getattr(logging, args.log_level)
    setup_logging(level=log_level)
    
    logger.info("=== Stock Processor Started ===")
    
    if not args.config:
        print("Error: --config required")
        print("Example config:")
        example = {
            "urls": ["https://youtube.com/watch?v=...", "https://youtube.com/playlist?list=..."],
            "final_duration": 1200,
            "clip_duration": 5,
            "segment_duration": 25
        }
        print(json.dumps(example, indent=2))
        sys.exit(1)
    
    config_path = Path(args.config)
    if not config_path.exists():
        logger.error(f"Config file not found: {config_path}")
        sys.exit(1)
    
    config = load_input_config(config_path)
    
    urls = config.get("urls", [])
    final_duration = config.get("final_duration", 1200)
    clip_duration = config.get("clip_duration", 5)
    segment_duration = config.get("segment_duration", 25)
    
    logger.info(f"Config loaded: {len(urls)} URLs, {final_duration}s final, {clip_duration}s clips, {segment_duration}s segments")
    print(f"\nConfig: {len(urls)} URLs, {final_duration}s ({final_duration//60}min), clips:{clip_duration}s, segments:{segment_duration}s")
    
    if args.dry_run:
        logger.info("Dry run mode - URLs that would be processed:")
        for url in urls:
            print(f"  - {url}")
        return
    
    project_dir = Path("project_output")
    downloads_dir = project_dir / "downloads"
    clips_dir = project_dir / "clips"
    segments_dir = project_dir / "segments"
    
    for d in [downloads_dir, clips_dir, segments_dir]:
        d.mkdir(parents=True, exist_ok=True)
    
    print("\n=== STEP 1: Downloading videos (parallel) ===")
    logger.info(f"Starting parallel download with {args.workers} workers")
    downloader = YouTubeDownloader(downloads_dir, quality="1080p")
    
    def download_with_retry(url):
        try:
            videos = download_single_video(downloader, url)
            return videos[0] if videos else None
        except Exception as e:
            logger.error(f"Failed to download {url}: {e}")
            return None
    
    videos = parallel_map(download_with_retry, urls, max_workers=args.workers, description="Download")
    videos = [v for v in videos if v is not None]
    
    logger.info(f"Download complete: {len(videos)} videos")
    print(f"Downloaded {len(videos)}/{len(urls)} videos")
    
    print("\n=== STEP 2: Splitting into clips ===")
    logger.info("Splitting videos into clips")
    clipper = VideoClipper(clips_dir, clip_duration=clip_duration)
    clips = clipper.split_multiple(videos)
    logger.info(f"Created {len(clips)} clips")
    print(f"Created {len(clips)} clips")
    
    print("\n=== STEP 3: Applying effects and transitions ===")
    logger.info("Applying random effects and transitions")
    processed_clips = []
    for i, clip in enumerate(clips):
        if i % 4 == 0:
            processed = clipper.apply_random_transition(clip)
        elif i % 5 == 0:
            processed = clipper.apply_random_effect(clip)
        else:
            processed = clip
        processed_clips.append(processed)
    logger.info(f"Processed {len(processed_clips)} clips")
    print(f"Processed {len(processed_clips)} clips")
    
    print("\n=== STEP 4: Creating segments ===")
    logger.info("Creating segments")
    concatenator = VideoConcatenator(segments_dir)
    segments = concatenator.shuffle_and_concatenate(
        processed_clips, 
        "segment", 
        segment_duration
    )
    logger.info(f"Created {len(segments)} segments")
    print(f"Created {len(segments)} segments")
    
    print("\n=== STEP 5: Uploading to Google Drive ===")
    if args.drive_folder:
        logger.info("Starting Google Drive upload")
        uploader = DriveUploader()
        folder_name = args.folder_name or "Stock_Export"
        
        print(f"Uploading {len(segments)} segments...")
        logger.info(f"Upload: folder={folder_name}, max_per_folder={args.max_per_folder}")
        
        @retry(max_attempts=3, delay=3.0, backoff=2.0, exceptions=(Exception,))
        def upload_with_retry(uploader, files, folder, parent):
            return uploader.upload_with_folder_split(files, folder, parent, args.max_per_folder)
        
        try:
            results = upload_with_retry(uploader, segments, folder_name, args.drive_folder)
            
            print("\n=== Upload Complete ===")
            logger.info("Upload complete")
            for folder, data in results.items():
                print(f"\n{folder}:")
                for link in data['links']:
                    print(f"  {link}")
        except Exception as e:
            logger.error(f"Upload failed: {e}")
            print(f"Upload failed: {e}")
    else:
        logger.info("No drive folder specified, skipping upload")
        print("No drive folder specified, skipping upload")
        print(f"Segments saved to: {segments_dir}")
    
    logger.info("=== Stock Processor Finished ===")

if __name__ == "__main__":
    main()
