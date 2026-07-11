-- 143_asset_locations_finalizer_columns.sql
--
-- Add columns required by the canonical AssetFinalizerTx when writing
-- asset_locations rows.
ALTER TABLE asset_locations ADD COLUMN external_id TEXT NOT NULL DEFAULT '';
ALTER TABLE asset_locations ADD COLUMN web_view_link TEXT NOT NULL DEFAULT '';
ALTER TABLE asset_locations ADD COLUMN download_url TEXT NOT NULL DEFAULT '';
