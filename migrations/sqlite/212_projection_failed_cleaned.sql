-- database: primary
-- Migration 212: add FAILED_CLEANED to the projection_registry status
-- lifecycle. A FAILED projection whose physical collection has been
-- verified cleaned up is transitioned to FAILED_CLEANED (terminal) so the
-- attempt history is preserved without leaving the record indistinguishable
-- from an un-cleaned FAILED partial.
--
-- SQLite cannot ALTER a CHECK constraint, so the table is rebuilt with the
-- expanded status set and the same column order + indexes as the live
-- schema. The rebuild is non-destructive: rows are copied verbatim and only
-- the CHECK is widened.

CREATE TABLE projection_registry_new (
    projection_id TEXT PRIMARY KEY,
    projection_type TEXT NOT NULL,
    collection_name TEXT NOT NULL,
    alias_name TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL CHECK (status IN ('BUILDING','VALIDATED','ACTIVE','RETIRED','FAILED','FAILED_CLEANED')),
    source_registry_seq INTEGER NOT NULL DEFAULT 0,
    embedding_model TEXT NOT NULL DEFAULT '',
    embedding_dimensions INTEGER NOT NULL DEFAULT 0,
    asset_count INTEGER NOT NULL DEFAULT 0,
    transcript_count INTEGER NOT NULL DEFAULT 0,
    collection_hash TEXT NOT NULL DEFAULT '',
    qdrant_version TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL,
    activated_at TEXT
);

INSERT INTO projection_registry_new
    (projection_id, projection_type, collection_name, alias_name, status,
     source_registry_seq, embedding_model, embedding_dimensions, asset_count,
     transcript_count, collection_hash, qdrant_version, created_at, activated_at)
SELECT
    projection_id, projection_type, collection_name, alias_name, status,
    source_registry_seq, embedding_model, embedding_dimensions, asset_count,
    transcript_count, collection_hash, qdrant_version, created_at, activated_at
FROM projection_registry;

DROP TABLE projection_registry;
ALTER TABLE projection_registry_new RENAME TO projection_registry;

CREATE INDEX IF NOT EXISTS idx_projection_registry_type_status
    ON projection_registry(projection_type, status);
CREATE INDEX IF NOT EXISTS idx_projection_registry_status
    ON projection_registry(status, source_registry_seq);
