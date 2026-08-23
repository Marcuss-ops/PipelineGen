-- database: primary
-- 229_rename_file_hash_to_legacy_file_md5.sql
--
-- EXPAND + BACKFILL phase (godlike/06 SSOT — expand → backfill → cutover
-- → contract). We ONLY ADD columns (no DROP, no RENAME of existing) per
-- the canonical migration 152 convention. The legacy `file_hash` column
-- remains intact on each table for the minimum-blast-radius window;
-- a future contract-phase PR will DROP the old columns.
--
-- The `file_hash` column historically held a mix of MD5 (32 hex) and
-- SHA-256 (64 hex). The canonical SHA-256 home is `binary_sha256` on
-- media_assets (migration 152). The `legacy_file_md5` column holds the
-- existing value as-is — it is the continuation of the same legacy
-- bucket, now with a typed name per godlike/06 "one-owner-per-fact".
-- Writers and readers in the codebase are cut over to the new column
-- name in the same change.
--
-- Tables:
--   media_assets, asset_locations, voiceovers,
--   asset_versions, stock_source_cache, asset_subtitle_artifacts,
--   artlist_clips, entity_image_catalog_candidates,
--   entity_image_catalog_materializations

ALTER TABLE media_assets ADD COLUMN legacy_file_md5 TEXT NOT NULL DEFAULT '';
UPDATE media_assets SET legacy_file_md5 = file_hash WHERE file_hash != '';

ALTER TABLE asset_locations ADD COLUMN legacy_file_md5 TEXT NOT NULL DEFAULT '';
UPDATE asset_locations SET legacy_file_md5 = file_hash WHERE file_hash != '';

ALTER TABLE voiceovers ADD COLUMN legacy_file_md5 TEXT NOT NULL DEFAULT '';
UPDATE voiceovers SET legacy_file_md5 = file_hash WHERE file_hash != '';

ALTER TABLE asset_versions ADD COLUMN legacy_file_md5 TEXT NOT NULL DEFAULT '';
UPDATE asset_versions SET legacy_file_md5 = file_hash WHERE file_hash != '';

ALTER TABLE stock_source_cache ADD COLUMN legacy_file_md5 TEXT NOT NULL DEFAULT '';
UPDATE stock_source_cache SET legacy_file_md5 = file_hash WHERE file_hash != '';

ALTER TABLE asset_subtitle_artifacts ADD COLUMN legacy_file_md5 TEXT NOT NULL DEFAULT '';
UPDATE asset_subtitle_artifacts SET legacy_file_md5 = file_hash WHERE file_hash != '';

ALTER TABLE artlist_clips ADD COLUMN legacy_file_md5 TEXT NOT NULL DEFAULT '';
UPDATE artlist_clips SET legacy_file_md5 = file_hash WHERE file_hash != '';

ALTER TABLE entity_image_catalog_candidates ADD COLUMN legacy_file_md5 TEXT NOT NULL DEFAULT '';

ALTER TABLE entity_image_catalog_materializations ADD COLUMN legacy_file_md5 TEXT NOT NULL DEFAULT '';
UPDATE entity_image_catalog_materializations SET legacy_file_md5 = file_hash WHERE file_hash != '';
