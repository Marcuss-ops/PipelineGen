#!/usr/bin/env python3
"""
PipelineGen Reranker Server — standalone CrossEncoder reranking service.
Uses BAAI/bge-reranker-v2-m3 for multilingual semantic precision.
Runs on port 8091, separate from the embedding server.

Usage:
    pip install fastapi uvicorn sentence-transformers torch
    RERANKER_MODEL=BAAI/bge-reranker-v2-m3 python scripts/reranker_server.py
"""

import os
from fastapi import FastAPI, HTTPException

try:
    from scripts.services.device_policy import (
        assert_model_device,
        env_flag,
        reranker_health_payload,
        resolve_device,
    )
    from scripts.services.model_registry_generated import RERANKER_MODEL_NAME
except ModuleNotFoundError:  # direct `python scripts/reranker_server.py` execution
    from device_policy import (  # type: ignore[no-redef]
        assert_model_device,
        env_flag,
        reranker_health_payload,
        resolve_device,
    )
    from model_registry_generated import RERANKER_MODEL_NAME  # type: ignore[no-redef]
from pydantic import BaseModel
import uvicorn

try:
    from sentence_transformers import CrossEncoder
except ImportError:
    print("Missing dependency: sentence-transformers")
    print("Install: pip install fastapi uvicorn sentence-transformers torch")
    exit(1)

configured_model = os.getenv("RERANKER_MODEL", "").strip()
if configured_model and configured_model != RERANKER_MODEL_NAME:
    raise RuntimeError(
        f"RERANKER_MODEL={configured_model!r} differs from canonical registry model "
        f"{RERANKER_MODEL_NAME!r}"
    )
MODEL_NAME = RERANKER_MODEL_NAME
RERANKER_DEVICE = os.getenv("PIPELINEGEN_RERANKER_DEVICE", "auto")
RERANKER_REQUIRE_GPU = env_flag("PIPELINEGEN_RERANKER_REQUIRE_GPU")
DEVICE_SELECTION = resolve_device(
    RERANKER_DEVICE,
    require_gpu=RERANKER_REQUIRE_GPU,
)

print(
    f"Reranker device policy: requested={DEVICE_SELECTION.requested} "
    f"effective={DEVICE_SELECTION.effective} "
    f"cuda_available={DEVICE_SELECTION.cuda_available} "
    f"gpu_required={DEVICE_SELECTION.require_gpu}"
)
print(f"Loading CrossEncoder model: {MODEL_NAME} ...")
model = CrossEncoder(MODEL_NAME, device=DEVICE_SELECTION.effective)
MODEL_DEVICE = assert_model_device(model, DEVICE_SELECTION, "reranker")
print(f"CrossEncoder {MODEL_NAME} loaded successfully on {MODEL_DEVICE}.")

app = FastAPI(title="PipelineGen Reranker Server")


class Candidate(BaseModel):
    id: str
    text: str
    qdrant_score: float | None = None


class RerankRequest(BaseModel):
    query: str
    candidates: list[Candidate]


class RerankResult(BaseModel):
    id: str
    rerank_score: float
    qdrant_score: float | None = None


class RerankResponse(BaseModel):
    results: list[RerankResult]


@app.get("/health")
def health():
    """Health check endpoint for circuit breaker logic in Go."""
    return reranker_health_payload(
        model_name=MODEL_NAME,
        model_device_name=MODEL_DEVICE,
        selection=DEVICE_SELECTION,
    )


@app.post("/rerank")
def rerank(req: RerankRequest) -> RerankResponse:
    """
    Rerank candidates using CrossEncoder.
    Returns candidates reordered by semantic relevance to the query.
    
    Multi-media ready: works for any candidate type (clips, stock, artlist,
    images, voiceovers, AI-generated video) as long as the text field contains
    a rich description.
    """
    if not req.candidates:
        return RerankResponse(results=[])

    # Build query-candidate pairs for CrossEncoder
    pairs = [(req.query, c.text) for c in req.candidates]

    try:
        scores = model.predict(
            pairs,
            batch_size=16,
            convert_to_numpy=True,
            show_progress_bar=False,
        )
    except Exception as e:
        raise HTTPException(status_code=500, detail=f"CrossEncoder prediction failed: {str(e)}")

    # Build results preserving original qdrant_score
    results = []
    for candidate, score in zip(req.candidates, scores):
        results.append(RerankResult(
            id=candidate.id,
            rerank_score=float(score),
            qdrant_score=candidate.qdrant_score,
        ))

    # Sort by rerank_score descending
    results.sort(key=lambda x: x.rerank_score, reverse=True)

    return RerankResponse(results=results)


if __name__ == "__main__":
    import argparse

    parser = argparse.ArgumentParser(description="PipelineGen Reranker Server")
    parser.add_argument("--port", type=int, default=8091)
    parser.add_argument("--host", default="127.0.0.1")
    args = parser.parse_args()

    print(f"Starting reranker server on {args.host}:{args.port}")
    uvicorn.run(app, host=args.host, port=args.port)
