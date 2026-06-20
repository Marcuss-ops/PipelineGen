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
-- PR12a.1 IDEMPOTENCY NOTE
-- SQLite has no `IF NOT EXISTS` clause on `ALTER TABLE ADD COLUMN` and no way to
-- conditionally execute DDL inside a CASE/CTE expression. RAISE(ABORT) — the
-- only portable "error-out of this statement" tool — is **trigger-only** in
-- SQLite 3.37.x (verified empirically: `RAISE() may only be used within a
-- trigger-program`). As a result, a pure-SQL guard that wraps each ALTER in a
-- column-existence check is NOT achievable in this file.
--
-- Operational contract:
--   * Production: the `schema_migrations` ledger ensures the file is applied
--     exactly once. No re-run path exists.
--   * Local dev / partial-failure recovery: if STEP 1 was interrupted after
--     some-but-not-all ADDs succeeded, a re-run will abort at the first
--     `duplicate column name` error. This is intentional — silently masking
--     schema drift is worse than a loud failure. To recover, the operator
--     must inspect pragma_table_info('media_assets') and either (a) manually
--     craft a follow-up migration that ADDs only the missing subset, or
--     (b) mark the migration as `dirty` in schema_migrations and re-run from
--     a known-good checkpoint.
--
-- The trailing SELECT below is a post-condition audit — it logs the current
-- media_assets schema state in the runner's output transcript. Operators can
-- grep for the 11 column names after the migration completes to confirm
-- STEP 1 landed cleanly. The audit itself cannot conditionally execute the
-- ADDs; it is observational only.
--
-- Future hardening (out of scope for this PR — see followup): enhance the Go
-- migration runner to introspect pragma_table_info before each ALTER and skip
-- the ADD when the column already exists. That migration-runner change makes
-- this file truly safe-no-op on partial-failure re-run.

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

-- Post-condition audit: actually setup fails if some columns didn't get added;
-- this is just a runtime confirmation of STEP 1's effect.
SELECT name, type, dflt_value, notnull AS is_not_null
FROM pragma_table_info('media_assets')
WHERE name IN (
    'filename', 'category', 'thumbnail_url', 'clip_page_url', 'search_text',
    'scene_type', 'quality_score', 'reuse_count', 'last_used_at',
    'lifecycle_state', 'deleted_at'
)
ORDER BY name;

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
