-- 006_media_asset_versions.sql
-- PostgreSQL + pgvector media domain — sequential version history.
-- Apply after 001_media_schema.sql, only to the dedicated media database.
--
-- Mirrors SQLite migration 105_asset_versions.sql (godlike/06 parity):
--   asset_versions tracks every sequential version of a media asset as it
--   is reprocessed, re-encoded or re-sourced. (asset_id, version_number)
--   is the monotone per-asset sequence written by the canonical
--   AssetTxFinalizer.insertAssetVersion (MAX(version_number)+1 inside the
--   caller's tx, UNIQUE enforces the contract under concurrent writers).
--
-- Consumed by:
--   - internal/capabilities/assets/finalizer.AssetTxFinalizer.insertAssetVersion
--       SELECT COALESCE(MAX(version_number),0)+1 FROM asset_versions WHERE asset_id = $1
--       INSERT INTO asset_versions (asset_id, version_number, source_uri,
--              legacy_file_md5, file_size_bytes, mime_type, metadata_json, created_at)
--              VALUES ($1..$8)
--   - HardDelete / purge paths (DELETE FROM asset_versions WHERE asset_id = $1)
--
-- Every statement is idempotent (IF NOT EXISTS): re-applying on a populated
-- database is a no-op. The legacy_file_md5 column is the canonical md5-tier
-- hash (SQLite renamed file_hash → legacy_file_md5 in migration 229).

CREATE TABLE IF NOT EXISTS asset_versions (
    id              BIGSERIAL PRIMARY KEY,
    asset_id        TEXT NOT NULL
                    REFERENCES media_assets(id)
                    ON DELETE CASCADE,
    version_number  INTEGER NOT NULL,
    source_uri      TEXT NOT NULL DEFAULT '',
    file_hash       TEXT NOT NULL DEFAULT '',
    legacy_file_md5 TEXT NOT NULL DEFAULT '',
    file_size_bytes BIGINT NOT NULL DEFAULT 0,
    mime_type       TEXT NOT NULL DEFAULT '',
    metadata_json   TEXT NOT NULL DEFAULT '{}',
    created_at      TEXT NOT NULL DEFAULT '',
    created_at_ts   TIMESTAMPTZ,

    -- Per-asset sequence contract: the same (asset_id, version_number)
    -- cannot appear twice. The UNIQUE pulls the invariant into SQL where
    -- it fires under cross-transaction races that MAX+1 alone cannot.
    UNIQUE (asset_id, version_number)
);

CREATE INDEX IF NOT EXISTS idx_asset_versions_asset
    ON asset_versions (asset_id);

-- TIMESTAMPTZ backfill for the new table (mirrors 004 pattern):
-- populate created_at_ts from legacy TEXT for rows written before this
-- migration, idempotent on re-run.
UPDATE asset_versions
SET created_at_ts = NULLIF(created_at, '')::timestamptz
WHERE created_at <> '' AND created_at_ts IS NULL;

CREATE INDEX IF NOT EXISTS idx_asset_versions_created_at_ts
    ON asset_versions (created_at_ts);
