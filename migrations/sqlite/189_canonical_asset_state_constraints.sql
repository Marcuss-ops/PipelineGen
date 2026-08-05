-- Migration 189 — canonical orthogonal asset state constraints.
--
-- Authoritative state dimensions:
--   lifecycle_state: asset lifecycle and deletion truth.
--   index_state:     embedding/Qdrant projection truth.
--   media_assets_pipeline_events.fase: append-only pipeline timeline.
--
-- media_assets.asset_state is retained as a compatibility read column only.
-- It is derived from lifecycle_state + index_state by the triggers below;
-- application code must never use it as a write-side state machine.
--
-- The migration is idempotent. Legacy index values are normalized before
-- constraints are installed:
--   INDEX_PENDING -> DISCOVERED
--   INDEX_FAILED  -> INDEXING_FAILED

UPDATE media_assets
SET index_state = 'DISCOVERED'
WHERE index_state = 'INDEX_PENDING';

UPDATE media_assets
SET index_state = 'INDEXING_FAILED'
WHERE index_state = 'INDEX_FAILED';

-- Rebuild the compatibility projection for every existing row. This CASE
-- is deliberately duplicated in the triggers so SQLite enforces the same
-- projection for future writes without relying on application code.
UPDATE media_assets
SET asset_state = CASE
    WHEN lifecycle_state IN ('DELETED', 'INDEX_DELETED')
         OR index_state = 'DELETED' THEN 'FAILED_PERMANENT'
    WHEN index_state = 'EMBEDDING_FAILED'
         OR index_state = 'INDEXING_FAILED' THEN 'FAILED_RETRYABLE'
    WHEN lifecycle_state IN ('STAGING', 'PREPARING', 'PROCESSING') THEN 'DISCOVERED'
    WHEN index_state = 'NOT_INDEXABLE' THEN 'UPLOADED'
    WHEN index_state = 'DISCOVERED' THEN 'DISCOVERED'
    WHEN index_state = 'EMBEDDING' THEN 'TRANSLATED'
    WHEN index_state = 'EMBEDDED' THEN 'INDEX_PENDING'
    WHEN index_state = 'INDEXING' THEN 'INDEX_PENDING'
    WHEN index_state = 'INDEXED' AND lifecycle_state = 'ACTIVE' THEN 'READY'
    WHEN index_state = 'INDEXED' THEN 'INDEXED'
    ELSE 'DISCOVERED'
END;

CREATE INDEX IF NOT EXISTS idx_media_assets_lifecycle_state
    ON media_assets(lifecycle_state);
CREATE INDEX IF NOT EXISTS idx_media_assets_index_state
    ON media_assets(index_state);

-- SQLite has no portable ALTER TABLE ADD CHECK for an existing table. These
-- BEFORE triggers are the database-level CHECK equivalent and fail closed for
-- every SQL writer, including tools that bypass Go repositories.
CREATE TRIGGER IF NOT EXISTS trg_media_assets_state_valid_insert
BEFORE INSERT ON media_assets
WHEN NEW.lifecycle_state NOT IN (
    'PREPARING','PUBLISHED','STAGING','PROCESSING','ACTIVE',
    'DELETE_PENDING','DELETE_REQUESTED','DRIVE_DELETE_PENDING',
    'DRIVE_DELETED','INDEX_DELETE_PENDING','INDEX_DELETED','DELETED','ERROR'
)
OR NEW.index_state NOT IN (
    'NOT_INDEXABLE','DISCOVERED','EMBEDDING','EMBEDDED','INDEXING','INDEXED',
    'EMBEDDING_FAILED','INDEXING_FAILED','INDEXING_SKIPPED_NO_INDEXER',
    'DELETE_PENDING','DELETED'
)
BEGIN
    SELECT RAISE(ABORT, 'media_assets: invalid lifecycle_state or index_state');
END;

CREATE TRIGGER IF NOT EXISTS trg_media_assets_state_valid_update
BEFORE UPDATE OF lifecycle_state, index_state ON media_assets
WHEN NEW.lifecycle_state NOT IN (
    'PREPARING','PUBLISHED','STAGING','PROCESSING','ACTIVE',
    'DELETE_PENDING','DELETE_REQUESTED','DRIVE_DELETE_PENDING',
    'DRIVE_DELETED','INDEX_DELETE_PENDING','INDEX_DELETED','DELETED','ERROR'
)
OR NEW.index_state NOT IN (
    'NOT_INDEXABLE','DISCOVERED','EMBEDDING','EMBEDDED','INDEXING','INDEXED',
    'EMBEDDING_FAILED','INDEXING_FAILED','INDEXING_SKIPPED_NO_INDEXER',
    'DELETE_PENDING','DELETED'
)
BEGIN
    SELECT RAISE(ABORT, 'media_assets: invalid lifecycle_state or index_state');
END;

-- Keep the compatibility column synchronized whenever either authoritative
-- dimension changes. INSERT is also covered for callers omitting asset_state.
CREATE TRIGGER IF NOT EXISTS trg_media_assets_asset_state_projection_insert
AFTER INSERT ON media_assets
BEGIN
    UPDATE media_assets
    SET asset_state = CASE
        WHEN NEW.lifecycle_state IN ('DELETED', 'INDEX_DELETED') OR NEW.index_state = 'DELETED' THEN 'FAILED_PERMANENT'
        WHEN NEW.index_state IN ('EMBEDDING_FAILED', 'INDEXING_FAILED') THEN 'FAILED_RETRYABLE'
        WHEN NEW.lifecycle_state IN ('STAGING', 'PREPARING', 'PROCESSING') THEN 'DISCOVERED'
        WHEN NEW.index_state = 'NOT_INDEXABLE' THEN 'UPLOADED'
        WHEN NEW.index_state = 'DISCOVERED' THEN 'DISCOVERED'
        WHEN NEW.index_state = 'EMBEDDING' THEN 'TRANSLATED'
        WHEN NEW.index_state IN ('EMBEDDED', 'INDEXING') THEN 'INDEX_PENDING'
        WHEN NEW.index_state = 'INDEXED' AND NEW.lifecycle_state = 'ACTIVE' THEN 'READY'
        WHEN NEW.index_state = 'INDEXED' THEN 'INDEXED'
        ELSE 'DISCOVERED'
    END
    WHERE id = NEW.id;
END;

CREATE TRIGGER IF NOT EXISTS trg_media_assets_asset_state_projection_update
AFTER UPDATE OF lifecycle_state, index_state ON media_assets
BEGIN
    UPDATE media_assets
    SET asset_state = CASE
        WHEN NEW.lifecycle_state IN ('DELETED', 'INDEX_DELETED') OR NEW.index_state = 'DELETED' THEN 'FAILED_PERMANENT'
        WHEN NEW.index_state IN ('EMBEDDING_FAILED', 'INDEXING_FAILED') THEN 'FAILED_RETRYABLE'
        WHEN NEW.lifecycle_state IN ('STAGING', 'PREPARING', 'PROCESSING') THEN 'DISCOVERED'
        WHEN NEW.index_state = 'NOT_INDEXABLE' THEN 'UPLOADED'
        WHEN NEW.index_state = 'DISCOVERED' THEN 'DISCOVERED'
        WHEN NEW.index_state = 'EMBEDDING' THEN 'TRANSLATED'
        WHEN NEW.index_state IN ('EMBEDDED', 'INDEXING') THEN 'INDEX_PENDING'
        WHEN NEW.index_state = 'INDEXED' AND NEW.lifecycle_state = 'ACTIVE' THEN 'READY'
        WHEN NEW.index_state = 'INDEXED' THEN 'INDEXED'
        ELSE 'DISCOVERED'
    END
    WHERE id = NEW.id;
END;

-- Direct writes to the compatibility column may not create a second truth.
-- Matching writes are harmless; divergent writes fail closed.
CREATE TRIGGER IF NOT EXISTS trg_media_assets_asset_state_projection_guard
BEFORE UPDATE OF asset_state ON media_assets
WHEN NEW.asset_state != CASE
    WHEN NEW.lifecycle_state IN ('DELETED', 'INDEX_DELETED') OR NEW.index_state = 'DELETED' THEN 'FAILED_PERMANENT'
    WHEN NEW.index_state IN ('EMBEDDING_FAILED', 'INDEXING_FAILED') THEN 'FAILED_RETRYABLE'
    WHEN NEW.lifecycle_state IN ('STAGING', 'PREPARING', 'PROCESSING') THEN 'DISCOVERED'
    WHEN NEW.index_state = 'NOT_INDEXABLE' THEN 'UPLOADED'
    WHEN NEW.index_state = 'DISCOVERED' THEN 'DISCOVERED'
    WHEN NEW.index_state = 'EMBEDDING' THEN 'TRANSLATED'
    WHEN NEW.index_state IN ('EMBEDDED', 'INDEXING') THEN 'INDEX_PENDING'
    WHEN NEW.index_state = 'INDEXED' AND NEW.lifecycle_state = 'ACTIVE' THEN 'READY'
    WHEN NEW.index_state = 'INDEXED' THEN 'INDEXED'
    ELSE 'DISCOVERED'
END
BEGIN
    SELECT RAISE(ABORT, 'media_assets: asset_state is a derived projection');
END;

-- PipelineState is an append-only projection. Validate its wire alphabet at
-- the database boundary as well as in AppendPipelineEvent.
CREATE TRIGGER IF NOT EXISTS trg_pipeline_events_fase_valid_insert
BEFORE INSERT ON media_assets_pipeline_events
WHEN NEW.fase NOT IN (
    'DISCOVERED','DOWNLOAD_PENDING','DOWNLOADING','DOWNLOADED',
    'PROCESSING','PROCESSED','PUBLISHING','PUBLISHED','INDEX_PENDING',
    'INDEXED','FAILED','SKIPPED'
)
BEGIN
    SELECT RAISE(ABORT, 'media_assets_pipeline_events: invalid fase');
END;
