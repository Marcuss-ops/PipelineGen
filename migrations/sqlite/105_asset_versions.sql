-- Migration 105: asset_versions — sequential version history per media asset.
--
-- Tracks every version of a media asset as it is reprocessed, re-encoded,
-- re-sourced, or re-uploaded. Each row is a snapshot of an asset's content
-- at a specific (asset_id, version_number). One-to-many child of media_assets.
--
-- Consumed by:
--   - internal/domain/asset/lifecycle_core.go
--       SELECT id, asset_id, version_number, source_uri, file_hash,
--              file_size_bytes, mime_type, metadata_json, created_at
--         FROM asset_versions WHERE asset_id = ? ORDER BY version_number DESC
--       SELECT COALESCE(MAX(version_number), 0) + 1 FROM asset_versions
--         WHERE asset_id = ?
--       INSERT INTO asset_versions (asset_id, version_number, source_uri,
--              file_hash, file_size_bytes, mime_type, metadata_json, created_at)
--              VALUES (?, ?, ?, ?, ?, ?, ?, ?)
--   - internal/domain/asset/store_core.go HardDelete adapter (line 391)
--       DELETE FROM asset_versions WHERE asset_id = ?
--   - internal/platform/sqlite/assets/txmutation/primitives.go
--       HardDeleteTx's hardDeleteChildTables also deletes via
--         WHERE asset_id = ?
--
-- Closing the drift (June 2026, PR-Asset-Versions-Migration):
-- The table was referenced by HardDeleteTx and store_core.go HardDelete
-- but no `CREATE TABLE asset_versions` migration existed in the tree.
-- Without this migration, a fresh install would error with
-- "no such table: asset_versions" on every HardDeleteTx call.
-- Production callers always run against a fully-migrated DB so the
-- drift was invisible; this migration makes the post-CUTOVER-state
-- (migration-ledger equals 105) the only canonical state.
--
-- Schema decisions (godlike/06 + 055 sibling convention):
--   - FK to media_assets(id) ON DELETE CASCADE — strict child-table
--     semantics; defensive against external SQL manipulation or
--     missed transaction cascades. HardDeleteTx still issues the
--     manual DELETE first (idempotent), and CASCADE is a safety net
--     that costs nothing on top.
--   - UNIQUE (asset_id, version_number) — enforces the per-asset
--     sequential version_number contract used by lifecycle_core.go's
--     SELECT COALESCE(MAX(version_number), 0) + 1 read pattern.
--     Pulls the invariant into SQL where it can fire under
--     cross-transaction races that the application-level MAX+1
--     logic alone cannot detect.
--   - created_at default '' — matches the 055/058 child-table
--     convention. lifecycle_core.go always passes an explicit
--     timeutil.FormatRFC3339(time.Now()) so the default is a
--     fallback only; consistency with siblings wins over the 054
--     parent-table (datetime('now')) convention.

CREATE TABLE IF NOT EXISTS asset_versions (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    asset_id        TEXT NOT NULL
                    REFERENCES media_assets(id)
                    ON DELETE CASCADE,
    version_number  INTEGER NOT NULL,
    source_uri      TEXT NOT NULL DEFAULT '',
    file_hash       TEXT NOT NULL DEFAULT '',
    file_size_bytes INTEGER NOT NULL DEFAULT 0,
    mime_type       TEXT NOT NULL DEFAULT '',
    metadata_json   TEXT NOT NULL DEFAULT '{}',
    created_at      TEXT NOT NULL DEFAULT '',

    -- Per-asset sequence contract: the same (asset_id, version_number)
    -- cannot appear twice. lifecycle_core.go's MAX(version_number)+1
    -- auto-assignment relies on this; pulling the invariant into SQL
    -- catches the cross-transaction race that application-level
    -- discipline alone cannot.
    UNIQUE (asset_id, version_number)
);

-- Dedicated index for the HardDeleteTx / store_core.go HardDelete hot
-- path (`DELETE FROM asset_versions WHERE asset_id = ?` runs on every
-- purge). Redundant with the UNIQUE composite above for asset_id-only
-- prefix lookups; kept to satisfy sibling-convention symmetry with the
-- 055/058 child-table pattern and to serve the DELETE hot path without
-- relying on SQLite's internal composite-index traversal.
CREATE INDEX IF NOT EXISTS idx_asset_versions_asset
    ON asset_versions (asset_id);
