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

class IndexRequest(BaseModel):
    db_path: str
    clip_id: str

def normalize_text(text: str) -> str:
    # Quick heuristic to detect Italian words
    italian_stopwords = {"il", "la", "i", "gli", "le", "un", "una", "di", "a", "da", "in", "con", "su", "per", "tra", "fra", "che"}
    words = text.lower().split()
    is_italian = any(w in italian_stopwords for w in words)
    target_nlp = nlp_it if (is_italian and nlp_it) else nlp
    
    doc = target_nlp(text.lower())
    return " ".join([token.lemma_ for token in doc if not token.is_stop and not token.is_punct])

def generate_search_text(tags: List[str]) -> str:
    return " ".join(tags)

def get_txt_content(local_path: Optional[str], name: Optional[str] = None) -> str:
    if not local_path and not name:
        return ""
    # 1. Try with_suffix(".txt") if local_path is specified
    if local_path:
        try:
            p = Path(local_path)
            txt_file = p.with_suffix(".txt")
            if txt_file.exists():
                with open(txt_file, "r", encoding="utf-8", errors="ignore") as f:
                    return f.read().strip()
        except Exception as e:
            print(f"Error reading with_suffix txt: {e}")
            
    # 2. Try searching in same folder as local_path or download folders for {name}.txt
    if name:
        base_name = Path(name).stem
        search_dirs = [Path("data/downloads"), Path("data/youtube-clips")]
        if local_path:
            search_dirs.insert(0, Path(local_path).parent)
            
        for s_dir in search_dirs:
            if s_dir.exists():
                txt_file = s_dir / f"{base_name}.txt"
                if txt_file.exists():
                    try:
                        with open(txt_file, "r", encoding="utf-8", errors="ignore") as f:
                            return f.read().strip()
                    except:
                        pass
                try:
                    for found_p in s_dir.rglob(f"{base_name}.txt"):
                        if found_p.exists():
                            with open(found_p, "r", encoding="utf-8", errors="ignore") as f:
                                return f.read().strip()
                except:
                    pass
    return ""

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
        # Query for all media assets with embeddings
        cursor.execute("SELECT id, embedding_json FROM media_assets WHERE embedding_json != '[]' AND embedding_json IS NOT NULL")
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
    mode: str = "text" # "text" or "visual"

@app.post("/search")
def search(req: SearchRequest):
    mode = req.mode.lower()
    # If mode is visual, we might need to extract from metadata_json if it's not a column
    # For now, assume embedding_json is a column in media_assets (it is)
    # But visual_embedding_json is NOT a column, it's in metadata_json
    if mode == "text":
        query_sql = "SELECT id, embedding_json FROM media_assets WHERE embedding_json != '[]' AND embedding_json IS NOT NULL"
    else:
        query_sql = "SELECT id, json_extract(metadata_json, '$.visual_embedding_json') FROM media_assets WHERE json_extract(metadata_json, '$.visual_embedding_json') IS NOT NULL"
    
    target_model = model if mode == "text" else clip_model
    
    # Cache key includes mode
    cache_key = f"{req.db_path}_{mode}"
    
    if cache_key in embedding_cache:
        cache = embedding_cache[cache_key]
    else:
        try:
            conn = sqlite3.connect(req.db_path)
            cursor = conn.cursor()
            cursor.execute(query_sql)
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
                return {"clips": [], "reason": "no_embeddings_found"}
                
            matrix = np.array(embeddings, dtype=np.float32)
            norms = np.linalg.norm(matrix, axis=1, keepdims=True)
            matrix = matrix / (norms + 1e-10)
            
            cache = {"ids": ids, "matrix": matrix}
            embedding_cache[cache_key] = cache
        except Exception as e:
            print(f"Error loading embeddings for {cache_key}: {e}")
            return {"clips": [], "reason": str(e)}
    
    try:
        # Embed query using the appropriate model
        if mode == "text":
            query_text = normalize_text(req.query)
            query_vec = target_model.encode(query_text).astype(np.float32)
        else:
            # For visual mode, we embed the text query using CLIP's text encoder
            # SentenceTransformer's CLIP model handles this automatically
            query_vec = target_model.encode(req.query).astype(np.float32)
            
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
            
        return {"clips": results, "mode": mode}
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

        cursor.execute("SELECT id, name, tags, json_extract(metadata_json, '$.local_path') as local_path FROM media_assets WHERE id = ?", (req.clip_id,))
        clip = cursor.fetchone()
        
        if not clip:
            conn.close()
            raise HTTPException(status_code=404, detail=f"Clip not found: {req.clip_id}")

        name = clip["name"] or ""
        local_path = clip["local_path"] or ""
        tags_str = clip["tags"] or "[]"
        try:
            tags = json.loads(tags_str)
            if not isinstance(tags, list):
                tags = []
        except:
            tags = []

        txt_desc = get_txt_content(local_path, name)
        search_parts = [name]
        if txt_desc:
            search_parts.append(txt_desc)
        search_parts.extend(tags)

        search_text = " ".join(search_parts)
        normalized = normalize_text(search_text)
        embedding_list = model.encode(normalized).tolist()
        embedding_json = json.dumps(embedding_list)

        cursor.execute(
            "UPDATE media_assets SET metadata_json = json_set(COALESCE(metadata_json,'{}'), '$.search_text', ?), embedding_json = ? WHERE id = ?",
            (search_text, embedding_json, req.clip_id)
        )
        
        # New: Visual Indexing if local_path exists
        local_path = clip["local_path"]
        if local_path and os.path.exists(local_path):
            try:
                # We need to extract a frame. We'll use a temporary path.
                frame_path = f"{local_path}_thumb.png"
                import subprocess
                subprocess.run([
                    "ffmpeg", "-y", "-ss", "1", "-i", local_path, 
                    "-frames:v", "1", "-q:v", "2", frame_path
                ], check=True, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
                
                if os.path.exists(frame_path):
                    img = Image.open(frame_path)
                    phash = str(imagehash.phash(img))
                    visual_emb = clip_model.encode(img).tolist()
                    visual_emb_json = json.dumps(visual_emb)
                    
                    cursor.execute(
                        "UPDATE media_assets SET metadata_json = json_set(json_set(COALESCE(metadata_json,'{}'), '$.phash', ?), '$.visual_embedding_json', ?) WHERE id = ?",
                        (phash, visual_emb_json, req.clip_id)
                    )
                    os.remove(frame_path)
                    print(f"Visual index updated for {req.clip_id}")
            except Exception as ve:
                print(f"Failed visual indexing in /index: {ve}")

        conn.commit()
        conn.close()

        # Invalidate cache for this DB
        db_path_str = str(db_path)
        if db_path_str in embedding_cache:
            del embedding_cache[db_path_str]

        return {"ok": True, "clip_id": req.clip_id, "search_text": search_text}
    except Exception as e:
        raise HTTPException(status_code=500, detail=str(e))

class VisualIndexRequest(BaseModel):
    db_path: str
    clip_id: str
    frame_path: str

@app.post("/index_visual")
def index_visual(req: VisualIndexRequest):
    db_path = Path(req.db_path)
    if not db_path.exists():
        raise HTTPException(status_code=404, detail=f"Database not found: {db_path}")

    frame_path = Path(req.frame_path)
    if not frame_path.exists():
        raise HTTPException(status_code=404, detail=f"Frame not found: {frame_path}")

    try:
        # Load image
        img = Image.open(frame_path)
        
        # Compute pHash
        phash = str(imagehash.phash(img))
        
        # Compute CLIP embedding
        visual_emb = clip_model.encode(img).tolist()
        visual_emb_json = json.dumps(visual_emb)

        # Update DB
        conn = sqlite3.connect(db_path)
        cursor = conn.cursor()
        cursor.execute(
            "UPDATE media_assets SET metadata_json = json_set(json_set(COALESCE(metadata_json,'{}'), '$.phash', ?), '$.visual_embedding_json', ?) WHERE id = ?",
            (phash, visual_emb_json, req.clip_id)
        )
        conn.commit()
        conn.close()

        return {
            "ok": True, 
            "clip_id": req.clip_id, 
            "phash": phash, 
            "visual_embedding_size": len(visual_emb)
        }
    except Exception as e:
        raise HTTPException(status_code=500, detail=str(e))

class BulkIndexRequest(BaseModel):
    db_path: str
    clip_ids: List[str]

class PhashRequest(BaseModel):
    image_path: str

@app.post("/phash")
def compute_phash(req: PhashRequest):
    try:
        img = Image.open(req.image_path)
        h = str(imagehash.phash(img))
        return {"phash": h}
    except Exception as e:
        raise HTTPException(status_code=500, detail=str(e))

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
            cursor.execute("SELECT id, name, tags, json_extract(metadata_json, '$.local_path') as local_path FROM media_assets WHERE id = ?", (clip_id,))
            clip = cursor.fetchone()
            if not clip:
                continue

            name = clip["name"] or ""
            local_path = clip["local_path"] or ""
            tags_str = clip["tags"] or "[]"
            try:
                tags = json.loads(tags_str)
                if not isinstance(tags, list):
                    tags = []
            except:
                tags = []

            txt_desc = get_txt_content(local_path, name)
            search_parts = [name]
            if txt_desc:
                search_parts.append(txt_desc)
            search_parts.extend(tags)

            search_text = " ".join(search_parts)
            normalized = normalize_text(search_text)
            embedding_list = model.encode(normalized).tolist()
            embedding_json = json.dumps(embedding_list)

            cursor.execute(
                "UPDATE media_assets SET metadata_json = json_set(COALESCE(metadata_json,'{}'), '$.search_text', ?), embedding_json = ? WHERE id = ?",
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
