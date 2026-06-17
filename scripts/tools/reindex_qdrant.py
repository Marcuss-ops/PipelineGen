#!/usr/bin/env python3
"""
Reindex Qdrant from SQLite embeddings.

Reads all media_assets that have at least one valid embedding type
(text/transcript/visual/audio) from SQLite and pushes them to the
Qdrant 'media_assets' collection via REST API.

String asset IDs are converted to deterministic UUID v5 (SHA-1 based)
so Qdrant can accept them.

Usage:
    python3 scripts/tools/reindex_qdrant.py [--batch-size 200] [--db data/media/media.db.sqlite]
"""

import argparse
import hashlib
import json
import os
import sqlite3
import sys
import time
import uuid
from typing import Any

QDRANT_COLLECTION = "media_assets"
BATCH_SIZE = 200
QDRANT_URL = "http://127.0.0.1:6333"

# UUID namespace for deterministic ID generation
UUID_NS = uuid.UUID("6ba7b810-9dad-11d1-80b4-00c04fd430c8")  # DNS namespace


def asset_id_to_uuid(asset_id: str) -> str:
    """Convert any string asset ID to a deterministic UUID v5."""
    return str(uuid.uuid5(UUID_NS, asset_id))


def connect_db(db_path: str) -> sqlite3.Connection:
    if not os.path.isfile(db_path):
        print(f"ERROR: DB not found at {db_path}")
        sys.exit(1)
    conn = sqlite3.connect(db_path)
    conn.row_factory = sqlite3.Row
    return conn


def query_assets(conn: sqlite3.Connection) -> list[sqlite3.Row]:
    """Return all assets that have at least one valid embedding."""
    sql = """
        SELECT
            id,
            COALESCE(name, '') AS name,
            COALESCE(source, '') AS source,
            COALESCE(tags, '[]') AS tags,
            COALESCE(embedding_json, '[]') AS embedding_json,
            COALESCE(visual_embedding, '[]') AS visual_embedding,
            COALESCE(transcript_embedding, '[]') AS transcript_embedding,
            COALESCE(json_extract(metadata_json, '$.audio_embedding_json'), '') AS audio_embedding_json,
            COALESCE(json_extract(metadata_json, '$.drive_link'), '') AS drive_link,
            COALESCE(json_extract(metadata_json, '$.local_path'), '') AS local_path,
            COALESCE(json_extract(metadata_json, '$.category'), '') AS category,
            COALESCE(json_extract(metadata_json, '$.style'), '') AS style,
            COALESCE(json_extract(metadata_json, '$.media_type'), '') AS media_type,
            COALESCE(CAST(json_extract(metadata_json, '$.duration_ms') AS INTEGER), 0) AS duration_ms,
            COALESCE(json_extract(metadata_json, '$.language'), '') AS language,
            COALESCE(json_extract(metadata_json, '$.search_text'), '') AS search_text,
            COALESCE(json_extract(metadata_json, '$.youtube_video_id'), '') AS youtube_video_id,
            COALESCE(json_extract(metadata_json, '$.youtube_url'), '') AS youtube_url,
            COALESCE(json_extract(metadata_json, '$.start'), '') AS start_time,
            COALESCE(json_extract(metadata_json, '$.end'), '') AS end_time
        FROM media_assets
        WHERE
            (embedding_json IS NOT NULL AND embedding_json != '' AND embedding_json != '[]' AND embedding_json != '{}')
            OR (transcript_embedding IS NOT NULL AND transcript_embedding != '' AND transcript_embedding != '[]' AND transcript_embedding != '{}')
            OR (visual_embedding IS NOT NULL AND visual_embedding != '' AND visual_embedding != '[]')
            OR (json_extract(metadata_json, '$.audio_embedding_json') IS NOT NULL
                AND json_extract(metadata_json, '$.audio_embedding_json') != ''
                AND json_extract(metadata_json, '$.audio_embedding_json') != '[]')
        ORDER BY id
    """
    rows = conn.execute(sql).fetchall()
    return rows


def parse_embedding(s: str) -> list[float]:
    """Parse a JSON embedding string, returning empty list on failure."""
    if not s or s in ("[]", "{}", "", "null"):
        return []
    try:
        val = json.loads(s)
        if isinstance(val, list) and len(val) > 0:
            return [float(x) for x in val]
        return []
    except (json.JSONDecodeError, TypeError, ValueError):
        return []


def build_vectors(row: sqlite3.Row) -> dict[str, list[float]]:
    """Build the named-vectors dict for a Qdrant point."""
    vectors: dict[str, list[float]] = {}

    text_emb = parse_embedding(row["embedding_json"])
    if text_emb:
        vectors["text"] = text_emb

    trans_emb = parse_embedding(row["transcript_embedding"])
    if trans_emb:
        vectors["transcript"] = trans_emb

    vis_emb = parse_embedding(row["visual_embedding"])
    if vis_emb:
        vectors["visual"] = vis_emb

    audio_emb = parse_embedding(row["audio_embedding_json"])
    if audio_emb:
        vectors["audio"] = audio_emb

    return vectors


def tokenize_bm25(text: str, max_vocab: int = 25000) -> dict:
    """
    Simple client-side BM25 tokenization for Qdrant sparse vectors.
    Tokenizes by splitting on non-alphanumeric, lowercasing, counting
    term frequencies, and mapping to a pseudo-vocab index via MD5 hash.
    """
    if not text:
        return {"indices": [], "values": []}

    import collections
    import re

    tokens = re.findall(r"[a-zA-Z0-9']+", text.lower())
    if not tokens:
        return {"indices": [], "values": []}

    tf = collections.Counter(tokens)

    indices: list[int] = []
    values: list[float] = []

    for term, count in tf.items():
        idx = int(hashlib.md5(term.encode()).hexdigest(), 16) % max_vocab
        # BM25-ish raw tf weight (no idf since no corpus stats)
        val = 1.0 + float(count - 1) * 0.5
        indices.append(idx)
        values.append(val)

    return {"indices": indices, "values": values}


def build_payload(row: sqlite3.Row) -> dict[str, Any]:
    """Build the Qdrant point payload from a DB row."""
    tags_str = row["tags"]
    try:
        tags = json.loads(tags_str) if tags_str and tags_str != "[]" else []
    except json.JSONDecodeError:
        tags = []

    return {
        "asset_id": row["id"],
        "name": row["name"],
        "source": row["source"],
        "tags": tags,
        "search_text": row["search_text"],
        "drive_link": row["drive_link"],
        "local_path": row["local_path"],
        "category": row["category"],
        "style": row["style"],
        "media_type": row["media_type"],
        "duration_ms": row["duration_ms"],
        "language": row["language"],
        "youtube_video_id": row["youtube_video_id"],
        "youtube_url": row["youtube_url"],
        "start_time": row["start_time"],
        "end_time": row["end_time"],
    }


def build_point(row: sqlite3.Row) -> dict[str, Any] | None:
    """Build a single Qdrant upsert point from a DB row."""
    vectors = build_vectors(row)
    if not vectors:
        return None  # No vectors → nothing to index

    point: dict[str, Any] = {
        "id": asset_id_to_uuid(row["id"]),
        "payload": build_payload(row),
        "vector": vectors,
    }

    # Sparse vector: BM25 from search_text
    search_text = row["search_text"] or ""
    sparse = tokenize_bm25(search_text, 25000)
    if sparse["indices"]:
        point["sparse_vectors"] = {
            "bm25_text": sparse
        }

    return point


def push_batch(qdrant_url: str, collection: str, batch: list[dict], batch_num: int, total_batches: int) -> int:
    """Push a batch of points to Qdrant. Returns the number of successfully pushed points."""
    import requests

    # Build Qdrant points list
    qdrant_points = []
    for p in batch:
        qp = {
            "id": p["id"],
            "vector": p["vector"],
            "payload": p["payload"],
        }
        if "sparse_vectors" in p:
            qp["sparse_vectors"] = p["sparse_vectors"]
        qdrant_points.append(qp)

    payload = {"points": qdrant_points}

    resp = requests.put(
        f"{qdrant_url}/collections/{collection}/points",
        json=payload,
        timeout=120,
    )

    if resp.status_code in (200, 201):
        result = resp.json()
        if result.get("status") == "ok":
            return len(batch)

    print(f"  ERROR batch {batch_num}/{total_batches}: HTTP {resp.status_code}", file=sys.stderr)
    try:
        err_detail = resp.json()
        print(f"  Details: {json.dumps(err_detail, indent=2)[:500]}", file=sys.stderr)
    except Exception:
        print(f"  Body: {resp.text[:300]}", file=sys.stderr)

    # If batch failed, try one-by-one
    print(f"  Retrying {len(batch)} points individually...", file=sys.stderr)
    pushed = 0
    for i, p in enumerate(batch):
        try:
            single_resp = requests.put(
                f"{qdrant_url}/collections/{collection}/points",
                json={"points": [{
                    "id": p["id"],
                    "vector": p["vector"],
                    "payload": p["payload"],
                } | ({"sparse_vectors": p["sparse_vectors"]} if "sparse_vectors" in p else {})]},
                timeout=30,
            )
            if single_resp.status_code in (200, 201):
                pushed += 1
            else:
                if pushed == 0 and i < 3:
                    print(f"    Point {p['id']}: HTTP {single_resp.status_code}", file=sys.stderr)
        except Exception as e:
            if i == 0:
                print(f"    First point error: {e}", file=sys.stderr)
    return pushed


def main() -> None:
    import requests

    parser = argparse.ArgumentParser(description="Reindex SQLite embeddings to Qdrant")
    parser.add_argument("--db", default="data/media/media.db.sqlite", help="Path to SQLite DB")
    parser.add_argument("--qdrant-url", default=QDRANT_URL, help="Qdrant REST URL")
    parser.add_argument("--batch-size", type=int, default=BATCH_SIZE, help="Batch size for upsert")
    parser.add_argument("--collection", default=QDRANT_COLLECTION, help="Qdrant collection name")
    parser.add_argument("--dry-run", action="store_true", help="Only count, don't push")
    args = parser.parse_args()
    collection = args.collection

    print(f"📦 Connecting to DB: {args.db}", flush=True)
    conn = connect_db(args.db)

    print(f"📊 Querying assets with embeddings...", flush=True)
    rows = query_assets(conn)
    total = len(rows)
    print(f"   Found {total} assets with at least one embedding", flush=True)

    if total == 0:
        print("❌ No assets to index. Exiting.", flush=True)
        return

    # Count by type
    text_count = sum(1 for r in rows if parse_embedding(r["embedding_json"]))
    trans_count = sum(1 for r in rows if parse_embedding(r["transcript_embedding"]))
    vis_count = sum(1 for r in rows if parse_embedding(r["visual_embedding"]))
    audio_count = sum(1 for r in rows if parse_embedding(r["audio_embedding_json"]))
    has_search_text = sum(1 for r in rows if r["search_text"])

    print(f"   ├─ text embeddings:       {text_count:>6}", flush=True)
    print(f"   ├─ transcript embeddings: {trans_count:>6}", flush=True)
    print(f"   ├─ visual embeddings:     {vis_count:>6}", flush=True)
    print(f"   ├─ audio embeddings:      {audio_count:>6}", flush=True)
    print(f"   └─ with search_text:      {has_search_text:>6} (→ BM25 sparse)", flush=True)
    print(f"   Batch size: {args.batch_size}", flush=True)

    if args.dry_run:
        print("\n🏁 Dry-run mode. No points pushed.", flush=True)
        conn.close()
        return

    # Verify Qdrant collection exists
    print(f"\n🔍 Verifying Qdrant collection '{collection}'...", flush=True)
    resp = requests.get(f"{args.qdrant_url}/collections/{collection}", timeout=10)
    if resp.status_code != 200:
        print(f"❌ Collection '{QDRANT_COLLECTION}' not found in Qdrant!", flush=True)
        conn.close()
        sys.exit(1)
    print(f"   ✅ Collection exists", flush=True)

    # Grab the current point count (should be 0 after fresh creation)
    col_info = resp.json()
    init_count = col_info.get("result", {}).get("segments_count", 0)
    print(f"   Current points: {col_info.get('result', {}).get('points_count', '?')}", flush=True)

    # Build all points
    print(f"\n🏗️  Building points (string IDs → UUID v5)...", flush=True)
    points: list[dict] = []
    skipped = 0
    for row in rows:
        point = build_point(row)
        if point is None:
            skipped += 1
            continue
        points.append(point)

    print(f"   Built {len(points)} points ({skipped} skipped — no vectors)", flush=True)

    if len(points) == 0:
        print("❌ No points to push.", flush=True)
        conn.close()
        return

    # Batch upsert
    print(f"\n🚀 Pushing to Qdrant (batch size={args.batch_size})...", flush=True)
    start_time = time.time()
    pushed_total = 0
    batch_idx = 0
    batch_count = (len(points) + args.batch_size - 1) // args.batch_size

    for i in range(0, len(points), args.batch_size):
        batch = points[i : i + args.batch_size]
        batch_idx += 1

        pushed = push_batch(args.qdrant_url, collection, batch, batch_idx, batch_count)
        pushed_total += pushed

        pct = (i + len(batch)) / len(points) * 100
        elapsed = time.time() - start_time
        print(f"   [{batch_idx:>3}/{batch_count}] {pushed:>3}/{len(batch):>3} pushed ({pct:.0f}%)", flush=True)

    elapsed = time.time() - start_time
    print(f"\n{'='*60}", flush=True)
    print(f"✅ Reindex complete!", flush=True)
    print(f"   Total points pushed: {pushed_total}", flush=True)
    print(f"   Elapsed:             {elapsed:.1f}s", flush=True)

    # Verify by checking collection count
    print(f"\n🔍 Verifying collection count...", flush=True)
    time.sleep(2)  # Qdrant needs a moment to update counts
    resp = requests.get(f"{args.qdrant_url}/collections/{QDRANT_COLLECTION}", timeout=10)
    if resp.status_code == 200:
        info = resp.json()
        points_count = info.get("result", {}).get("segments_count", 0)
        # More accurate: count via scroll
        scroll_resp = requests.post(
            f"{args.qdrant_url}/collections/{collection}/points/scroll",
            json={"limit": 0, "offset": None},
            timeout=10,
        )
        if scroll_resp.status_code == 200:
            scroll_data = scroll_resp.json()
            print(f"   Points in collection (from scroll): {scroll_data.get('result', {}).get('next_page_offset', 'done')}", flush=True)
        print(f"   Points count: {points_count}", flush=True)

    conn.close()
    print(f"\n🎉 Done!", flush=True)


if __name__ == "__main__":
    main()
