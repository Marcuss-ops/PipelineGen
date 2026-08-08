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

from scripts.services.device_policy import (
    assert_model_device,
    embedding_health_payload,
    env_flag,
    resolve_device,
)

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

EMBEDDING_DEVICE = os.environ.get("PIPELINEGEN_EMBEDDING_DEVICE", "auto")
EMBEDDING_REQUIRE_GPU = env_flag("PIPELINEGEN_EMBEDDING_REQUIRE_GPU")
DEVICE_SELECTION = resolve_device(
    EMBEDDING_DEVICE,
    require_gpu=EMBEDDING_REQUIRE_GPU,
)

print(
    f"Embedding device policy: requested={DEVICE_SELECTION.requested} "
    f"effective={DEVICE_SELECTION.effective} "
    f"cuda_available={DEVICE_SELECTION.cuda_available} "
    f"gpu_required={DEVICE_SELECTION.require_gpu}"
)

print("Loading NLP model (en_core_web_sm)...")
nlp = spacy.load("en_core_web_sm")
nlp_it = None
try:
    print("Loading Italian NLP model (it_core_news_sm)...")
    nlp_it = spacy.load("it_core_news_sm")
except Exception as e:
    print(f"Italian NLP model it_core_news_sm not loaded (using English fallback): {e}")

print("Loading SentenceTransformer model (intfloat/multilingual-e5-base)...")
model = SentenceTransformer(
    "intfloat/multilingual-e5-base", device=DEVICE_SELECTION.effective
)
TEXT_MODEL_DEVICE = assert_model_device(model, DEVICE_SELECTION, "text embedding")
TEXT_MODEL_NAME = "intfloat/multilingual-e5-base"
TEXT_MODEL_VERSION = "2026-06-26-v1"
siglip_model = None
VISUAL_MODEL_NAME = "google/siglip-so400m-patch14-384"
VISUAL_MODEL_VERSION = "2026-06-26-v1"
if os.environ.get("SKIP_SIGLIP", "").lower() in ("1", "true", "yes"):
    if DEVICE_SELECTION.requested == "cuda" or DEVICE_SELECTION.require_gpu:
        raise RuntimeError("SigLIP cannot be skipped while GPU mode is explicit or required")
    print("SKIP_SIGLIP set — skipping SigLIP model load (visual endpoints will return 501)")
else:
    try:
        print("Loading SigLIP model (google/siglip-so400m-patch14-384, 768d)...")
        siglip_model = SentenceTransformer(
            "google/siglip-so400m-patch14-384", device=DEVICE_SELECTION.effective
        )
        VISUAL_MODEL_DEVICE = assert_model_device(siglip_model, DEVICE_SELECTION, "visual embedding")
        print(f"SigLIP model loaded, embedding dimension: {siglip_model.get_sentence_embedding_dimension()}")
    except Exception as e:
        if DEVICE_SELECTION.effective == "cuda" or DEVICE_SELECTION.require_gpu:
            raise RuntimeError(f"SigLIP GPU model load failed: {e}") from e
        print(f"SigLIP model not loaded (visual endpoints will return 501): {e}")

clap_model = None
CLAP_MODEL_NAME = "laion/clap-htsat-fused"
CLAP_MODEL_VERSION = "2026-06-26-v1"
if os.environ.get("SKIP_CLAP", "").lower() in ("1", "true", "yes"):
    if DEVICE_SELECTION.requested == "cuda" or DEVICE_SELECTION.require_gpu:
        raise RuntimeError("CLAP cannot be skipped while GPU mode is explicit or required")
    print("SKIP_CLAP set — skipping CLAP model load (audio endpoints will return 501)")
else:
    try:
        print("Loading CLAP model (laion/clap-htsat-fused)...")
        clap_model = SentenceTransformer(
            "laion/clap-htsat-fused", device=DEVICE_SELECTION.effective
        )
        AUDIO_MODEL_DEVICE = assert_model_device(clap_model, DEVICE_SELECTION, "audio embedding")
    except Exception as e:
        if DEVICE_SELECTION.effective == "cuda" or DEVICE_SELECTION.require_gpu:
            raise RuntimeError(f"CLAP GPU model load failed: {e}") from e
        print(f"CLAP model not loaded: {e}")

# ── FastAPI app ─────────────────────────────────────────────────────────────
if siglip_model is None:
    VISUAL_MODEL_DEVICE = None
if clap_model is None:
    AUDIO_MODEL_DEVICE = None

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
    return embedding_health_payload(
        queue_depth=_busy_count,
        total_requests=_request_count,
        inference_slots=INFERENCE_CONCURRENCY,
        text_device=TEXT_MODEL_DEVICE,
        visual_device=VISUAL_MODEL_DEVICE,
        audio_device=AUDIO_MODEL_DEVICE,
        selection=DEVICE_SELECTION,
    )


# Mount router sub-modules. Each sub-module defines its own APIRouter and
# algorithms; we just attach them to the FastAPI app here. This pattern
# avoids `from . import app` relative-import edge cases (where `app` could
# be ambiguously a submodule or an attribute).
from . import text, visual, audio  # noqa: E402, F401
app.include_router(text.router)
app.include_router(visual.router)
app.include_router(audio.router)
