#!/usr/bin/env python3
import os
import json
import sqlite3
import argparse
from pathlib import Path
from typing import List, Optional
from fastapi import FastAPI, HTTPException
from pydantic import BaseModel
import uvicorn

try:
    from sentence_transformers import SentenceTransformer
    import spacy
except ImportError as e:
    print(f"Missing dependency: {e}")
    print("Install: pip install fastapi uvicorn sentence-transformers spacy")
    exit(1)

# Load models once
print("Loading NLP model (en_core_web_sm)...")
nlp = spacy.load("en_core_web_sm")
print("Loading SentenceTransformer model (all-MiniLM-L6-v2)...")
model = SentenceTransformer("all-MiniLM-L6-v2")

app = FastAPI(title="PipelineGen Embedding Server")

class EmbedRequest(BaseModel):
    text: str

class IndexRequest(BaseModel):
    db_path: str
    clip_id: str

def normalize_text(text: str) -> str:
    doc = nlp(text.lower())
    return " ".join([token.lemma_ for token in doc if not token.is_stop and not token.is_punct])

def generate_search_text(tags: List[str]) -> str:
    return " ".join(tags)

@app.get("/health")
def health():
    return {"status": "ok"}

@app.post("/embed")
def embed(req: EmbedRequest):
    try:
        normalized = normalize_text(req.text)
        embedding = model.encode(normalized).tolist()
        return {"embedding": embedding, "normalized_text": normalized}
    except Exception as e:
        raise HTTPException(status_code=500, detail=str(e))

@app.post("/index")
def index_clip(req: IndexRequest):
    db_path = Path(req.db_path)
    if not db_path.exists():
        raise HTTPException(status_code=404, detail=f"Database not found: {db_path}")

    try:
        conn = sqlite3.connect(db_path)
        conn.row_factory = sqlite3.Row
        cursor = conn.cursor()

        cursor.execute("SELECT id, name, tags FROM clips WHERE id = ?", (req.clip_id,))
        clip = cursor.fetchone()
        
        if not clip:
            conn.close()
            raise HTTPException(status_code=404, detail=f"Clip not found: {req.clip_id}")

        name = clip["name"] or ""
        tags_str = clip["tags"] or "[]"
        try:
            tags = json.loads(tags_str)
            if not isinstance(tags, list):
                tags = []
        except:
            tags = []

        search_text = " ".join([name] + tags)
        normalized = normalize_text(search_text)
        embedding = json.dumps(model.encode(normalized).tolist())

        cursor.execute(
            "UPDATE clips SET search_text = ?, embedding_json = ? WHERE id = ?",
            (search_text, embedding, req.clip_id)
        )
        conn.commit()
        conn.close()

        return {"ok": True, "clip_id": req.clip_id, "search_text": search_text}
    except Exception as e:
        raise HTTPException(status_code=500, detail=str(e))

if __name__ == "__main__":
    parser = argparse.ArgumentParser()
    parser.add_argument("--port", type=int, default=8001)
    parser.add_argument("--host", default="127.0.0.1")
    args = parser.parse_args()

    uvicorn.run(app, host=args.host, port=args.port)
