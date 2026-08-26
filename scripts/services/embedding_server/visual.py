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
    BatchImageEmbedRequest,
    ImageEmbedRequest,
    PhashRequest,
    VisualAnalyzeRequest,
    VisualEmbedRequest,
)

router = APIRouter()

def _require_siglip():
    if siglip_model is None:
        raise HTTPException(status_code=501, detail="SigLIP model not loaded (set SKIP_SIGLIP=0 and restart)")


@router.post("/embed_visual")
async def embed_visual(req: VisualEmbedRequest):
    """Generate SigLIP visual embedding (768d) from text description.

    Uses SigLIP's text encoder to produce a visual-aligned embedding.
    For image-file embeddings, use /embed_visual_from_image.
    """
    _require_siglip()
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
    _require_siglip()
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


# ── batch endpoint (godlike/07 fail-closed, signer wire) ────────────────
# Single HTTP round-trip for N image embeddings. The Go caller uses this
# whenever it would otherwise issue N sequential /embed_visual_from_image
# requests (e.g. video-frame backfills, slide-deck ingestion). All N
# forward passes happen under ONE _inference_sem acquisition so concurrent
# batch callers serialize rather than fan out across the INFERENCE_CONCURRENCY
# semaphore slots (protects against CPU + RAM exhaustion when callers
# bulk-load N=512 in parallel).
@router.post("/embed_visual_from_images")
async def embed_visual_from_images(req: BatchImageEmbedRequest):
    """Generate SigLIP visual embeddings (768d each) for a list of image files.

    Response envelope (order-preserved, response[i] corresponds to
    request.image_paths[i]):

        {
          "embeddings": [[768 floats], [768 floats], ...],
          "dimensions": 768,
          "count": N,
          "model": VISUAL_MODEL_NAME,
          "model_version": VISUAL_MODEL_VERSION
        }

    Semi-trusted callers: trust that ALL paths succeed or we surface a
    single typed error for the whole batch. NO partial-success arrays.
    Fail-closed semantics (godlike/07):
      - SKIP_SIGLIP=1 (or model load failure) ⇒ HTTP 501 (re-uses _require_siglip()).
      - empty / oversized batch ⇒ HTTP 422 (Pydantic validator).
      - PIL read failure on any path ⇒ HTTP 500 with detail
        "image_paths[<idx>] PIL open failed: ...". Caller must retry the
        WHOLE batch; no silent drop of failing items.
      - any non-200 ⇒ typed error.
    """
    _require_siglip()
    async with _inference_sem:
        try:
            from PIL import Image

            imgs = []
            for idx, p in enumerate(req.image_paths):
                try:
                    img = Image.open(p).convert("RGB")
                    imgs.append(img)
                except Exception as e:
                    raise HTTPException(
                        status_code=500,
                        detail=f"image_paths[{idx}] PIL open failed: {type(e).__name__}: {e}",
                    )

            # True vectorised batched inference. siglip_model.encode accepts a
            # list of PIL Images and produces one batched forward pass — much
            # faster than N serial encode() calls under the same semaphore.
            embeddings = siglip_model.encode(imgs).tolist()

            # Per-vector cross-shape uniformity — godlike/06 SSOT invariant.
            if not embeddings:
                raise HTTPException(status_code=500, detail="empty embeddings list from siglip_model.encode")
            canonical = len(embeddings[0])
            for i, v in enumerate(embeddings):
                if len(v) != canonical:
                    raise HTTPException(
                        status_code=500,
                        detail=f"embeddings[{i}] dimension drift: {len(v)} vs canonical {canonical}",
                    )

            return {
                "embeddings": embeddings,
                "dimensions": canonical,
                "count": len(embeddings),
                "model": VISUAL_MODEL_NAME,
                "model_version": VISUAL_MODEL_VERSION,
            }
        except HTTPException:
            raise  # pass through typed errors
        except Exception as e:
            raise HTTPException(status_code=500, detail=str(e))


@router.post("/visual_analyze")
async def visual_analyze(req: VisualAnalyzeRequest):
    """Generate SigLIP visual embedding + perceptual hash for a local image file."""
    _require_siglip()
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
