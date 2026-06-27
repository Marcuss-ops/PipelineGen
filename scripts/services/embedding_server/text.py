"""Text-embedding endpoints (multilingual-e5-base) + text normalization.

QDRANT-001 (June 2026) closure: the /index, /index_transcript, and /index_bulk
endpoints used to read clip rows from media.db.sqlite and write embedding
results back to SQLite inside this sidecar. Per QDRANT-001 (single-writer
rule), Go is now the sole writer of SQLite.

These endpoints are now PURE compute operators:
  input  → JSON body {clip_id, name, search_text, [transcript_path]}
  output → JSON body {clip_id, field, embedding, dimensions, ...}

The Go caller (clipindexer.indexViaAPI / indexBulkAPI) reads the
embedding JSON from the response and persists it via the canonical
outbox/indexed flow (QDRANT-002 PR4 contract).

Uses APIRouter; __init__.py mounts the router on the FastAPI app via
app.include_router(text.router).
"""

from pathlib import Path

from fastapi import APIRouter, HTTPException

from . import TEXT_MODEL_NAME, TEXT_MODEL_VERSION, _inference_sem, model, nlp, nlp_it
from .models import EmbedRequest, IndexBulkRequest, IndexTextRequest

router = APIRouter()


def normalize_text(text: str) -> str:
    """Lemmatize + stop-word removal. Italian is detected via stop-word
    heuristics; falls back to the English pipeline if Italian is unavailable."""
    italian_stopwords = {
        "il", "la", "i", "gli", "le", "un", "una", "di", "a",
        "da", "in", "con", "su", "per", "tra", "fra", "che",
    }
    words = text.lower().split()
    is_italian = any(w in italian_stopwords for w in words)
    target_nlp = nlp_it if (is_italian and nlp_it) else nlp

    doc = target_nlp(text.lower())
    return " ".join(
        [token.lemma_ for token in doc if not token.is_stop and not token.is_punct]
    )


@router.post("/embed")
async def embed(req: EmbedRequest):
    """Generate text embedding (768d, intfloat/multilingual-e5-base).
    Per E5 recommendation for asymmetric retrieval:
    - type='query' (default): adds 'query:' prefix for search queries
    - type='passage': adds 'passage:' prefix for document indexing
    See: https://huggingface.co/intfloat/multilingual-e5-base
    """
    async with _inference_sem:
        try:
            prefix = "query: " if req.type == "query" else "passage: "
            normalized = normalize_text(req.text)
            prefixed = prefix + normalized
            embedding = model.encode(prefixed, normalize_embeddings=True).tolist()
            return {
                "embedding": embedding,
                "dimensions": len(embedding),
                "normalized_text": normalized,
                "type": req.type,
                "model": TEXT_MODEL_NAME,
                "model_version": TEXT_MODEL_VERSION,
            }
        except Exception as e:
            raise HTTPException(status_code=500, detail=str(e))


@router.post("/index")
async def index_text(req: IndexTextRequest):
    """QDRANT-001 closure: compute-and-return. Caller (Go) persists.

    QDRANT-001 review fix: the request body carries `name` and `search_text`
    directly (Go is canonical owner of media_assets). This endpoint never
    touches SQLite.

    Response:
        {"status": "success", "clip_id": "...", "field": "embedding_json",
         "embedding": [...768 floats...], "dimensions": 768, "text_length": int}
    """
    async with _inference_sem:
        try:
            text = (req.search_text or req.name or "").strip()
            if not text:
                raise HTTPException(
                    status_code=400,
                    detail=(
                        f"clip {req.clip_id or '<unknown>'} has no "
                        "search_text or name in request body"
                    ),
                )
            normalized = normalize_text(text)
            prefixed = "passage: " + normalized
            embedding = model.encode(prefixed, normalize_embeddings=True).tolist()

            return {
                "embedding": embedding,
                "dimensions": len(embedding),
                "model": TEXT_MODEL_NAME,
                "model_version": TEXT_MODEL_VERSION,
            }
        except HTTPException:
            raise
        except Exception as e:
            raise HTTPException(status_code=500, detail=str(e))


@router.post("/index_transcript")
async def index_transcript(req: IndexTextRequest):
    """QDRANT-001 closure: transcript compute-and-return. Caller persists."""
    async with _inference_sem:
        try:
            transcript_text = ""
            if req.transcript_path:
                p = Path(req.transcript_path)
                if p.exists() and p.is_file():
                    transcript_text = p.read_text(
                        encoding="utf-8", errors="ignore"
                    ).strip()

            if not transcript_text:
                return {
                    "status": "skipped",
                    "reason": "no transcript file provided",
                }

            normalized = normalize_text(transcript_text)
            prefixed = "passage: " + normalized
            embedding = model.encode(prefixed, normalize_embeddings=True).tolist()

            return {
                "embedding": embedding,
                "dimensions": len(embedding),
                "model": TEXT_MODEL_NAME,
                "model_version": TEXT_MODEL_VERSION,
            }
        except HTTPException:
            raise
        except Exception as e:
            raise HTTPException(status_code=500, detail=str(e))


@router.post("/index_bulk")
async def index_bulk(req: IndexBulkRequest):
    """QDRANT-001 closure: bulk compute-and-return. Caller persists.

    Input: list of clip specs {clip_id, name, search_text}.
    Output: list of {clip_id, field, embedding, dimensions, text_length, status}.

    Replaces the previous flow that opened SQLite inside this sidecar.
    """
    async with _inference_sem:
        try:
            results: list[dict] = []
            for clip in req.clips:
                text = (clip.search_text or clip.name or "").strip()
                if not text:
                    results.append({
                        "status": "skipped",
                        "clip_id": clip.clip_id,
                        "reason": "no search_text or name",
                    })
                    continue
                normalized = normalize_text(text)
                prefixed = "passage: " + normalized
                embedding = model.encode(prefixed, normalize_embeddings=True).tolist()
                results.append({
                    "clip_id": clip.clip_id,
                    "embedding": embedding,
                    "dimensions": len(embedding),
                    "model": TEXT_MODEL_NAME,
                    "model_version": TEXT_MODEL_VERSION,
                })
            return {"status": "success", "count": len(results), "total": len(req.clips), "results": results}
        except Exception as e:
            raise HTTPException(status_code=500, detail=str(e))
