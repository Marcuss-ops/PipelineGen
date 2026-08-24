-- 159_media_assets_atomic_writer_columns.sql
--
-- Expand-phase migration for the clip/artlist atomic writers.
-- `internal/platform/sqlite/assets/asset_committer.go`
-- now writes three canonical per-asset columns that were already
-- declared in the in-memory canonical schema but never added to the
-- SQLite migration chain on live databases.
--
-- These fields are intentionally NOT backfilled from other tables here:
-- existing rows can safely keep the empty-string default, and new writes
-- will populate the columns directly. The goal is to close the schema
-- drift so UPSERTs stop failing on older databases.
--
-- Idempotency: SQLite `ALTER TABLE ... ADD COLUMN` has no IF NOT EXISTS
-- form, so rerunning the migration on a DB that already has the columns
-- will be soft-skipped by the migration runner's duplicate-column guard.

ALTER TABLE media_assets ADD COLUMN asset_version  TEXT NOT NULL DEFAULT '';
ALTER TABLE media_assets ADD COLUMN asset_location TEXT NOT NULL DEFAULT '';
ALTER TABLE media_assets ADD COLUMN rendition      TEXT NOT NULL DEFAULT '';
