"""Visual (CLIP) endpoints — compute-only, no SQLite access (QDRANT-001).

QDRANT-001 (June 2026) closure: the /index_visual, /index_visual_multi
endpoints used to write back to media.db.sqlite from inside this sidecar.
Per QDRANT-001 (single-writer rule), Go is the sole writer of SQLite.

These endpoints are now PURE compute operators: input -> embeddings JSON.
The Go caller (clipindexer.indexViaAPI) reads the response and persists
via the canonical outbox/indexed flow.

Uses APIRouter; __init__.py mounts this via `app.include_router(visual.router)`.
"""

import json
import os
import subprocess

from fastapi import APIRouter, HTTPException

from . import _inference_sem, clip_model
from .models import (
    IndexVisualMultiRequest,
    PhashRequest,
    VisualAnalyzeRequest,
    VisualEmbedRequest,
)

router = APIRouter()


def _read_clip_meta(search_text_clip_path: str) -> dict | None:
    """Read clip metadata from a sidecar JSON pointer file written by Go.
    Keeps this script free of any sqlite3 import — the canonical SQLite
    read happens on the Go side.
    """
    if not search_text_clip_path:
        return None
    from pathlib import Path
    p = Path(search_text_clip_path)
    if p.exists() and p.is_file():
        try:
            return json.loads(p.read_text(encoding="utf-8", errors="ignore"))
        except Exception:
            return None
    return None


@router.post("/embed_visual")
async def embed_visual(req: VisualEmbedRequest):
    """Generate CLIP visual embedding (512d) from text description.
    Pure compute — no DB access.
    """
    async with _inference_sem:
        try:
            embedding = clip_model.encode(req.text).tolist()
            return {"embedding": embedding, "dimensions": len(embedding)}
        except Exception as e:
            raise HTTPException(status_code=500, detail=str(e))


@router.post("/visual_analyze")
async def visual_analyze(req: VisualAnalyzeRequest):
    """Generate CLIP image embedding + perceptual hash for a local image file.
    Pure compute — caller (Go) reads dimensions and persists hash.
    """
    async with _inference_sem:
        try:
            from PIL import Image
            import imagehash

            img = Image.open(req.image_path).convert("RGB")
            embedding = clip_model.encode(img).tolist()
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
    """Compute perceptual hash of an image file. Pure compute."""
    async with _inference_sem:
        try:
            from PIL import Image
            import imagehash

            img = Image.open(req.image_path)
            h = str(imagehash.phash(img))
            return {"phash": h}
        except Exception as e:
            raise HTTPException(status_code=500, detail=str(e))


@router.post("/index_visual")
async def index_visual(req):
    """QDRANT-001 closure: compute-and-return instead of DB write.
    Kept as a route so legacy callers receive a clear 410 Gone response
    pointing to the canonical Go-owned flow. Callers should migrate to
    /embed_visual (pure embedding) and persist via the outbox dispatcher.
    """
    raise HTTPException(
        status_code=410,
        detail={
            "error": "QDRANT-001 closure",
            "message": (
                "/index_visual has been retired. The CLIP embedding is now "
                "computed by /embed_visual and persisted by Go through the "
                "outbox dispatcher (QDRANT-002). Update clipindexer callers "
                "to use /embed_visual + the canonical write path.",
            ),
            "replacement_endpoint": "/embed_visual",
            "owner": "internal/infrastructure/indexing/clipindexer",
        },
    )


@router.post("/index_visual_multi")
async def index_visual_multi(req: IndexVisualMultiRequest):
    """QDRANT-001 closure: compute multi-frame CLIP embeddings and return as
    JSON instead of writing to SQLite. The Go caller persists via outbox.

    Output schema:
        {
          "status": "success",
          "clip_id": "...",
          "field": "visual_embedding",
          "frame_embeddings": [[...512...], [...512...], [...512...]],
          "averaged_embedding": [...512...],
          "frame_count": int,
          "frame_positions": [0.2, 0.5, 0.8],
          "dimensions": 512,
        }
    """
    async with _inference_sem:
        try:
            from PIL import Image
            import numpy as np

            probe_cmd = [
                "ffprobe", "-v", "quiet", "-print_format", "json",
                "-show_format", req.video_path,
            ]
            probe_result = subprocess.run(
                probe_cmd, capture_output=True, text=True, timeout=30,
            )
            if probe_result.returncode != 0:
                raise HTTPException(
                    status_code=500,
                    detail=f"ffprobe failed: {probe_result.stderr}",
                )
            probe_data = json.loads(probe_result.stdout)
            duration = float(probe_data.get("format", {}).get("duration", 0))
            if duration <= 0:
                raise HTTPException(
                    status_code=500,
                    detail=f"Invalid video duration: {duration}",
                )

            frame_embeddings: list[list[float]] = []
            positions_used: list[float] = []
            for pct in req.frame_positions:
                timestamp = duration * pct
                frame_path = f"/tmp/frame_{req.clip_id}_{pct}.jpg"
                extract_cmd = [
                    "ffmpeg", "-y", "-ss", f"{timestamp:.3f}",
                    "-i", req.video_path,
                    "-frames:v", "1", "-q:v", "2", frame_path,
                ]
                extract_result = subprocess.run(
                    extract_cmd, capture_output=True, text=True, timeout=60,
                )
                if extract_result.returncode != 0:
                    print(
                        f"Warning: frame extraction failed at "
                        f"{pct*100:.0f}%: {extract_result.stderr}"
                    )
                    continue
                try:
                    img = Image.open(frame_path).convert("RGB")
                    embedding = clip_model.encode(img).tolist()
                    frame_embeddings.append(embedding)
                    positions_used.append(pct)
                finally:
                    try:
                        os.remove(frame_path)
                    except OSError:
                        pass

            if not frame_embeddings:
                raise HTTPException(
                    status_code=500, detail="No frames could be extracted",
                )

            avg = np.mean(frame_embeddings, axis=0).tolist()

            return {
                "status": "success",
                "clip_id": req.clip_id,
                "field": "visual_embedding",
                "frame_embeddings": frame_embeddings,
                "averaged_embedding": avg,
                "frame_count": len(frame_embeddings),
                "frame_positions": positions_used,
                "dimensions": len(avg),
            }
        except HTTPException:
            raise
        except Exception as e:
            raise HTTPException(status_code=500, detail=str(e))
