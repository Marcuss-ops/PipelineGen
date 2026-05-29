#!/usr/bin/env python3
from __future__ import annotations

import json
import math
import os
import sqlite3
import threading
from pathlib import Path
from typing import Any

import numpy as np
from fastapi import FastAPI, HTTPException
from pydantic import BaseModel
import uvicorn
from sentence_transformers import SentenceTransformer

DB_PATH = Path("/home/pierone/src/go-master/projects/Pyt/VeloxEditing/refactored/data/velox/velox.db.sqlite")
DEFAULT_COLLECTION = "pipelinegen_assets"
TEXT_MODEL = "all-MiniLM-L6-v2"

app = FastAPI(title="Qdrant Emulator")
model = SentenceTransformer(TEXT_MODEL)
lock = threading.RLock()
collections: dict[str, dict[str, Any]] = {}
loaded = False


class PointSearchVector(BaseModel):
    name: str | None = None
    vector: list[float]


class SearchFilterMatch(BaseModel):
    value: Any | None = None


class SearchFilterCondition(BaseModel):
    key: str
    match: SearchFilterMatch | None = None


class SearchFilter(BaseModel):
    must: list[SearchFilterCondition] = []


class SearchRequest(BaseModel):
    vector: PointSearchVector
    limit: int = 10
    with_payload: bool = True
    score_threshold: float | None = None
    filter: SearchFilter | None = None


class UpsertPoint(BaseModel):
    id: str
    vector: dict[str, list[float]] | list[float] | None = None
    payload: dict[str, Any] = {}


class UpsertRequest(BaseModel):
    points: list[UpsertPoint]


class DeleteRequest(BaseModel):
    points: list[str]


class CreateCollectionRequest(BaseModel):
    name: str | None = None
    vectors: dict[str, Any] | None = None


def cosine_similarity(a: list[float], b: list[float]) -> float:
    if not a or not b:
        return 0.0
    va = np.asarray(a, dtype=np.float32)
    vb = np.asarray(b, dtype=np.float32)
    denom = float(np.linalg.norm(va) * np.linalg.norm(vb))
    if denom == 0:
        return 0.0
    return float(np.dot(va, vb) / denom)


def match_filter(payload: dict[str, Any], flt: SearchFilter | None) -> bool:
    if not flt or not flt.must:
        return True
    for cond in flt.must:
        expected = None if cond.match is None else cond.match.value
        actual = payload.get(cond.key)
        if expected is None:
            continue
        if isinstance(actual, list):
            if expected not in actual:
                return False
        else:
            if actual != expected:
                return False
    return True


def extract_text(row: sqlite3.Row) -> str:
    meta = {}
    try:
        meta = json.loads(row["metadata_json"] or "{}")
    except Exception:
        meta = {}
    parts = [
        row["asset_id"] or "",
        row["asset_type"] or "",
        row["source"] or "",
        row["group_name"] or "",
        row["subfolder"] or "",
        row["local_path"] or "",
        row["download_link"] or "",
        row["drive_link"] or "",
        meta.get("search_text", ""),
        meta.get("semantic_description", ""),
        meta.get("prompt_original", ""),
        meta.get("filename", ""),
        meta.get("folder_path", ""),
        meta.get("category", ""),
    ]
    return " ".join(str(p) for p in parts if str(p).strip())


def backfill_collection(name: str) -> None:
    global loaded
    if not DB_PATH.exists():
        return
    conn = sqlite3.connect(str(DB_PATH))
    conn.row_factory = sqlite3.Row
    cur = conn.cursor()
    try:
        cur.execute("SELECT asset_id, asset_type, source, source_id, operation_key, group_name, subfolder, local_path, drive_link, download_link, file_hash, content_hash, status, metadata_json, created_at, updated_at FROM asset_index WHERE status = ready")
        rows = cur.fetchall()
    except Exception as e:
        print(f"backfill: failed to read asset_index: {e}")
        conn.close()
        return

    batch_texts = []
    batch_rows = []
    batch_size = 64
    total = 0
    with lock:
        collections.setdefault(name, {"vectors": {}, "points": {}, "created": True})
    for row in rows:
        text = extract_text(row)
        if not text.strip():
            continue
        batch_texts.append(text)
        batch_rows.append(row)
        if len(batch_texts) >= batch_size:
            vectors = model.encode(batch_texts, batch_size=len(batch_texts), show_progress_bar=False)
            with lock:
                for r, vec in zip(batch_rows, vectors):
                    payload = {k: r[k] for k in r.keys()}
                    try:
                        payload.update(json.loads(r["metadata_json"] or "{}"))
                    except Exception:
                        pass
                    point_id = str(r["asset_id"])
                    collections[name]["points"][point_id] = {
                        "id": point_id,
                        "vectors": {"text": [float(x) for x in vec.tolist()]},
                        "payload": payload,
                    }
                    total += 1
            batch_texts = []
            batch_rows = []
            print(f"backfill: loaded {total} points")
    if batch_texts:
        vectors = model.encode(batch_texts, batch_size=len(batch_texts), show_progress_bar=False)
        with lock:
            for r, vec in zip(batch_rows, vectors):
                payload = {k: r[k] for k in r.keys()}
                try:
                    payload.update(json.loads(r["metadata_json"] or "{}"))
                except Exception:
                    pass
                point_id = str(r["asset_id"])
                collections[name]["points"][point_id] = {
                    "id": point_id,
                    "vectors": {"text": [float(x) for x in vec.tolist()]},
                    "payload": payload,
                }
                total += 1
    conn.close()
    loaded = True
    print(f"backfill complete: {total} points in collection {name}")


@app.on_event("startup")
def startup() -> None:
    threading.Thread(target=backfill_collection, args=(DEFAULT_COLLECTION,), daemon=True).start()


@app.get("/health")
def health():
    return {"status": "ok"}


@app.get("/collections/{collection}")
def get_collection(collection: str):
    with lock:
        if collection not in collections:
            raise HTTPException(status_code=404, detail="collection not found")
        return {"result": {"status": "green", "vectors": list(collections[collection]["vectors"].keys())}}


@app.put("/collections/{collection}")
def create_collection(collection: str, req: CreateCollectionRequest):
    with lock:
        collections.setdefault(collection, {"vectors": {}, "points": {}, "created": True})
        if req.vectors:
            collections[collection]["vectors"] = req.vectors
    return {"result": True}


@app.put("/collections/{collection}/points")
def upsert_points(collection: str, req: UpsertRequest):
    with lock:
        coll = collections.setdefault(collection, {"vectors": {}, "points": {}, "created": True})
        for point in req.points:
            vectors: dict[str, list[float]] = {}
            if isinstance(point.vector, dict):
                vectors = {k: [float(x) for x in v] for k, v in point.vector.items()}
            elif isinstance(point.vector, list):
                vectors = {"text": [float(x) for x in point.vector]}
            payload = point.payload or {}
            coll["points"][str(point.id)] = {"id": str(point.id), "vectors": vectors, "payload": payload}
    return {"result": {"operation_id": 1, "status": "completed"}}


@app.post("/collections/{collection}/points/search")
def search_points(collection: str, req: SearchRequest):
    with lock:
        coll = collections.get(collection)
        if not coll:
            raise HTTPException(status_code=404, detail="collection not found")
        points = list(coll["points"].values())

    vector_name = req.vector.name or "text"
    qvec = req.vector.vector
    results = []
    for point in points:
        payload = point.get("payload", {})
        if not match_filter(payload, req.filter):
            continue
        vectors = point.get("vectors", {})
        pvec = vectors.get(vector_name)
        if pvec is None and vector_name != "text":
            pvec = vectors.get("text")
        if pvec is None:
            continue
        score = cosine_similarity(qvec, pvec)
        threshold = req.score_threshold if req.score_threshold is not None else 0.0
        if score < threshold:
            continue
        results.append({"id": point["id"], "score": score, "payload": payload})

    results.sort(key=lambda x: x["score"], reverse=True)
    results = results[: max(req.limit, 1)]
    return {"result": results}


@app.post("/collections/{collection}/points/delete")
def delete_points(collection: str, req: DeleteRequest):
    with lock:
        coll = collections.get(collection)
        if not coll:
            return {"result": {"status": "completed"}}
        for pid in req.points:
            coll["points"].pop(str(pid), None)
    return {"result": {"status": "completed"}}


if __name__ == "__main__":
    uvicorn.run(app, host="127.0.0.1", port=6333)
