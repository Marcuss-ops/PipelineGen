#!/usr/bin/env python3
"""PipelineGen Embedding Server — stripped to only embed + phash endpoints.
   Qdrant handles search/index; this server is only for generating embeddings."""
import argparse
from pathlib import Path
from fastapi import FastAPI, HTTPException
from pydantic import BaseModel
import uvicorn

try:
    from sentence_transformers import SentenceTransformer
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

print("Loading SentenceTransformer model (all-MiniLM-L6-v2)...")
model = SentenceTransformer("all-MiniLM-L6-v2")
print("Loading CLIP model (clip-ViT-B-32)...")
clip_model = SentenceTransformer("clip-ViT-B-32")

app = FastAPI(title="PipelineGen Embedding Server")


class EmbedRequest(BaseModel):
    text: str


class PhashRequest(BaseModel):
    image_path: str


class VisualEmbedRequest(BaseModel):
    text: str  # For CLIP text-to-visual embedding


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


@app.post("/phash")
def compute_phash(req: PhashRequest):
    """Compute perceptual hash of an image file (used during clip indexing)."""
    try:
        img = Image.open(req.image_path)
        h = str(imagehash.phash(img))
        return {"phash": h}
    except Exception as e:
        raise HTTPException(status_code=500, detail=str(e))


if __name__ == "__main__":
    parser = argparse.ArgumentParser()
    parser.add_argument("--port", type=int, default=8001)
    parser.add_argument("--host", default="127.0.0.1")
    args = parser.parse_args()

    uvicorn.run(app, host=args.host, port=args.port)
