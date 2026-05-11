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

import numpy as np

# Cache for embeddings: {db_path: {"ids": [], "matrix": np.array}}
embedding_cache = {}

def load_embeddings(db_path: str):
    db_path_str = str(db_path)
    # Simple cache invalidation if file changed? For now, just cache forever or until restart
    if db_path_str in embedding_cache:
        return embedding_cache[db_path_str]
    
    try:
        conn = sqlite3.connect(db_path_str)
        cursor = conn.cursor()
        # Query for all clips with embeddings
        cursor.execute("SELECT id, embedding_json FROM clips WHERE embedding_json != '[]' AND embedding_json IS NOT NULL")
        rows = cursor.fetchall()
        conn.close()
        
        ids = []
        embeddings = []
        for row in rows:
            try:
                emb = json.loads(row[1])
                if len(emb) > 0:
                    ids.append(row[0])
                    embeddings.append(emb)
            except:
                continue
        
        if not embeddings:
            return None
            
        matrix = np.array(embeddings, dtype=np.float32)
        # Normalize for cosine similarity
        norms = np.linalg.norm(matrix, axis=1, keepdims=True)
        matrix = matrix / (norms + 1e-10)
        
        cache_entry = {"ids": ids, "matrix": matrix}
        embedding_cache[db_path_str] = cache_entry
        return cache_entry
    except Exception as e:
        print(f"Error loading embeddings from {db_path_str}: {e}")
        return None

class SearchRequest(BaseModel):
    db_path: str
    query: str
    limit: int = 10

@app.post("/search")
def search(req: SearchRequest):
    cache = load_embeddings(req.db_path)
    if not cache:
        return {"clips": [], "reason": "no_embeddings_found"}
    
    try:
        # Embed query
        query_text = normalize_text(req.query)
        query_vec = model.encode(query_text).astype(np.float32)
        query_vec = query_vec / (np.linalg.norm(query_vec) + 1e-10)
        
        # Calculate cosine similarity
        similarities = np.dot(cache["matrix"], query_vec)
        
        # Get top N
        top_indices = np.argsort(similarities)[::-1][:req.limit]
        
        results = []
        for idx in top_indices:
            results.append({
                "clip_id": cache["ids"][idx],
                "score": float(similarities[idx])
            })
            
        return {"clips": results, "query_normalized": query_text}
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
        embedding_list = model.encode(normalized).tolist()
        embedding_json = json.dumps(embedding_list)

        cursor.execute(
            "UPDATE clips SET search_text = ?, embedding_json = ? WHERE id = ?",
            (search_text, embedding_json, req.clip_id)
        )
        conn.commit()
        conn.close()

        # Invalidate cache for this DB
        db_path_str = str(db_path)
        if db_path_str in embedding_cache:
            del embedding_cache[db_path_str]

        return {"ok": True, "clip_id": req.clip_id, "search_text": search_text}
    except Exception as e:
        raise HTTPException(status_code=500, detail=str(e))

class BulkIndexRequest(BaseModel):
    db_path: str
    clip_ids: List[str]

@app.post("/index_bulk")
def index_bulk(req: BulkIndexRequest):
    db_path = Path(req.db_path)
    if not db_path.exists():
        raise HTTPException(status_code=404, detail=f"Database not found: {db_path}")

    try:
        conn = sqlite3.connect(db_path)
        conn.row_factory = sqlite3.Row
        cursor = conn.cursor()

        indexed_count = 0
        for clip_id in req.clip_ids:
            cursor.execute("SELECT id, name, tags FROM clips WHERE id = ?", (clip_id,))
            clip = cursor.fetchone()
            if not clip:
                continue

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
            embedding_list = model.encode(normalized).tolist()
            embedding_json = json.dumps(embedding_list)

            cursor.execute(
                "UPDATE clips SET search_text = ?, embedding_json = ? WHERE id = ?",
                (search_text, embedding_json, clip_id)
            )
            indexed_count += 1

        conn.commit()
        conn.close()

        # Invalidate cache
        db_path_str = str(db_path)
        if db_path_str in embedding_cache:
            del embedding_cache[db_path_str]

        return {"ok": True, "indexed_count": indexed_count}
    except Exception as e:
        raise HTTPException(status_code=500, detail=str(e))

if __name__ == "__main__":
    parser = argparse.ArgumentParser()
    parser.add_argument("--port", type=int, default=8001)
    parser.add_argument("--host", default="127.0.0.1")
    args = parser.parse_args()

    uvicorn.run(app, host=args.host, port=args.port)
