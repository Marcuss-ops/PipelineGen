# AI Generation System Documentation

This document explains how to use the AI-powered image and video generation features in PipelineGen.

## Overview

PipelineGen integrates NVIDIA NIM and Flux models to generate high-quality images, which can then be automatically animated into 1080p zoom-out videos. These assets are fully integrated with the system:
- **Deduplication**: Automatic SHA-256 hashing to avoid duplicate files.
- **Database Sync**: Metadata stored in `images.db.sqlite` (for static/animated images) and optionally `stock.db.sqlite` (for video clips).
- **Google Drive Integration**: Automatic upload of both images and generated videos.

## Image Generation

### Models Available
- `flux-1-dev`: Highest quality (Black Forest Labs via NVIDIA cloud).
- `flux-2-klein`: High speed, good quality (Black Forest Labs via NVIDIA cloud).
- `local-nim`: Local inference using NVIDIA NIM (running on port 8000).

### API Endpoint: `POST /api/images/generate/nvidia`
Generates an image from a prompt.

**Request Payload:**
```json
{
  "prompt": "A futuristic city at sunset, cinematic lighting, highly detailed",
  "model": "flux-1-dev",
  "width": 1280,
  "height": 720
}
```

**What happens behind the scenes:**
1. The image is generated via the selected model.
2. The image is downloaded and saved to `data/images/<prompt-slug>/<hash>.png`.
3. The image hash is calculated and checked for duplicates.
4. The image is uploaded to the Google Drive folder configured in `config.yaml`.
5. A record is created in `images.db.sqlite`.

## Video Animation (Zoom-Out)

You can transform any generated image into a 7-second, 1080p zoom-out video using FFmpeg.

### API Endpoint: `POST /api/images/animate`
Creates a video from an existing image hash.

**Request Payload:**
```json
{
  "image_hash": "a1b2c3d4...",
  "duration": 7
}
```

**What happens behind the scenes:**
1. The system finds the image locally via its hash.
2. It runs `scripts/animate_image.py` which uses FFmpeg to:
   - Apply a smooth zoom-out pan (from 1.5x to 1.0x).
   - Upscale the image to 1920x1080.
   - Encode it as high-quality H.264 MP4.
3. The resulting video is saved in `data/animations/`.
4. *Future*: The video is uploaded to Drive and registered as a `stock` asset.

## Integration with Stock DB

The system is designed to allow these generated "AI Videos" to be used as regular stock footage.

### Manual Ingestion via CLI
You can use the provided scripts for testing:
- `python3 scripts/test_nvidia_images.py`: Test image generation.
- `python3 scripts/animate_image.py <image_path>`: Manually create animations.

## Configuration

In `config.yaml`, ensure you have your NVIDIA API key:
```yaml
external:
  nvidia_api_key: "nvapi-..."
  nvidia_model: "flux-1-dev"

drive:
  images_root_folder: "YOUR_DRIVE_FOLDER_ID"
```

## Database Schema (Images)

The `images.db.sqlite` contains:
- `subjects`: Organized by slug (e.g., "mike-tyson").
- `images`: Contains `file_hash`, `local_path`, `drive_file_id`, and `source_url`.
