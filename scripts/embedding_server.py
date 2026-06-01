#!/usr/bin/env python3
"""PipelineGen Embedding Server — stripped to only embed + phash endpoints.
   Qdrant handles search/index; this server is only for generating embeddings."""
import argparse
from pathlib import Path
from fastapi import FastAPI, HTTPException
from pydantic import BaseModel
import uvicorn

try:
    from sentence_transformers import SentenceTransformer, CrossEncoder
    import spacy
    import imagehash
    from PIL import Image
except ImportError as e:
    print(f"Missing dependency: {e}")
    print("Install: pip install fastapi uvicorn sentence-transformers spacy imagehash pillow")
    exit(1)

# Load models once
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
print("Loading CLIP model (clip-ViT-B-32)...")
clip_model = SentenceTransformer("clip-ViT-B-32")

# Load CLAP model for audio-text embeddings
clap_model = None
try:
    print("Loading CLAP model (laion/clap-htsat-fused)...")
    # Note: SentenceTransformer can sometimes load CLAP models if they are in the right format,
    # or we might need laion-clap. For now, we attempt to load a compatible one.
    clap_model = SentenceTransformer("laion/clap-htsat-fused")
except Exception as e:
    print(f"CLAP model not loaded: {e}")

# Load CrossEncoder Reranker model
reranker_model = None
try:
    print("Loading CrossEncoder model (BAAI/bge-reranker-base)...")
    reranker_model = CrossEncoder("BAAI/bge-reranker-base")
except Exception as e:
    print(f"Failed to load BAAI/bge-reranker-base: {e}. Falling back to cross-encoder/ms-marco-MiniLM-L-6-v2")
    try:
        reranker_model = CrossEncoder("cross-encoder/ms-marco-MiniLM-L-6-v2")
    except Exception as ex:
        print(f"Failed to load fallback CrossEncoder: {ex}")

app = FastAPI(title="PipelineGen Embedding Server")


class EmbedRequest(BaseModel):
    text: str


class PhashRequest(BaseModel):
    image_path: str


import sqlite3
import json

class IndexVisualRequest(BaseModel):
    db_path: str
    clip_id: str
    frame_path: str


class IndexAudioRequest(BaseModel):
    db_path: str
    clip_id: str
    audio_path: str


class VisualEmbedRequest(BaseModel):
    text: str  # For CLIP text-to-visual embedding

class VisualAnalyzeRequest(BaseModel):
    image_path: str


@app.post("/index_visual")
def index_visual(req: IndexVisualRequest):
    """Generate CLIP embedding from image file and update SQLite."""
    try:
        img = Image.open(req.frame_path)
        # CLIP model can encode images directly
        embedding = clip_model.encode(img).tolist()
        
        # Compute phash too
        h = str(imagehash.phash(img))

        # Update DB
        conn = sqlite3.connect(req.db_path)
        cursor = conn.cursor()
        cursor.execute(
            "UPDATE media_assets SET metadata_json = json_set(COALESCE(metadata_json,'{}'), '$.visual_embedding_json', ?), "
            "metadata_json = json_set(metadata_json, '$.phash', ?) WHERE id = ?",
            (json.dumps(embedding), h, req.clip_id)
        )
        conn.commit()
        conn.close()

        return {"status": "success", "phash": h, "dimensions": len(embedding)}
    except Exception as e:
        import traceback
        print(traceback.format_exc())
        raise HTTPException(status_code=500, detail=str(e))


@app.post("/index_audio")
def index_audio(req: IndexAudioRequest):
    """Generate CLAP embedding from audio file and update SQLite."""
    if clap_model is None:
        raise HTTPException(status_code=501, detail="CLAP model not loaded")
    try:
        # Note: CLAP needs audio loading logic. SentenceTransformer typically 
        # expects a path or a waveform for audio models.
        # This assumes the model is structured to take the path.
        embedding = clap_model.encode(req.audio_path).tolist()

        # Update DB
        conn = sqlite3.connect(req.db_path)
        cursor = conn.cursor()
        cursor.execute(
            "UPDATE media_assets SET metadata_json = json_set(COALESCE(metadata_json,'{}'), '$.audio_embedding_json', ?) WHERE id = ?",
            (json.dumps(embedding), req.clip_id)
        )
        conn.commit()
        conn.close()

        return {"status": "success", "dimensions": len(embedding)}
    except Exception as e:
        raise HTTPException(status_code=500, detail=str(e))


def normalize_text(text: str) -> str:
    # Quick heuristic to detect Italian words
    italian_stopwords = {"il", "la", "i", "gli", "le", "un", "una", "di", "a",
                         "da", "in", "con", "su", "per", "tra", "fra", "che"}
    words = text.lower().split()
    is_italian = any(w in italian_stopwords for w in words)
    target_nlp = nlp_it if (is_italian and nlp_it) else nlp

    doc = target_nlp(text.lower())
    return " ".join([token.lemma_ for token in doc if not token.is_stop and not token.is_punct])


@app.get("/health")
def health():
    return {"status": "ok"}


@app.post("/embed")
def embed(req: EmbedRequest):
    """Generate text embedding (384d, all-MiniLM-L6-v2)."""
    try:
        normalized = normalize_text(req.text)
        embedding = model.encode(normalized).tolist()
        return {"embedding": embedding, "normalized_text": normalized}
    except Exception as e:
        raise HTTPException(status_code=500, detail=str(e))


@app.post("/embed_visual")
def embed_visual(req: VisualEmbedRequest):
    """Generate CLIP visual embedding (512d) from text description."""
    try:
        embedding = clip_model.encode(req.text).tolist()
        return {"embedding": embedding, "dimensions": len(embedding)}
    except Exception as e:
        raise HTTPException(status_code=500, detail=str(e))

@app.post("/visual_analyze")
def visual_analyze(req: VisualAnalyzeRequest):
    """Generate CLIP image embedding and perceptual hash for a local image file."""
    try:
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


@app.post("/embed_audio")
def embed_audio(req: EmbedRequest):
    """Generate CLAP audio embedding (512d) from text description."""
    if clap_model is None:
        raise HTTPException(status_code=501, detail="CLAP model not loaded")
    try:
        embedding = clap_model.encode(req.text).tolist()
        return {"embedding": embedding, "dimensions": len(embedding)}
    except Exception as e:
        raise HTTPException(status_code=500, detail=str(e))


@app.post("/phash")
def compute_phash(req: PhashRequest):
    """Compute perceptual hash of an image file (used during clip indexing)."""
    try:
        img = Image.open(req.image_path)
        h = str(imagehash.phash(img))
        return {"phash": h}
    except Exception as e:
        raise HTTPException(status_code=500, detail=str(e))


class RerankCandidate(BaseModel):
    id: str
    text: str


class RerankRequest(BaseModel):
    query: str
    candidates: list[RerankCandidate]


@app.post("/rerank")
def rerank(req: RerankRequest):
    """Rerank candidates using CrossEncoder."""
    if reranker_model is None:
        raise HTTPException(status_code=501, detail="Reranker model not loaded")
    if not req.candidates:
        return {"results": []}
    try:
        # Prepare pairs for cross-encoder matching
        pairs = [[req.query, cand.text] for cand in req.candidates]
        scores = reranker_model.predict(pairs).tolist()

        # Build and sort results by score descending
        results = []
        for idx, cand in enumerate(req.candidates):
            results.append({
                "id": cand.id,
                "score": float(scores[idx])
            })
        results.sort(key=lambda x: x["score"], reverse=True)
        return {"results": results}
    except Exception as e:
        raise HTTPException(status_code=500, detail=str(e))


if __name__ == "__main__":
    parser = argparse.ArgumentParser()
    parser.add_argument("--port", type=int, default=8001)
    parser.add_argument("--host", default="127.0.0.1")
    args = parser.parse_args()

    uvicorn.run(app, host=args.host, port=args.port)
