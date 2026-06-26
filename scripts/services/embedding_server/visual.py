"""Visual (SigLIP) endpoints — image-based embeddings + perceptual hash.

Uses APIRouter; __init__.py mounts this via `app.include_router(visual.router)`.
"""

from fastapi import APIRouter, HTTPException

from . import (
    VISUAL_MODEL_NAME,
    VISUAL_MODEL_VERSION,
    _inference_sem,
    siglip_model,
)
from .models import (
    ImageEmbedRequest,
    PhashRequest,
    VisualAnalyzeRequest,
    VisualEmbedRequest,
)

router = APIRouter()


@router.post("/embed_visual")
async def embed_visual(req: VisualEmbedRequest):
    """Generate SigLIP visual embedding (768d) from text description.

    Uses SigLIP's text encoder to produce a visual-aligned embedding.
    For image-file embeddings, use /embed_visual_from_image.
    """
    async with _inference_sem:
        try:
            embedding = siglip_model.encode(req.text).tolist()
            return {
                "embedding": embedding,
                "dimensions": len(embedding),
                "model": VISUAL_MODEL_NAME,
                "model_version": VISUAL_MODEL_VERSION,
            }
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
            return {
                "embedding": embedding,
                "dimensions": len(embedding),
                "model": VISUAL_MODEL_NAME,
                "model_version": VISUAL_MODEL_VERSION,
            }
        except Exception as e:
            raise HTTPException(status_code=500, detail=str(e))


@router.post("/visual_analyze")
async def visual_analyze(req: VisualAnalyzeRequest):
    """Generate SigLIP visual embedding + perceptual hash for a local image file."""
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
                "model": VISUAL_MODEL_NAME,
                "model_version": VISUAL_MODEL_VERSION,
                "width": width,
                "height": height,
            }
        except Exception as e:
            raise HTTPException(status_code=500, detail=str(e))


@router.post("/phash")
async def compute_phash(req: PhashRequest):
    """Compute perceptual hash of an image file (used during media asset indexing)."""
    async with _inference_sem:
        try:
            from PIL import Image
            import imagehash

            img = Image.open(req.image_path)
            h = str(imagehash.phash(img))
            return {"phash": h}
        except Exception as e:
            raise HTTPException(status_code=500, detail=str(e))
