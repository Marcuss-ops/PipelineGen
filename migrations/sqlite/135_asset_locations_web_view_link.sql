-- 135_asset_locations_web_view_link.sql
--
-- Corrective migration for the asset finalizer schema.
-- The canonical asset_locations writer uses the `web_view_link` column
-- as the human-readable Drive URL, but older local databases only had
-- access_url/download_url from migration 061. Add the missing column and
-- backfill it from access_url when present so existing rows stay readable.

ALTER TABLE asset_locations ADD COLUMN web_view_link TEXT NOT NULL DEFAULT '';

UPDATE asset_locations
SET web_view_link = COALESCE(web_view_link, access_url, '')
WHERE TRIM(web_view_link) = '' AND TRIM(access_url) != '';
