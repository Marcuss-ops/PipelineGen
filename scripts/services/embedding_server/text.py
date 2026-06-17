"""Text-embedding endpoints (multilingual-e5-base) + text normalization.

Uses an APIRouter so route registration is bulletproof (no `from . import app`
relative-import resolution edge cases). __init__.py mounts the router on the
FastAPI app via `app.include_router(text.router)`.
"""

import json
import sqlite3

from fastapi import APIRouter, HTTPException

from . import _inference_sem, model, nlp, nlp_it
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
            return {"embedding": embedding, "normalized_text": normalized, "type": req.type}
        except Exception as e:
            raise HTTPException(status_code=500, detail=str(e))


@router.post("/index")
async def index_text(req: IndexTextRequest):
    """Generate SEMANTIC text embedding for a clip and save to DB.

    Reads the clip's search_text (title + summary + topics + hook) from
    media_assets, generates a 768d passage embedding via multilingual-e5,
    and stores it in embedding_json. Called by Go clipindexer.IndexClip()
    via indexViaAPI() — avoids spawning a separate Python process.
    """
    async with _inference_sem:
        try:
            conn = sqlite3.connect(req.db_path)
            conn.row_factory = sqlite3.Row
            cursor = conn.cursor()

            row = cursor.execute(
                "SELECT name, json_extract(metadata_json, '$.search_text') as search_text "
                "FROM media_assets WHERE id = ?",
                (req.clip_id,),
            ).fetchone()

            if row is None:
                conn.close()
                raise HTTPException(status_code=404, detail=f"Clip {req.clip_id} not found")

            text = row["search_text"] or row["name"] or ""
            if not text.strip():
                conn.close()
                raise HTTPException(
                    status_code=400,
                    detail=f"Clip {req.clip_id} has no search_text or name",
                )

            normalized = normalize_text(text)
            prefixed = "passage: " + normalized
            embedding = model.encode(prefixed, normalize_embeddings=True).tolist()
            embedding_json = json.dumps(embedding)

            cursor.execute(
                "UPDATE media_assets SET embedding_json = ? WHERE id = ?",
                (embedding_json, req.clip_id),
            )
            conn.commit()
            conn.close()

            return {
                "status": "success",
                "clip_id": req.clip_id,
                "dimensions": len(embedding),
                "text_length": len(text),
            }
        except HTTPException:
            raise
        except Exception as e:
            import traceback
            print(traceback.format_exc())
            raise HTTPException(status_code=500, detail=str(e))


@router.post("/index_transcript")
async def index_transcript(req: IndexTextRequest):
    """Generate TRANSCRIPT embedding for a clip and save to DB.

    Reads the clip's transcript text (full Whisper transcription) from a
    .txt file associated with the clip, generates a 768d passage
    embedding, stores it in transcript_embedding. Enables dual-vector
    search: semantic (general meaning) + transcript (precise speech).
    """
    async with _inference_sem:
        try:
            from pathlib import Path

            conn = sqlite3.connect(req.db_path)
            conn.row_factory = sqlite3.Row
            cursor = conn.cursor()

            row = cursor.execute(
                "SELECT name, json_extract(metadata_json, '$.local_path') as local_path "
                "FROM media_assets WHERE id = ?",
                (req.clip_id,),
            ).fetchone()

            if row is None:
                conn.close()
                raise HTTPException(status_code=404, detail=f"Clip {req.clip_id} not found")

            clip_name = row["name"] or ""
            local_path = row["local_path"] or ""
            transcript_text = ""

            if local_path:
                txt_file = Path(local_path).with_suffix(".txt")
                if txt_file.exists() and txt_file.is_file():
                    transcript_text = txt_file.read_text(
                        encoding="utf-8", errors="ignore"
                    ).strip()

            if not transcript_text and clip_name:
                base_name = Path(clip_name).stem
                search_dirs = [
                    Path("data/media"),
                    Path("data/downloads"),
                    Path("data/youtube-clips"),
                ]
                if local_path:
                    search_dirs.insert(0, Path(local_path).parent)
                for s_dir in search_dirs:
                    if s_dir.exists():
                        for candidate in [s_dir / f"{base_name}.txt"]:
                            if candidate.exists():
                                transcript_text = candidate.read_text(
                                    encoding="utf-8", errors="ignore"
                                ).strip()
                                break
                        if transcript_text:
                            break

            if not transcript_text:
                conn.close()
                return {
                    "status": "skipped",
                    "clip_id": req.clip_id,
                    "reason": "no transcript .txt file found",
                }

            normalized = normalize_text(transcript_text)
            prefixed = "passage: " + normalized
            embedding = model.encode(prefixed, normalize_embeddings=True).tolist()
            transcript_embedding_json = json.dumps(embedding)

            cursor.execute(
                "UPDATE media_assets SET transcript_embedding = ? WHERE id = ?",
                (transcript_embedding_json, req.clip_id),
            )
            conn.commit()
            conn.close()

            return {
                "status": "success",
                "clip_id": req.clip_id,
                "dimensions": len(embedding),
                "text_length": len(transcript_text),
            }
        except HTTPException:
            raise
        except Exception as e:
            import traceback
            print(traceback.format_exc())
            raise HTTPException(status_code=500, detail=str(e))


@router.post("/index_bulk")
async def index_bulk(req: IndexBulkRequest):
    """Generate text embeddings for multiple clips and save to DB in one transaction."""
    async with _inference_sem:
        conn = None
        try:
            from pathlib import Path

            db_path = Path(req.db_path)
            if not db_path.exists():
                raise HTTPException(status_code=400, detail=f"Database file not found: {req.db_path}")

            conn = sqlite3.connect(req.db_path, timeout=10)
            conn.row_factory = sqlite3.Row
            cursor = conn.cursor()

            indexed_count = 0
            for clip_id in req.clip_ids:
                try:
                    row = cursor.execute(
                        "SELECT name, json_extract(metadata_json, '$.search_text') as search_text "
                        "FROM media_assets WHERE id = ?",
                        (clip_id,),
                    ).fetchone()

                    if row is None:
                        continue

                    text = row["search_text"] or row["name"] or ""
                    if not text.strip():
                        continue

                    normalized = normalize_text(text)
                    prefixed = "passage: " + normalized
                    embedding = model.encode(prefixed, normalize_embeddings=True).tolist()
                    embedding_json = json.dumps(embedding)

                    cursor.execute(
                        "UPDATE media_assets SET embedding_json = ? WHERE id = ?",
                        (embedding_json, clip_id),
                    )
                    indexed_count += 1
                except Exception as clip_err:
                    print(f"Warning: failed to index clip {clip_id}: {clip_err}")

            conn.commit()
            return {"status": "success", "count": indexed_count, "total": len(req.clip_ids)}
        except HTTPException:
            raise
        except Exception as e:
            if conn:
                try:
                    conn.rollback()
                except Exception:
                    pass
            import traceback
            print(traceback.format_exc())
            raise HTTPException(status_code=500, detail=str(e))
        finally:
            if conn:
                conn.close()
