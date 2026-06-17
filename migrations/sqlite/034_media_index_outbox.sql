-- =========================================================================
-- Migration: 034_media_index_outbox.sql
-- Purpose:  Create the media_index_outbox table for distributed idempotent indexing.
--
-- Outbox pattern:
--   SQLite transaction
--   ├── UPSERT media_assets
--   └── INSERT media_index_outbox
--   COMMIT
--                ↓
--   Job worker (any of N parallel)
--                ↓
--   embedding + Qdrant upsert
--                ↓
--   UPDATE media_index_outbox SET status = 'processed'
--
-- The composite (asset_id, content_hash, embedding_version) makes the outbox
-- row idempotent: two workers receiving the same payload will not duplicate
-- work; only one wins via INSERT OR IGNORE on (asset_id, content_hash).
-- =========================================================================

CREATE TABLE IF NOT EXISTS media_index_outbox (
    id                INTEGER PRIMARY KEY AUTOINCREMENT,
    asset_id          TEXT    NOT NULL,
    content_hash      TEXT    NOT NULL,
    embedding_model   TEXT    NOT NULL,
    embedding_version TEXT    NOT NULL,
    collection_version TEXT   NOT NULL DEFAULT 'v1',
    status            TEXT    NOT NULL DEFAULT 'pending'
                       CHECK (status IN ('pending', 'in_flight', 'processed', 'failed', 'dead_letter')),
    payload_json      TEXT    NOT NULL,  -- denormalized for worker dispatch
    attempt_count     INTEGER NOT NULL DEFAULT 0,
    last_error        TEXT    NOT NULL DEFAULT '',
    next_attempt_at   TEXT    NOT NULL DEFAULT (datetime('now')),
    created_at        TEXT    NOT NULL DEFAULT (datetime('now')),
    updated_at        TEXT    NOT NULL DEFAULT (datetime('now')),

    -- Idempotency unique constraint: if a worker has already claimed this exact
    -- (asset, content, model, version, collection) tuple, reject the second insert.
    -- Two workers dispatching the same work won't get duplicated outbox rows.
    UNIQUE (asset_id, content_hash, embedding_model, embedding_version, collection_version)
);

CREATE INDEX IF NOT EXISTS idx_outbox_pending
    ON media_index_outbox (status, next_attempt_at)
    WHERE status = 'pending';

CREATE INDEX IF NOT EXISTS idx_outbox_inflight
    ON media_index_outbox (status, updated_at)
    WHERE status = 'in_flight';

CREATE INDEX IF NOT EXISTS idx_outbox_asset
    ON media_index_outbox (asset_id, status);

-- Trigger: keep updated_at fresh.
-- NOTE: SQLite does NOT accept IF NOT EXISTS on CREATE TRIGGER (it raises
-- "incomplete input" — only tables/views/indexes support the idempotency
-- clause). Replay-safety (in case migration 034 is re-applied after a
-- partial interruption) is achieved by explicitly dropping the trigger
-- first. The migration-runner still records 034 in schema_migrations so
-- no second run occurs under normal operation.
DROP TRIGGER IF EXISTS trg_outbox_updated_at;
CREATE TRIGGER trg_outbox_updated_at
    AFTER UPDATE ON media_index_outbox
    FOR EACH ROW
BEGIN
    UPDATE media_index_outbox
       SET updated_at = datetime('now')
     WHERE id = OLD.id;
END;

-- View: pending work visible to operators
CREATE VIEW IF NOT EXISTS media_index_outbox_pending AS
SELECT
    asset_id,
    content_hash,
    embedding_model,
    embedding_version,
    collection_version,
    attempt_count,
    last_error,
    next_attempt_at,
    (julianday('now') - julianday(created_at)) * 86400 AS age_seconds
  FROM media_index_outbox
 WHERE status IN ('pending', 'in_flight', 'failed')
 ORDER BY next_attempt_at ASC;
