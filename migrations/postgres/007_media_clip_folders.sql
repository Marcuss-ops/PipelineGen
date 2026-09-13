-- 007_media_clip_folders.sql
-- PostgreSQL media domain — clip_folders projection.
-- Apply after 001_media_schema.sql, only to the dedicated media database.
--
-- POSTGRES-MEDIA-CUTOVER: the catalog-sync folder reconciliation
-- (catalogsync.CatalogRepository.ListFolders / DeleteFolder) is moving off
-- the operational SQLite clip_folders table onto this PostgreSQL projection,
-- so both the folder list and the catalog existence check read one engine.
--
-- Mirrors SQLite migration 093_create_clip_folders.sql (godlike/06 parity):
--   clip_folders records one Drive-backed folder per (source, folder_path)
--   tuple, carrying the Drive folder id, manifest pointers and the
--   aggregate clip counters the synchroniser prunes against.
--
-- Writers (transition dual-write):
--   - internal/platform/sqlite/assets/imagesregistry.AssetStoreSQLite
--       UpsertFolder / UpsertDriveFolder / DeleteFolder mirror every write
--       into this projection through the injected FolderProjection port.
-- Readers:
--   - internal/platform/postgres/media.FolderRepository
--       ListFolders / GetFolder / GetFolderByPath / GetFolderByVideoID /
--       LookupDriveFolderIDBySourcePath
--
-- Every statement is idempotent (IF NOT EXISTS): re-applying on a populated
-- database is a no-op. created_at / updated_at keep the legacy TEXT shape and
-- gain TIMESTAMPTZ mirrors (the same expand pattern as migration 004).

CREATE TABLE IF NOT EXISTS clip_folders (
    id                 TEXT PRIMARY KEY,
    source             TEXT NOT NULL DEFAULT '',
    source_url         TEXT NOT NULL DEFAULT '',
    video_id           TEXT NOT NULL DEFAULT '',
    folder_id          TEXT NOT NULL DEFAULT '',
    folder_path        TEXT NOT NULL DEFAULT '',
    local_folder_path  TEXT NOT NULL DEFAULT '',
    group_name         TEXT NOT NULL DEFAULT '',
    manifest_txt_path  TEXT NOT NULL DEFAULT '',
    manifest_json_path TEXT NOT NULL DEFAULT '',
    clip_count         INTEGER NOT NULL DEFAULT 0,
    processed_count    INTEGER NOT NULL DEFAULT 0,
    failed_count       INTEGER NOT NULL DEFAULT 0,
    skipped_count      INTEGER NOT NULL DEFAULT 0,
    last_error         TEXT NOT NULL DEFAULT '',
    metadata           TEXT NOT NULL DEFAULT '{}',
    created_at         TEXT NOT NULL DEFAULT '',
    updated_at         TEXT NOT NULL DEFAULT '',
    search_key         TEXT NOT NULL DEFAULT '',
    created_at_ts      TIMESTAMPTZ,
    updated_at_ts      TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS idx_clip_folders_search_key
    ON clip_folders (search_key);

CREATE INDEX IF NOT EXISTS idx_clip_folders_source
    ON clip_folders (source);

CREATE INDEX IF NOT EXISTS idx_clip_folders_folder_path
    ON clip_folders (folder_path);

CREATE INDEX IF NOT EXISTS idx_clip_folders_video_id
    ON clip_folders (video_id);

-- TIMESTAMPTZ backfill (mirrors 004/006): populate the typed mirrors from the
-- legacy TEXT columns for rows written before this migration. Idempotent.
UPDATE clip_folders
SET created_at_ts = NULLIF(created_at, '')::timestamptz
WHERE created_at <> '' AND created_at_ts IS NULL;

UPDATE clip_folders
SET updated_at_ts = NULLIF(updated_at, '')::timestamptz
WHERE updated_at <> '' AND updated_at_ts IS NULL;
