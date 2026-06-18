-- 058_asset_processing.sql
-- Canonical asset_processing table: tracks the lifecycle of each processing
-- step (download, normalize, transcription, embedding, indexing, upload,
-- verify, cleanup) for a media asset.
--
-- 8 canonical stages (step values):
--   download      – fetch from source provider
--   normalize     – transcode / normalize format
--   transcription – speech-to-text (Whisper)
--   embedding     – generate semantic vectors
--   indexing      – upsert into vector store (Qdrant)
--   upload        – push to storage destination (Drive / S3)
--   verify        – integrity check (hash / playback)
--   cleanup       – remove temp / intermediate files
--
-- One row per (asset_id, step). Status lifecycle:
--   pending   → running → completed
--                        → failed → running (retry)
--   completed → running (reprocessing)

CREATE TABLE IF NOT EXISTS asset_processing (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    asset_id        TEXT NOT NULL,
    step            TEXT NOT NULL,
    status          TEXT NOT NULL DEFAULT 'pending'
                    CHECK (status IN ('pending', 'running', 'completed', 'failed')),
    started_at      TEXT,
    completed_at    TEXT,
    error_message   TEXT NOT NULL DEFAULT '',
    attempt_count   INTEGER NOT NULL DEFAULT 1,
    metadata_json   TEXT NOT NULL DEFAULT '{}',
    created_at      TEXT NOT NULL DEFAULT '',
    updated_at      TEXT NOT NULL DEFAULT '',
    UNIQUE (asset_id, step)
);

CREATE INDEX IF NOT EXISTS idx_asset_processing_asset
    ON asset_processing(asset_id);

CREATE INDEX IF NOT EXISTS idx_asset_processing_status
    ON asset_processing(status);
