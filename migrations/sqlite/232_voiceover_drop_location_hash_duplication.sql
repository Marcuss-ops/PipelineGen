-- database: primary
-- 232_voiceover_drop_location_hash_duplication.sql
--
-- PR-VO-ASSET-ID (August 2026): drops the columns that duplicate
-- facts already owned by the media_assets projection. The voiceover
-- finalizer (finalizer_execute.go) already creates a media_assets
-- row with the same id, and the canonical MediaCommitter writes
-- asset_locations with Drive data.
--
-- After cutover:
--   voiceovers:        business identity (text_hash, language,
--                      voice, fingerprint, job_id, asset_id)
--   media_assets:      canonical asset row (same id)
--   asset_locations:   physical locations (Drive, local, folder)
--
-- Columns being dropped (all moved to asset_locations + media_assets):
--   filename             → media_assets.filename
--   local_path           → asset_locations (kind='local')
--   cleaned_path         → asset_locations (kind='local_postproc')
--   folder_id            → asset_locations (kind='drive')
--   folder_path          → asset_locations
--   drive_file_id        → asset_locations.external_id (kind='drive')
--   drive_link           → asset_locations.web_view_link
--   download_link        → asset_locations.download_url
--   legacy_file_md5      → media_assets.content_hash / media_asset_sources
--
-- IDEMPOTENCY: NOT idempotent. SQLite DROP COLUMN has no IF EXISTS.
-- APPLY ONCE after all Go readers have been migrated to read from
-- the media_assets projection.

-- SQLite refuses to drop a column referenced by an index.
DROP INDEX IF EXISTS idx_voiceovers_folder_id;

ALTER TABLE voiceovers DROP COLUMN filename;
ALTER TABLE voiceovers DROP COLUMN local_path;
ALTER TABLE voiceovers DROP COLUMN cleaned_path;
ALTER TABLE voiceovers DROP COLUMN folder_id;
ALTER TABLE voiceovers DROP COLUMN folder_path;
ALTER TABLE voiceovers DROP COLUMN drive_file_id;
ALTER TABLE voiceovers DROP COLUMN drive_link;
ALTER TABLE voiceovers DROP COLUMN download_link;
ALTER TABLE voiceovers DROP COLUMN legacy_file_md5;
