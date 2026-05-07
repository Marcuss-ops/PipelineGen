-- 006_add_asset_embeddings.sql
-- Asset embeddings table for semantic search
-- Note: For vector search, consider using a dedicated vector database (Qdrant, Chroma, LanceDB)
-- This table stores embeddings in BLOB format for basic similarity comparison in Go/Python

CREATE TABLE IF NOT EXISTS asset_embeddings (
    asset_id TEXT PRIMARY KEY,
    embedding_model TEXT NOT NULL,
    embedding BLOB NOT NULL,
    text_source TEXT,
    dimensions INTEGER NOT NULL DEFAULT 0,
    created_at TEXT DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY (asset_id) REFERENCES asset_index(asset_id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_asset_embeddings_model
ON asset_embeddings(embedding_model);

CREATE INDEX IF NOT EXISTS idx_asset_embeddings_dimensions
ON asset_embeddings(dimensions)
WHERE dimensions > 0;

-- Note: For proper vector similarity search, consider migrating to:
-- 1. sqlite-vss (Vector Similarity Search for SQLite)
-- 2. PostgreSQL + pgvector
-- 3. Dedicated vector database (Qdrant, Chroma, LanceDB)
-- SQLite with BLOB embeddings requires manual cosine similarity calculation
