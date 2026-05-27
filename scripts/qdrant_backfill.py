#!/usr/bin/env python3
"""
Qdrant backfill script.

Reads all assets from the PipelineGen media database and indexes them
into Qdrant for ANN vector search. This is a one-shot migration tool.

Usage:
    python3 scripts/qdrant_backfill.py \\
        --db data/media/media.db.sqlite \\
        --qdrant http://127.0.0.1:6333 \\
        --collection pipelinegen_assets

Options:
    --db PATH             Media database path (required)
    --qdrant URL          Qdrant HTTP endpoint (default: http://127.0.0.1:6333)
    --collection NAME     Qdrant collection name (default: pipelinegen_assets)
    --text-dim N          Text embedding dimension (default: 384)
    --visual-dim N        Visual embedding dimension (default: 512)
    --text-vector NAME    Named vector for text (default: text)
    --visual-vector NAME  Named vector for visual (default: visual)
    --limit N             Max assets to process (default: all)
    --offset N            Skip first N assets (default: 0)
    --dry-run             Print what would be done without writing
    --batch-size N        Points per batch (default: 100)
    --verbose             Print per-asset progress
"""

import argparse
import json
import os
import sqlite3
import sys
import time
from datetime import datetime
from urllib import request, error


def qdrant_request(url, method="GET", body=None, timeout=10):
    """Make an HTTP request to Qdrant REST API."""
    data = json.dumps(body).encode("utf-8") if body else None
    req = request.Request(url, data=data, method=method)
    req.add_header("Content-Type", "application/json")
    try:
        resp = request.urlopen(req, timeout=timeout)
        return json.loads(resp.read().decode("utf-8"))
    except error.HTTPError as e:
        body = e.read().decode("utf-8", errors="replace")
        print(f"Qdrant HTTP {e.code}: {body}", file=sys.stderr)
        return None
    except Exception as e:
        print(f"Qdrant request error: {e}", file=sys.stderr)
        return None


def ensure_collection(qdrant_url, collection, text_dim, visual_dim, audio_dim, text_vector, visual_vector, audio_vector):
    """Create the Qdrant collection with named vectors if it doesn't exist."""
    # Check if exists
    url = f"{qdrant_url}/collections/{collection}"
    result = qdrant_request(url)
    if result and result.get("result"):
        print(f"Collection '{collection}' already exists")
        return True

    # Create
    body = {
        "name": collection,
        "vectors": {
            text_vector: {"size": text_dim, "distance": "Cosine"},
            visual_vector: {"size": visual_dim, "distance": "Cosine"},
            audio_vector: {"size": audio_dim, "distance": "Cosine"},
        },
    }
    result = qdrant_request(url, method="PUT", body=body, timeout=30)
    if result and result.get("result"):
        print(f"Collection '{collection}' created")
        return True
    else:
        print(f"Failed to create collection '{collection}'", file=sys.stderr)
        return False


def fetch_assets(db_path, limit=None, offset=0):
    """Fetch all assets from the media database with their metadata and embeddings."""
    if not os.path.exists(db_path):
        print(f"Database not found: {db_path}", file=sys.stderr)
        sys.exit(1)

    conn = sqlite3.connect(db_path)
    conn.row_factory = sqlite3.Row
    cursor = conn.cursor()

    # Check table exists
    cursor.execute("SELECT name FROM sqlite_master WHERE type='table' AND name='media_assets'")
    if not cursor.fetchone():
        print("Table 'media_assets' not found in database", file=sys.stderr)
        conn.close()
        sys.exit(1)

    query = """
        SELECT id, name, source, tags,
               COALESCE(embedding_json, '[]') as embedding_json,
               json_extract(metadata_json, '$.visual_embedding_json') as visual_embedding_json,
               json_extract(metadata_json, '$.audio_embedding_json') as audio_embedding_json,
               json_extract(metadata_json, '$.drive_link') as drive_link,
               json_extract(metadata_json, '$.local_path') as local_path,
               json_extract(metadata_json, '$.category') as category,
               json_extract(metadata_json, '$.style') as style,
               json_extract(metadata_json, '$.media_type') as media_type,
               json_extract(metadata_json, '$.duration_ms') as duration_ms,
               json_extract(metadata_json, '$.created_at') as created_at
        FROM media_assets
        WHERE (embedding_json IS NOT NULL AND embedding_json != '[]')
           OR json_extract(metadata_json, '$.visual_embedding_json') IS NOT NULL
           OR json_extract(metadata_json, '$.audio_embedding_json') IS NOT NULL
    """
    params = []

    if limit:
        query += " LIMIT ?"
        params.append(limit)
    if offset:
        # SQLite doesn't support OFFSET without LIMIT
        if not limit:
            query += " LIMIT -1"
        query += " OFFSET ?"
        params.append(offset)

    cursor.execute(query, params)
    rows = cursor.fetchall()
    conn.close()

    print(f"Fetched {len(rows)} assets from database")
    return rows


def build_payload(row, text_vector, visual_vector, audio_vector):
    """Build a Qdrant point with named vectors and payload."""
    asset_id = row["id"]
    name = row["name"] or ""
    source = row["source"] or "unknown"

    # Parse text embedding
    text_emb = []
    try:
        text_emb = json.loads(row["embedding_json"])
        if not isinstance(text_emb, list) or len(text_emb) == 0:
            text_emb = []
    except (json.JSONDecodeError, TypeError):
        text_emb = []

    # Parse visual embedding
    visual_emb = []
    try:
        vej = row["visual_embedding_json"]
        if vej:
            visual_emb = json.loads(vej)
            if not isinstance(visual_emb, list) or len(visual_emb) == 0:
                visual_emb = []
    except (json.JSONDecodeError, TypeError):
        visual_emb = []

    # Parse audio embedding
    audio_emb = []
    try:
        aej = row["audio_embedding_json"]
        if aej:
            audio_emb = json.loads(aej)
            if not isinstance(audio_emb, list) or len(audio_emb) == 0:
                audio_emb = []
    except (json.JSONDecodeError, TypeError):
        audio_emb = []

    # Build named vectors
    vectors = {}
    if text_emb:
        vectors[text_vector] = text_emb
    if visual_emb:
        vectors[visual_vector] = visual_emb
    if audio_emb:
        vectors[audio_vector] = audio_emb

    if not vectors:
        return None  # No embeddings to index

    # Parse tags
    tags = []
    try:
        tags = json.loads(row["tags"]) if row["tags"] else []
    except (json.JSONDecodeError, TypeError):
        tags = []

    # Parse created_at
    created_at = row["created_at"] or ""
    if isinstance(created_at, str):
        try:
            # Normalize ISO format
            dt = datetime.fromisoformat(created_at)
            created_at = dt.strftime("%Y-%m-%dT%H:%M:%S")
        except (ValueError, TypeError):
            pass

    # Duration
    duration_ms = row["duration_ms"]
    if duration_ms is not None:
        try:
            duration_ms = int(duration_ms)
        except (ValueError, TypeError):
            duration_ms = 0
    else:
        duration_ms = 0

    point = {
        "id": asset_id,
        "vector": vectors,
        "payload": {
            "asset_id": asset_id,
            "source": source,
            "name": name,
            "local_path": row["local_path"] or "",
            "drive_link": row["drive_link"] or "",
            "category": row["category"] or "",
            "style": row["style"] or "",
            "media_type": row["media_type"] or "video",
            "duration_ms": duration_ms,
            "tags": tags,
            "created_at": created_at,
        },
    }
    return point


def upsert_batch(qdrant_url, collection, points, dry_run=False):
    """Upsert a batch of points to Qdrant."""
    if dry_run:
        print(f"[DRY-RUN] Would upsert {len(points)} points")
        for p in points:
            print(f"  {p['id']}: {p['payload']['name']} ({p['payload']['source']})")
        return True

    url = f"{qdrant_url}/collections/{collection}/points?wait=true"
    body = {"points": points}
    result = qdrant_request(url, method="PUT", body=body, timeout=60)
    if result:
        status = result.get("result", {})
        if isinstance(status, dict):
            ops_count = status.get("operation_id", "?")
            print(f"  Upserted {len(points)} points (op: {ops_count})")
        else:
            print(f"  Upserted {len(points)} points")
        return True
    return False


def main():
    parser = argparse.ArgumentParser(description="Qdrant backfill for PipelineGen")
    parser.add_argument("--db", required=True, help="Media database path")
    parser.add_argument("--qdrant", default="http://127.0.0.1:6333", help="Qdrant HTTP URL")
    parser.add_argument("--collection", default="pipelinegen_assets", help="Qdrant collection name")
    parser.add_argument("--text-dim", type=int, default=384)
    parser.add_argument("--visual-dim", type=int, default=512)
    parser.add_argument("--audio-dim", type=int, default=512)
    parser.add_argument("--text-vector", default="text")
    parser.add_argument("--visual-vector", default="visual")
    parser.add_argument("--audio-vector", default="audio")
    parser.add_argument("--limit", type=int, default=0, help="Max assets to process (0 = all)")
    parser.add_argument("--offset", type=int, default=0)
    parser.add_argument("--dry-run", action="store_true", help="Print without writing")
    parser.add_argument("--batch-size", type=int, default=100)
    parser.add_argument("--verbose", action="store_true")
    args = parser.parse_args()

    start = time.time()

    # 1. Ensure collection exists
    print(f"Connecting to Qdrant at {args.qdrant}...")
    if not ensure_collection(
        args.qdrant, args.collection,
        args.text_dim, args.visual_dim, args.audio_dim,
        args.text_vector, args.visual_vector, args.audio_vector,
    ):
        sys.exit(1)

    # 2. Fetch assets from DB
    limit = args.limit if args.limit > 0 else None
    rows = fetch_assets(args.db, limit=limit, offset=args.offset)

    if not rows:
        print("No assets with embeddings found. Nothing to backfill.")
        return

    # 3. Batch upsert
    total = len(rows)
    indexed = 0
    skipped = 0
    batch = []

    for i, row in enumerate(rows):
        point = build_payload(row, args.text_vector, args.visual_vector, args.audio_vector)
        if point is None:
            skipped += 1
            if args.verbose:
                print(f"  [{i+1}/{total}] {row['id']}: no embeddings, skipping")
            continue

        batch.append(point)

        if len(batch) >= args.batch_size:
            if upsert_batch(args.qdrant, args.collection, batch, args.dry_run):
                indexed += len(batch)
            batch = []

            elapsed = time.time() - start
            rate = indexed / elapsed if elapsed > 0 else 0
            print(f"  Progress: {indexed}/{total} indexed, {rate:.1f} pts/s")

    # Final batch
    if batch:
        if upsert_batch(args.qdrant, args.collection, batch, args.dry_run):
            indexed += len(batch)

    elapsed = time.time() - start
    print(f"\nDone in {elapsed:.1f}s")
    print(f"  Total assets: {total}")
    print(f"  Indexed:      {indexed}")
    print(f"  Skipped:      {skipped}")
    if args.dry_run:
        print("  (dry-run — no data written)")


if __name__ == "__main__":
    main()
