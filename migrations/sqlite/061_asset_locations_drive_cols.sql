-- 061_asset_locations_drive_cols.sql
--
-- Phase 5: extend asset_locations to support Drive + Object Storage metadata.
-- Prepares for the Phase 6 backfill from media_assets.{local_path,
-- drive_file_id, drive_link, download_link} into asset_locations rows.
--
-- Field semantics (canonical):
--   * uri           — local filesystem path OR drive://FILE_ID OR s3://bucket/key.
--                     Acts as the disambiguating locator; the URI scheme is the
--                     single source-of-truth for the storage kind.
--   * external_id   — opaque identifier inside the remote system (Drive
--                     FILE_ID, S3 object key, etc.). Empty for local.
--   * access_url    — human/HTML access link (Drive web URL, S3 public URL,
--                     etc.). Empty when no browse-style view exists.
--   * download_url  — explicit download link (Drive download endpoint,
--                     pre-signed S3 GET URL, etc.). Empty when uri IS
--                     already directly usable.
--
-- Per-kind populated form:
--   local:
--     uri           = /data/media/clip.mp4
--     external_id   = ""
--     access_url    = ""
--     download_url  = ""
--   drive:
--     uri           = drive://FILE_ID
--     external_id   = FILE_ID
--     access_url    = https://drive.google.com/...
--     download_url  = download endpoint link (may be empty if access_url
--                     is sufficient)
--   object_storage:
--     uri           = s3://bucket/key
--     external_id   = object key (or composite bucket+key)
--     access_url    = optional — pre-signed or public URL
--     download_url  = optional — explicit download endpoint
--
-- DEFAULT '' keeps existing rows valid (the table was populated Phase 0-style
-- for some local-only assets). The migration runner applies this file once;
-- no manual idempotency needed.

ALTER TABLE asset_locations ADD COLUMN external_id  TEXT NOT NULL DEFAULT '';
ALTER TABLE asset_locations ADD COLUMN access_url   TEXT NOT NULL DEFAULT '';
ALTER TABLE asset_locations ADD COLUMN download_url TEXT NOT NULL DEFAULT '';

-- Conditional index for cross-engine lookups by external id (Drive/S3).
-- Partial index keeps it small: local rows (external_id = '') are skipped.
CREATE INDEX IF NOT EXISTS idx_asset_locations_external_id
    ON asset_locations(external_id)
    WHERE external_id != '';
