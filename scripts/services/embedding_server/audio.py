"""Audio (CLAP) endpoints — disabled when the CLAP model failed to load.

Uses APIRouter; __init__.py mounts this via `app.include_router(audio.router)`.
"""

import json
import sqlite3

from fastapi import APIRouter, HTTPException

from . import _inference_sem, clap_model
from .models import AudioFileEmbedRequest, EmbedRequest, IndexAudioRequest

router = APIRouter()


@router.post("/embed_audio")
async def embed_audio(req: EmbedRequest):
    """Generate CLAP audio embedding (512d) from text description.

    Returns 501 if the CLAP model didn't load — endpoints stay registered
    so the Go side can detect that and decide to fall back to text-only
    retrieval. Crashes during load (network, OOM) aren't recoverable at
    request time, so we surface them as 501 rather than 500.
    """
    if clap_model is None:
        raise HTTPException(status_code=501, detail="CLAP model not loaded")
    async with _inference_sem:
        try:
            embedding = clap_model.encode(req.text).tolist()
            return {"embedding": embedding, "dimensions": len(embedding)}
        except Exception as e:
            raise HTTPException(status_code=500, detail=str(e))


@router.post("/embed_audio_from_file")
async def embed_audio_from_file(req: AudioFileEmbedRequest):
    """Generate CLAP audio embedding (512d) from an audio file.

    Accepts an audio file path, encodes it with CLAP's audio encoder,
    and returns the embedding. Returns 501 if CLAP is not loaded.
    """
    if clap_model is None:
        raise HTTPException(status_code=501, detail="CLAP model not loaded")
    async with _inference_sem:
        try:
            embedding = clap_model.encode(req.audio_path).tolist()
            return {"embedding": embedding, "dimensions": len(embedding)}
        except Exception as e:
            raise HTTPException(status_code=500, detail=str(e))


@router.post("/index_audio")
async def index_audio(req: IndexAudioRequest):
    """Generate CLAP embedding from audio file and update SQLite."""
    if clap_model is None:
        raise HTTPException(status_code=501, detail="CLAP model not loaded")
    async with _inference_sem:
        try:
            embedding = clap_model.encode(req.audio_path).tolist()

            conn = sqlite3.connect(req.db_path)
            cursor = conn.cursor()
            cursor.execute(
                "UPDATE media_assets SET metadata_json = json_set(COALESCE(metadata_json,'{}'), '$.audio_embedding_json', ?) WHERE id = ?",
                (json.dumps(embedding), req.clip_id),
            )
            conn.commit()
            conn.close()

            return {"status": "success", "dimensions": len(embedding)}
        except Exception as e:
            raise HTTPException(status_code=500, detail=str(e))
