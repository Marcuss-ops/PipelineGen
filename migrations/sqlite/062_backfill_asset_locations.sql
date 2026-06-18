-- 062_backfill_asset_locations.sql
--
-- Backfill asset_locations from legacy media_assets columns.
-- Populates local, drive locations from local_path, drive_file_id,
-- drive_link, download_link columns. Also backfills structured columns
-- that were previously hidden in metadata_json.
--
-- Idempotent: ON CONFLICT ensures re-runs don't create duplicates.
-- Runs after 061 (which added external_id, access_url, download_url).

-- 1. Backfill local locations
INSERT INTO asset_locations (
    asset_id, location_kind, uri, file_hash, is_primary, created_at, updated_at
)
SELECT
    id,
    'local',
    local_path,
    file_hash,
    CASE WHEN COALESCE(local_path, '') != '' THEN 1 ELSE 0 END,
    created_at,
    updated_at
FROM media_assets
WHERE COALESCE(local_path, '') != ''
ON CONFLICT(asset_id, location_kind) DO UPDATE SET
    uri = excluded.uri,
    file_hash = excluded.file_hash,
    updated_at = excluded.updated_at;

-- 2. Backfill drive locations
INSERT INTO asset_locations (
    asset_id, location_kind, uri, external_id, access_url, download_url,
    file_hash, is_primary, created_at, updated_at
)
SELECT
    id,
    'drive',
    CASE
        WHEN COALESCE(drive_file_id, '') != '' THEN 'drive://' || drive_file_id
        WHEN COALESCE(drive_link, '') != '' THEN drive_link
        ELSE ''
    END,
    COALESCE(drive_file_id, ''),
    COALESCE(drive_link, ''),
    COALESCE(download_link, ''),
    COALESCE(file_hash, ''),
    CASE WHEN COALESCE(local_path, '') = '' THEN 1 ELSE 0 END,
    created_at,
    updated_at
FROM media_assets
WHERE COALESCE(drive_file_id, '') != ''
   OR COALESCE(drive_link, '') != ''
ON CONFLICT(asset_id, location_kind) DO UPDATE SET
    uri = excluded.uri,
    external_id = excluded.external_id,
    access_url = excluded.access_url,
    download_url = excluded.download_url,
    file_hash = excluded.file_hash,
    updated_at = excluded.updated_at;

-- 3. Backfill structured columns from metadata_json where NULL
UPDATE media_assets
SET filename = COALESCE(
        NULLIF(filename, ''),
        json_extract(metadata_json, '$.filename'),
        ''
    ),
    category = COALESCE(
        NULLIF(category, ''),
        json_extract(metadata_json, '$.category'),
        ''
    ),
    search_text = COALESCE(
        NULLIF(search_text, ''),
        json_extract(metadata_json, '$.search_text'),
        ''
    )
WHERE COALESCE(filename, '') = ''
   OR COALESCE(category, '') = ''
   OR COALESCE(search_text, '') = '';
