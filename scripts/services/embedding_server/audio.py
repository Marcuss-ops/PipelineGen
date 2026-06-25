"""Audio (CLAP) endpoints — compute-only, no SQLite access (QDRANT-001).

QDRANT-001 (June 2026) closure: the /index_audio endpoint used to write
embedding back to media.db.sqlite from inside this sidecar. Per
QDRANT-001 (single-writer rule), Go is the sole writer of SQLite.

/embed_audio remains as a pure embedding endpoint.
"""

from fastapi import APIRouter, HTTPException

from . import _inference_sem, clap_model
from .models import EmbedRequest

router = APIRouter()


@router.post("/embed_audio")
async def embed_audio(req: EmbedRequest):
    """Generate CLAP audio embedding (512d) from text description.

    Returns 501 if the CLAP model didn't load — endpoints stay registered
    so the Go side can detect that and decide to fall back to text-only
    retrieval.
    """
    if clap_model is None:
        raise HTTPException(status_code=501, detail="CLAP model not loaded")
    async with _inference_sem:
        try:
            embedding = clap_model.encode(req.text).tolist()
            return {"embedding": embedding, "dimensions": len(embedding)}
        except Exception as e:
            raise HTTPException(status_code=500, detail=str(e))


@router.post("/index_audio")
async def index_audio(req):
    """QDRANT-001 closure: 410 Gone with migration message."""
    raise HTTPException(
        status_code=410,
        detail={
            "error": "QDRANT-001 closure",
            "message": (
                "/index_audio has been retired. The CLAP embedding is now "
                "produced by /embed_audio and persisted by Go through the "
                "outbox dispatcher (QDRANT-002). Update clipindexer callers "
                "to use /embed_audio + the canonical write path.",
            ),
            "replacement_endpoint": "/embed_audio",
            "owner": "internal/infrastructure/indexing/clipindexer",
        },
    )
