"""Visual (CLIP) endpoints — image-based embeddings + perceptual hash.

Uses APIRouter; __init__.py mounts this via `app.include_router(visual.router)`.
"""

import json
import os
import subprocess

from fastapi import APIRouter, HTTPException

from . import _inference_sem, siglip_model
from .models import (
    ImageEmbedRequest,
    IndexVisualMultiRequest,
    IndexVisualRequest,
    PhashRequest,
    VisualAnalyzeRequest,
    VisualEmbedRequest,
)

router = APIRouter()


@router.post("/index_visual")
async def index_visual(req: IndexVisualRequest):
    """Generate SigLIP embedding from image file and return it to Go."""
    async with _inference_sem:
        try:
            from PIL import Image
            import imagehash

            img = Image.open(req.frame_path)
            embedding = siglip_model.encode(img).tolist()
            h = str(imagehash.phash(img))
            return {
                "status": "success",
                "clip_id": req.clip_id,
                "embedding": embedding,
                "phash": h,
                "dimensions": len(embedding),
            }
        except Exception as e:
            import traceback
            print(traceback.format_exc())
            raise HTTPException(status_code=500, detail=str(e))


@router.post("/embed_visual")
async def embed_visual(req: VisualEmbedRequest):
    """Generate SigLIP visual embedding (768d) from text description.

    Uses SigLIP's text encoder to produce a visual-aligned embedding.
    For image-file embeddings, use /embed_visual_from_image.
    """
    async with _inference_sem:
        try:
            embedding = siglip_model.encode(req.text).tolist()
            return {"embedding": embedding, "dimensions": len(embedding)}
        except Exception as e:
            raise HTTPException(status_code=500, detail=str(e))


@router.post("/embed_visual_from_image")
async def embed_visual_from_image(req: ImageEmbedRequest):
    """Generate SigLIP visual embedding (768d) from an image file.

    Uses SigLIP's image encoder. Returns 501 if PIL is unavailable.
    """
    async with _inference_sem:
        try:
            from PIL import Image
            img = Image.open(req.image_path).convert("RGB")
            embedding = siglip_model.encode(img).tolist()
            return {"embedding": embedding, "dimensions": len(embedding)}
        except Exception as e:
            raise HTTPException(status_code=500, detail=str(e))


@router.post("/index_visual_multi")
async def index_visual_multi(req: IndexVisualMultiRequest):
    """Generate multi-frame CLIP embeddings from a video file.

    Extracts frames at 20%, 50%, 80% of video duration (configurable).
    Returns 3 SigLIP embeddings and their averaged embedding.
    """
    async with _inference_sem:
        try:
            from PIL import Image

            # Get video duration via ffprobe
            probe_cmd = [
                "ffprobe", "-v", "quiet", "-print_format", "json",
                "-show_format", req.video_path,
            ]
            probe_result = subprocess.run(probe_cmd, capture_output=True, text=True, timeout=30)
            if probe_result.returncode != 0:
                raise HTTPException(status_code=500, detail=f"ffprobe failed: {probe_result.stderr}")

            probe_data = json.loads(probe_result.stdout)
            duration = float(probe_data.get("format", {}).get("duration", 0))
            if duration <= 0:
                raise HTTPException(status_code=500, detail=f"Invalid video duration: {duration}")

            frame_embeddings = []
            for pct in req.frame_positions:
                timestamp = duration * pct

                # Extract frame
                frame_path = f"/tmp/frame_{req.clip_id}_{pct}.jpg"
                extract_cmd = [
                    "ffmpeg", "-y", "-ss", f"{timestamp:.3f}",
                    "-i", req.video_path,
                    "-frames:v", "1", "-q:v", "2", frame_path,
                ]
                extract_result = subprocess.run(extract_cmd, capture_output=True, text=True, timeout=60)
                if extract_result.returncode != 0:
                    print(f"Warning: frame extraction failed at {pct*100:.0f}%: {extract_result.stderr}")
                    continue

                # Generate SigLIP embedding
                img = Image.open(frame_path).convert("RGB")
                embedding = siglip_model.encode(img).tolist()
                frame_embeddings.append(embedding)

                # Cleanup temp frame
                try:
                    os.remove(frame_path)
                except OSError:
                    pass

            if not frame_embeddings:
                raise HTTPException(status_code=500, detail="No frames could be extracted")

            # Compute averaged embedding (backward-compatible main vector)
            import numpy as np
            avg_embedding = np.mean(frame_embeddings, axis=0).tolist()

            return {
                "status": "success",
                "clip_id": req.clip_id,
                "frame_count": len(frame_embeddings),
                "frame_positions": req.frame_positions[:len(frame_embeddings)],
                "frame_embeddings": frame_embeddings,
                "averaged_embedding": avg_embedding,
                "dimensions": len(avg_embedding),
            }
        except HTTPException:
            raise
        except Exception as e:
            import traceback
            print(traceback.format_exc())
            raise HTTPException(status_code=500, detail=str(e))


@router.post("/visual_analyze")
async def visual_analyze(req: VisualAnalyzeRequest):
    """Generate CLIP image embedding + perceptual hash for a local image file."""
    async with _inference_sem:
        try:
            from PIL import Image
            import imagehash

            img = Image.open(req.image_path).convert("RGB")
            embedding = siglip_model.encode(img).tolist()
            h = str(imagehash.phash(img))
            width, height = img.size
            return {
                "embedding": embedding,
                "phash": h,
                "dimensions": len(embedding),
                "width": width,
                "height": height,
            }
        except Exception as e:
            raise HTTPException(status_code=500, detail=str(e))


@router.post("/phash")
async def compute_phash(req: PhashRequest):
    """Compute perceptual hash of an image file (used during clip indexing)."""
    async with _inference_sem:
        try:
            from PIL import Image
            import imagehash

            img = Image.open(req.image_path)
            h = str(imagehash.phash(img))
            return {"phash": h}
        except Exception as e:
            raise HTTPException(status_code=500, detail=str(e))
