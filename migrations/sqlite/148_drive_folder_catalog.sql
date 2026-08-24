-- F3: drive_folder_catalog (July 2026)
-- Canonical SQLite table backing the Drive Destination Catalog.
-- Stores resolved folder IDs for every destination+path pair so the
-- publisher never needs to call Drive's folder listing API on every
-- upload — the catalog acts as a local index of already-created folders.
--
-- Schema design:
--   destination  → canonical DestinationKey (e.g. "youtube_clip")
--   namespace    → canonical top-level dir (e.g. "clips", "stock")
--   path         → full logical path under the root (e.g. "clips/finance/vid123")
--   folder_id    → resolved Google Drive folder ID
--   parent_folder_id → Drive folder ID of the parent (the root or a
--                       previously-created intermediate folder)
--   source       → how the folder was resolved: "bootstrap" (created by
--                   admin CLI), "discovered" (found on Drive by reconcile),
--                   "created" (created lazily at first publish), or
--                   "config" (hardcoded from config root folders)
--   status       → "active" (folder exists and is reachable),
--                   "missing" (not yet created or deleted on Drive),
--                   "invalid" (Drive API returned an error)
--
-- UNIQUE(destination, path) ensures one row per logical path.
--
-- EXPAND phase (this migration):
--   1. The table is created.
--   2. Writes happen via the canonical repository at
--      internal/platform/sqlite/delivery/repository.go.
--   3. The admin drive bootstrap command (F4 forward-pointer) is the
--      primary writer.
--
-- BACKFILL phase (forward-pointer, F5 drive reconcile):
--   - The reconcile CLI scans Drive recursively and backfills rows for
--     already-existing folders.
--
-- CUTOVER phase (forward-pointer, F5):
--   - The publisher reads the catalog before calling Drive's folder
--     listing API; if a row exists with status='active', it uses the
--     cached folder_id directly.
--
-- godlike/07 minimum-blast-radius: pure additive migration.
-- - No column drops or renames.
-- - No data migration of existing tables.
-- - DEFAULT '' for all TEXT columns ensures backward-compat.

CREATE TABLE IF NOT EXISTS drive_folder_catalog (
    id               INTEGER PRIMARY KEY AUTOINCREMENT,
    destination      TEXT NOT NULL,
    namespace        TEXT NOT NULL DEFAULT '',
    path             TEXT NOT NULL,
    folder_id        TEXT NOT NULL DEFAULT '',
    parent_folder_id TEXT NOT NULL DEFAULT '',
    source           TEXT NOT NULL DEFAULT 'created',
    status           TEXT NOT NULL DEFAULT 'active',
    created_at       TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    updated_at       TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),

    UNIQUE(destination, path)
);

-- Index for lookups by destination (e.g. "find all folders for youtube_clip").
CREATE INDEX IF NOT EXISTS idx_drive_folder_catalog_dest
    ON drive_folder_catalog(destination);

-- Index for status-based queries (e.g. "find all missing folders").
CREATE INDEX IF NOT EXISTS idx_drive_folder_catalog_status
    ON drive_folder_catalog(status);
