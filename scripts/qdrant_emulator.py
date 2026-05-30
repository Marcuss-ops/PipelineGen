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

# Prefix for media_assets IDs to avoid collision with asset_index IDs
MEDIA_ASSET_PREFIX = "ma:"

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


def extract_text_from_media_asset(row: sqlite3.Row) -> str:
    """Build a rich text representation for a media_assets row."""
    meta = {}
    try:
        meta = json.loads(row["metadata_json"] or "{}")
    except Exception:
        meta = {}

    def safe_list(val) -> list:
        if isinstance(val, list):
            return [str(x) for x in val if x]
        if isinstance(val, str) and val:
            try:
                parsed = json.loads(val)
                if isinstance(parsed, list):
                    return [str(x) for x in parsed if x]
            except Exception:
                return [val]
        return []

    tags = safe_list(json.loads(row["tags"] or "[]") if row["tags"] else [])
    parts = [
        str(row["id"]) if row["id"] else "",
        row["name"] or "",
        row["source"] or "",
        " ".join(tags),
        str(meta.get("search_text", "") or ""),
        str(meta.get("search_text_expanded", "") or ""),
        str(meta.get("semantic_description", "") or ""),
        " ".join(safe_list(meta.get("concept_tags"))),
        " ".join(safe_list(meta.get("visual_objects"))),
        " ".join(safe_list(meta.get("emotional_tone"))),
        " ".join(safe_list(meta.get("mood"))),
        " ".join(safe_list(meta.get("categories"))),
        row["download_link"] or "",
        row["drive_link"] or "",
    ]
    return " ".join(str(p) for p in parts if str(p).strip())



def _ingest_batch(name: str, batch_texts: list, batch_rows: list, id_fn, payload_fn, counter: list) -> None:
    """Encode a batch and store into the collection. Mutates counter[0]."""
    if not batch_texts:
        return
    vectors = model.encode(batch_texts, batch_size=len(batch_texts), show_progress_bar=False)
    with lock:
        for row, vec in zip(batch_rows, vectors):
            payload = payload_fn(row)
            point_id = id_fn(row)
            collections[name]["points"][point_id] = {
                "id": point_id,
                "vectors": {"text": [float(x) for x in vec.tolist()]},
                "payload": payload,
            }
            counter[0] += 1


def backfill_collection(name: str) -> None:
    global loaded
    if not DB_PATH.exists():
        return

    with lock:
        collections.setdefault(name, {"vectors": {}, "points": {}, "created": True})

    conn = sqlite3.connect(str(DB_PATH))
    conn.row_factory = sqlite3.Row
    cur = conn.cursor()
    total = [0]  # mutable counter passed to helper
    batch_size = 64

    # ── Part 1: asset_index (images, google-vids, flux) ──────────────────────
    try:
        cur.execute(
            "SELECT asset_id, asset_type, source, source_id, operation_key, "
            "group_name, subfolder, local_path, drive_link, download_link, "
            "file_hash, content_hash, status, metadata_json, created_at, updated_at "
            "FROM asset_index WHERE status = 'ready'"
        )
        rows = cur.fetchall()
        batch_texts, batch_rows = [], []
        for row in rows:
            text = extract_text(row)
            if not text.strip():
                continue
            batch_texts.append(text)
            batch_rows.append(row)
            if len(batch_texts) >= batch_size:
                _ingest_batch(
                    name, batch_texts, batch_rows,
                    id_fn=lambda r: str(r["asset_id"]),
                    payload_fn=lambda r: {**{k: r[k] for k in r.keys()}, **_safe_json(r["metadata_json"])},
                    counter=total,
                )
                batch_texts, batch_rows = [], []
                print(f"backfill asset_index: {total[0]} points loaded")
        _ingest_batch(
            name, batch_texts, batch_rows,
            id_fn=lambda r: str(r["asset_id"]),
            payload_fn=lambda r: {**{k: r[k] for k in r.keys()}, **_safe_json(r["metadata_json"])},
            counter=total,
        )
        print(f"backfill asset_index done: {total[0]} points")
    except Exception as e:
        print(f"backfill: asset_index failed: {e}")

    # ── Part 2: media_assets (artlist clips, stock, youtube) ─────────────────
    try:
        cur.execute(
            "SELECT id, source, name, tags, embedding_json, download_link, "
            "drive_link, drive_file_id, local_path, file_hash, metadata_json, "
            "media_type, status, created_at, updated_at "
            "FROM media_assets"
        )
        rows = cur.fetchall()
        batch_texts, batch_rows = [], []
        for row in rows:
            text = extract_text_from_media_asset(row)
            if not text.strip():
                continue
            # If the row already has a real embedding vector, use it directly
            emb_json = row["embedding_json"] or ""
            if emb_json and emb_json.startswith("["):
                try:
                    emb_vec = json.loads(emb_json)
                    if isinstance(emb_vec, list) and len(emb_vec) == 384:
                        # Pre-computed embedding — store directly without re-encoding
                        payload = _make_media_asset_payload(row)
                        point_id = MEDIA_ASSET_PREFIX + str(row["id"])
                        with lock:
                            collections[name]["points"][point_id] = {
                                "id": point_id,
                                "vectors": {"text": [float(x) for x in emb_vec]},
                                "payload": payload,
                            }
                            total[0] += 1
                        continue
                except Exception:
                    pass
            batch_texts.append(text)
            batch_rows.append(row)
            if len(batch_texts) >= batch_size:
                _ingest_batch(
                    name, batch_texts, batch_rows,
                    id_fn=lambda r: MEDIA_ASSET_PREFIX + str(r["id"]),
                    payload_fn=_make_media_asset_payload,
                    counter=total,
                )
                batch_texts, batch_rows = [], []
                print(f"backfill media_assets: {total[0]} points loaded")
        _ingest_batch(
            name, batch_texts, batch_rows,
            id_fn=lambda r: MEDIA_ASSET_PREFIX + str(r["id"]),
            payload_fn=_make_media_asset_payload,
            counter=total,
        )
        print(f"backfill media_assets done: {total[0]} total points")
    except Exception as e:
        print(f"backfill: media_assets failed: {e}")

    conn.close()
    loaded = True
    print(f"backfill complete: {total[0]} points in collection {name}")


def _safe_json(s: str | None) -> dict:
    try:
        return json.loads(s or "{}")
    except Exception:
        return {}


def _make_media_asset_payload(row: sqlite3.Row) -> dict:
    meta = _safe_json(row["metadata_json"])
    payload = {
        "id": str(row["id"]),
        "source": row["source"] or "",
        "name": row["name"] or "",
        "media_type": row["media_type"] or "video",
        "status": row["status"] or "",
        "local_path": row["local_path"] or "",
        "download_link": row["download_link"] or "",
        "drive_link": row["drive_link"] or "",
        "drive_file_id": row["drive_file_id"] or "",
        "file_hash": row["file_hash"] or "",
        "created_at": row["created_at"] or "",
    }
    try:
        tags = json.loads(row["tags"] or "[]")
        payload["tags"] = tags
    except Exception:
        payload["tags"] = []
    payload.update(meta)
    return payload


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
