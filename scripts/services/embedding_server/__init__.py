"""PipelineGen Embedding Server — FastAPI app + model loaders.

Qdrant handles search/index; this server is only for generating embeddings
(E5 multilingual text + CLIP visual + CLAP audio + perceptual hash).

Package layout (split out from a 604-line single file):

  __init__.py  — model loaders (nlp, e5, clip, clap), FastAPI app,
                 concurrency primitives (semaphore + busy counters),
                 /health endpoint + tracking middleware.
  models.py    — Pydantic request schemas.
  text.py      — text endpoints (/embed, /index, /index_bulk,
                 /index_transcript) + normalize_text helper.
  visual.py    — visual endpoints (/embed_visual, /visual_analyze,
                 /index_visual, /index_visual_multi, /phash).
  audio.py     — audio endpoints (/embed_audio, /index_audio).
  __main__.py  — argparse + uvicorn.

CrossEncoder Rerankar is REMOVED from this server; reranking is handled
by scripts/reranker_server.py on port 8091 (separate process per
architectural separation: embedding vs reranking).
"""

import logging
import os
import time
import asyncio

from fastapi import FastAPI

log = logging.getLogger("embedding_server")

try:
    from sentence_transformers import SentenceTransformer
    import spacy
    import imagehash
    from PIL import Image
except ImportError as e:
    print(f"Missing dependency: {e}")
    print(
        "Install: pip install fastapi uvicorn sentence-transformers spacy imagehash pillow"
    )
    raise

# ── Concurrency control ──────────────────────────────────────────────────────
# Limit simultaneous model inference to avoid CPU/GPU memory exhaustion
# when catalogsync + artlist jobs hit the server concurrently.
INFERENCE_CONCURRENCY = 2
_inference_sem = asyncio.Semaphore(INFERENCE_CONCURRENCY)
_request_count = 0
_busy_count = 0
_busy_lock = asyncio.Lock()

# ── Model loaders ───────────────────────────────────────────────────────────
# Loaded once at module import (so the FastAPI app shares them across requests).

print("Loading NLP model (en_core_web_sm)...")
nlp = spacy.load("en_core_web_sm")
nlp_it = None
try:
    print("Loading Italian NLP model (it_core_news_sm)...")
    nlp_it = spacy.load("it_core_news_sm")
except Exception as e:
    print(f"Italian NLP model it_core_news_sm not loaded (using English fallback): {e}")

print("Loading SentenceTransformer model (intfloat/multilingual-e5-base)...")
model = SentenceTransformer("intfloat/multilingual-e5-base")
print("Loading SigLIP model (google/siglip-so400m-patch14-384, 768d)...")
siglip_model = SentenceTransformer("google/siglip-so400m-patch14-384")
print(f"SigLIP model loaded, embedding dimension: {siglip_model.get_sentence_embedding_dimension()}")

clap_model = None
try:
    print("Loading CLAP model (laion/clap-htsat-fused)...")
    clap_model = SentenceTransformer("laion/clap-htsat-fused")
except Exception as e:
    print(f"CLAP model not loaded: {e}")

# ── FastAPI app ─────────────────────────────────────────────────────────────
app = FastAPI(title="PipelineGen Embedding Server")


class EmbeddingQueueMiddleware:
    """Stub kept for backwards compat with the embedding queue depth tracking.
    The actual tracking logic is wired via @app.middleware below."""

    def __init__(self, app):
        self.app = app

    async def __call__(self, scope, receive, send):
        if scope["type"] != "http":
            return await self.app(scope, receive, send)
        return await self.app(scope, receive, send)


@app.middleware("http")
async def track_concurrency(request, call_next):
    global _request_count, _busy_count
    _request_count += 1
    start = time.monotonic()
    async with _busy_lock:
        _busy_count += 1
    try:
        response = await call_next(request)
        return response
    finally:
        async with _busy_lock:
            _busy_count -= 1
        elapsed = time.monotonic() - start
        if elapsed > 5.0:
            log.warning(
                "slow request %s %.1fs queue_depth=%d",
                request.url.path, elapsed, _busy_count,
            )


@app.get("/health")
async def health():
    return {
        "status": "ok",
        "queue_depth": _busy_count,
        "total_requests": _request_count,
        "inference_slots": INFERENCE_CONCURRENCY,
    }


# Mount router sub-modules. Each sub-module defines its own APIRouter and
# algorithms; we just attach them to the FastAPI app here. This pattern
# avoids `from . import app` relative-import edge cases (where `app` could
# be ambiguously a submodule or an attribute).
from . import text, visual, audio  # noqa: E402, F401
app.include_router(text.router)
app.include_router(visual.router)
app.include_router(audio.router)
