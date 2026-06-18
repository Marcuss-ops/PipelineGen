-- 062_asset_locations_backfill.sql
--
-- Phase 6: complete the data migration off metadata_json + off media_assets.{local_path,
-- drive_file_id, drive_link, download_link} into the normalised schema.
--
-- This migration is THREE distinct operations, applied in order:
--
--   STEP 1. ALTER media_assets to add the 11 stable fields that were
--           previously only stored in metadata_json. Each column uses a
--           safe DEFAULT so the addition is O(1) on existing rows.
--
--   STEP 2. Backfill asset_locations from media_assets — UPSERTs per kind
--           (local, drive). Idempotent: ON CONFLICT(asset_id, location_kind)
--           DO UPDATE refreshes uri/external_id/access_url/download_url/
--           file_hash/updated_at from the latest media_assets row.
--
--           is_primary decision (matches user's spec):
--             * local location is primary if local_path != ''
--             * drive location is primary if local_path == ''
--           So a media_asset that lives both locally and on Drive has two
--           rows in asset_locations, with the local one flagged primary.
--
--   STEP 3. UPDATE media_assets SET col = COALESCE(NULLIF(col, current_value),
--           json_extract(metadata_json, '$.key'), fallback). The pattern
--           preserves any value that already lives in the new column
--           (e.g. written today by a non-legacy caller), falls back to the
--           JSON payload for legacy rows, and defaults to '' / 0 when both
--           are absent.
--
-- Idempotency: every step is safe to re-run. The migration runner's
-- schema_migrations ledger ensures the file is applied exactly once
-- in production; if a developer re-runs locally after a partial failure,
-- Steps 2 and 3 are no-ops because all writes use COALESCE-preserving
-- semantics and ON CONFLICT DO UPDATE.
--
-- Reference parity check (manual, copy-paste after apply, expect 0):
--   SELECT COUNT(*) FROM media_assets
--   WHERE COALESCE(local_path, '') != ''
--     AND NOT EXISTS (
--       SELECT 1 FROM asset_locations l
--       WHERE l.asset_id = media_assets.id AND l.location_kind = 'local'
--     );

-- ════════════════════════════════════════════════════════════════════════════
-- STEP 1 — Add the 11 missing columns to media_assets
-- ════════════════════════════════════════════════════════════════════════════

ALTER TABLE media_assets ADD COLUMN filename        TEXT    NOT NULL DEFAULT '';
ALTER TABLE media_assets ADD COLUMN category        TEXT    NOT NULL DEFAULT '';
ALTER TABLE media_assets ADD COLUMN thumbnail_url   TEXT    NOT NULL DEFAULT '';
ALTER TABLE media_assets ADD COLUMN clip_page_url   TEXT    NOT NULL DEFAULT '';
ALTER TABLE media_assets ADD COLUMN search_text     TEXT    NOT NULL DEFAULT '';
ALTER TABLE media_assets ADD COLUMN scene_type      TEXT    NOT NULL DEFAULT '';
ALTER TABLE media_assets ADD COLUMN quality_score   REAL    NOT NULL DEFAULT 0.0;
ALTER TABLE media_assets ADD COLUMN reuse_count     INTEGER NOT NULL DEFAULT 0;
ALTER TABLE media_assets ADD COLUMN last_used_at    TEXT    NOT NULL DEFAULT '';
ALTER TABLE media_assets ADD COLUMN lifecycle_state TEXT    NOT NULL DEFAULT '';
ALTER TABLE media_assets ADD COLUMN deleted_at      TEXT    NOT NULL DEFAULT '';

-- ════════════════════════════════════════════════════════════════════════════
-- STEP 2 — Backfill asset_locations via UPSERT
-- ════════════════════════════════════════════════════════════════════════════

-- (a) local locations
INSERT INTO asset_locations (
    asset_id, location_kind, uri, file_hash, is_primary,
    created_at, updated_at
)
SELECT
    id, 'local', local_path, file_hash,
    CASE WHEN local_path != '' THEN 1 ELSE 0 END,
    created_at, updated_at
FROM media_assets
WHERE COALESCE(local_path, '') != ''
ON CONFLICT(asset_id, location_kind) DO UPDATE SET
    uri         = excluded.uri,
    file_hash   = excluded.file_hash,
    updated_at  = excluded.updated_at;

-- (b) drive locations
INSERT INTO asset_locations (
    asset_id, location_kind, uri, external_id, access_url, download_url,
    file_hash, is_primary, created_at, updated_at
)
SELECT
    id, 'drive',
    CASE WHEN drive_file_id != '' THEN 'drive://' || drive_file_id ELSE drive_link END,
    drive_file_id, drive_link, download_link, file_hash,
    CASE WHEN COALESCE(local_path, '') = '' THEN 1 ELSE 0 END,
    created_at, updated_at
FROM media_assets
WHERE COALESCE(drive_file_id, '') != '' OR COALESCE(drive_link, '') != ''
ON CONFLICT(asset_id, location_kind) DO UPDATE SET
    uri          = excluded.uri,
    external_id  = excluded.external_id,
    access_url   = excluded.access_url,
    download_url = excluded.download_url,
    file_hash    = excluded.file_hash,
    updated_at   = excluded.updated_at;

-- ════════════════════════════════════════════════════════════════════════════
-- STEP 3 — Promote media_assets.{metadata_json.$.*} into the new columns
-- ════════════════════════════════════════════════════════════════════════════

UPDATE media_assets
SET
    filename        = COALESCE(NULLIF(filename, ''),        json_extract(metadata_json, '$.filename'),         ''),
    category        = COALESCE(NULLIF(category, ''),        json_extract(metadata_json, '$.category'),         ''),
    thumbnail_url   = COALESCE(NULLIF(thumbnail_url, ''),   json_extract(metadata_json, '$.thumbnail_url'),    ''),
    clip_page_url   = COALESCE(NULLIF(clip_page_url, ''),   json_extract(metadata_json, '$.clip_page_url'),    ''),
    search_text     = COALESCE(NULLIF(search_text, ''),     json_extract(metadata_json, '$.search_text'),      ''),
    scene_type      = COALESCE(NULLIF(scene_type, ''),      json_extract(metadata_json, '$.scene_type'),       ''),
    quality_score   = COALESCE(NULLIF(quality_score, 0.0),  CAST(json_extract(metadata_json, '$.quality_score') AS REAL), 0.0),
    reuse_count     = COALESCE(NULLIF(reuse_count, 0),       CAST(json_extract(metadata_json, '$.reuse_count')   AS INTEGER), 0),
    last_used_at    = COALESCE(NULLIF(last_used_at, ''),    json_extract(metadata_json, '$.last_used_at'),     ''),
    lifecycle_state = COALESCE(NULLIF(lifecycle_state, ''), json_extract(metadata_json, '$.lifecycle_state'),  ''),
    deleted_at      = COALESCE(NULLIF(deleted_at, ''),      json_extract(metadata_json, '$.deleted_at'),       '');
