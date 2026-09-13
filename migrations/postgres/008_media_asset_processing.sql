-- 008_media_asset_processing.sql
-- PostgreSQL media domain — asset_processing (pipeline-step progress).
-- Apply after 001_media_schema.sql, only to the dedicated media database.
--
-- MEDIA-SSOT WRITE-BRIDGE (September 2026): asset_processing was written
-- through the generic detail.ProcessingRepository seam, which the
-- composition root satisfied with the operational SQLite AssetStoreSQLite
-- even when PostgreSQL owned media_assets. That made per-step pipeline
-- progress (download / normalize / transcription / …) invisible to the
-- canonical media SSOT and split the media aggregate across two engines.
-- asset_processing is classified MEDIA-AUTHORITATIVE: it is part of the
-- media aggregate's observable state, so it lands on the canonical
-- PostgreSQL media database alongside media_assets and asset_locations.
--
-- Mirrors the canonical SQLite asset_processing (migration 058) with the
-- PostgreSQL conventions of this schema family:
--   - `id` is a PostgreSQL-owned BIGSERIAL surrogate key. The stable
--     cross-engine identity is the UNIQUE (asset_id, step) conflict target
--     used by the canonical upsert, exactly as in asset_locations.
--   - TEXT timestamps are kept RFC 3339 (UTC) for byte-identical
--     cross-engine formatting and gain TIMESTAMPTZ mirrors (the same expand
--     pattern as migrations 004 / 006 / 007).
--   - status stays a TEXT enum with the same CHECK vocabulary as SQLite.
--
-- Writers:
--   - internal/platform/postgres/media.PostgresMediaCommitter
--       (persistence.AssetProcessingWriter — StartAssetProcessing /
--       CompleteAssetProcessing / FailAssetProcessing), resolved only
--       through persistence.CanonicalAssetProcessingWriter.
-- Readers: none yet (the operational read surface is still the SQLite
-- mirror; the write bridge is what this migration closes).
--
-- Every statement is idempotent (IF NOT EXISTS): re-applying on a populated
-- database is a no-op.

CREATE TABLE IF NOT EXISTS asset_processing (
    id              BIGSERIAL PRIMARY KEY,
    asset_id        TEXT NOT NULL REFERENCES media_assets(id) ON DELETE CASCADE,
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
    started_at_ts   TIMESTAMPTZ,
    completed_at_ts TIMESTAMPTZ,
    UNIQUE (asset_id, step)
);

CREATE INDEX IF NOT EXISTS idx_asset_processing_asset
    ON asset_processing (asset_id);

CREATE INDEX IF NOT EXISTS idx_asset_processing_status
    ON asset_processing (status);

-- TIMESTAMPTZ backfill (mirrors 004/006/007): populate the typed mirrors from
-- the legacy TEXT columns for rows written before this migration. Idempotent.
UPDATE asset_processing
SET started_at_ts = NULLIF(started_at, '')::timestamptz
WHERE started_at <> '' AND started_at_ts IS NULL;

UPDATE asset_processing
SET completed_at_ts = NULLIF(completed_at, '')::timestamptz
WHERE completed_at <> '' AND completed_at_ts IS NULL;
