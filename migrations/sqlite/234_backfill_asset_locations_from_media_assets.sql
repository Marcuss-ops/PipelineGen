-- database: primary
-- 234_backfill_asset_locations_from_media_assets.sql
--
-- PR-LOCATIONS-CONSOLIDATE (August 2026): copies any remaining location
-- data from the deprecated media_assets shadow columns into the
-- canonical asset_locations table. Each row is backfilled only when
-- a corresponding asset_locations row does not already exist for
-- that asset_id + location_kind.
--
-- Sources:
--   media_assets.drive_file_id  → asset_locations (kind='drive')
--   media_assets.drive_link     → asset_locations.web_view_link
--   media_assets.download_link  → asset_locations.download_url
--   media_assets.local_path     → asset_locations (kind='local')
--   media_assets.folder_id      → asset_locations (kind='folder')
--   media_assets.folder_path    → asset_locations (kind='folder', uri)
--
-- After this migration, all reads should prefer asset_locations over
-- the deprecated media_assets columns. The shadow columns remain in
-- place for backward compatibility but will be dropped in a future
-- migration once all reading code has been migrated.
--
-- IDEMPOTENCY: INSERT OR IGNORE + WHERE NOT EXISTS makes this safe
-- to re-run.

-- Drive location: drive_file_id → asset_locations
INSERT OR IGNORE INTO asset_locations
    (asset_id, location_kind, uri, external_id, web_view_link, download_url, is_primary)
SELECT
    m.id,
    'drive',
    COALESCE(NULLIF(m.drive_file_id, ''), m.id),
    m.drive_file_id,
    m.drive_link,
    m.download_link,
    1
FROM media_assets m
WHERE (m.drive_file_id != '' OR m.drive_link != '')
  AND NOT EXISTS (
    SELECT 1 FROM asset_locations al
    WHERE al.asset_id = m.id AND al.location_kind = 'drive'
  );

-- Local location: local_path → asset_locations
INSERT OR IGNORE INTO asset_locations
    (asset_id, location_kind, uri, is_primary)
SELECT
    m.id,
    'local',
    m.local_path,
    CASE WHEN m.drive_file_id = '' THEN 1 ELSE 0 END
FROM media_assets m
WHERE m.local_path != ''
  AND NOT EXISTS (
    SELECT 1 FROM asset_locations al
    WHERE al.asset_id = m.id AND al.location_kind = 'local'
  );

-- Folder location: folder_id → asset_locations
INSERT OR IGNORE INTO asset_locations
    (asset_id, location_kind, uri, external_id, is_primary)
SELECT
    m.id,
    'folder',
    COALESCE(NULLIF(m.folder_path, ''), m.folder_id),
    m.folder_id,
    0
FROM media_assets m
WHERE m.folder_id != ''
  AND NOT EXISTS (
    SELECT 1 FROM asset_locations al
    WHERE al.asset_id = m.id AND al.location_kind = 'folder'
  );
