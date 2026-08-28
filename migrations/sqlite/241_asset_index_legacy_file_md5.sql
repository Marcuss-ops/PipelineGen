-- 241_asset_index_legacy_file_md5.sql
--
-- Keep asset_index compatible with the canonical asset-index repository.
-- The original table exposed file_hash; the repository now reads the
-- compatibility-only legacy_file_md5 field, just like media_assets.  This
-- migration is additive and preserves the historical value.

ALTER TABLE asset_index ADD COLUMN legacy_file_md5 TEXT NOT NULL DEFAULT '';

UPDATE asset_index
SET legacy_file_md5 = file_hash
WHERE COALESCE(legacy_file_md5, '') = ''
  AND COALESCE(file_hash, '') <> '';
