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

from . import TEXT_MODEL_NAME, TEXT_MODEL_VERSION, _inference_sem, model, nlp
from .models import EmbedRequest, IndexBulkRequest, IndexTextRequest

router = APIRouter()


def normalize_text(text: str, language: str = "") -> str:
    """Normalize with the explicitly selected model only.

    Language classification belongs to the Go lexicon registry. The sidecar
    never guesses a language from an embedded word list.
    """
    _ = language
    doc = nlp(text.lower())
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
    Output: {status, total, successful, skipped, failed, results}.

    Status derivation:
      - all clips embedded successfully  → "success"
      - some skipped / some failed      → "partial"
      - every clip failed or skipped    → "failed"

    Per-clip embedding errors are caught individually so one bad clip
    does not abort the entire batch. The caller inspects the per-item
    status field (absent = success, "skipped", "failed") to decide
    which entries to persist.

    Replaces the previous flow that opened SQLite inside this sidecar.
    """
    async with _inference_sem:
        try:
            results: list[dict] = []
            successful = 0
            skipped = 0
            failed = 0
            for clip in req.clips:
                text = (clip.search_text or clip.name or "").strip()
                if not text:
                    skipped += 1
                    results.append({
                        "status": "skipped",
                        "clip_id": clip.clip_id,
                        "reason": "no search_text or name",
                    })
                    continue
                try:
                    normalized = normalize_text(text)
                    prefixed = "passage: " + normalized
                    embedding = model.encode(prefixed, normalize_embeddings=True).tolist()
                    successful += 1
                    results.append({
                        "clip_id": clip.clip_id,
                        "embedding": embedding,
                        "dimensions": len(embedding),
                        "model": TEXT_MODEL_NAME,
                        "model_version": TEXT_MODEL_VERSION,
                    })
                except Exception:
                    failed += 1
                    results.append({
                        "status": "failed",
                        "clip_id": clip.clip_id,
                        "reason": "embedding generation failed",
                    })

            total = len(req.clips)
            if successful == total:
                status = "success"
            elif successful == 0:
                status = "failed"
            else:
                status = "partial"

            return {
                "status": status,
                "total": total,
                "successful": successful,
                "skipped": skipped,
                "failed": failed,
                "results": results,
            }
        except Exception as e:
            raise HTTPException(status_code=500, detail=str(e))
