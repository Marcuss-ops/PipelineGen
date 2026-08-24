-- 068_add_media_assets_missing_columns.sql
-- Adds missing columns to media_assets that are referenced by
-- mediaAssetColumns (query_helpers.go) and clips_repository.go
-- but were never included in any migration.
--
-- The media_assets table was created in migration 033 with a minimal
-- schema. Migration 059 added lifecycle/quality/folder columns but
-- missed width, height, and group_name. These columns are queried
-- via COALESCE(col, default) in both domain/asset/query_helpers.go
-- and platform/sqlite/assets/clips_repository.go.
--
-- Fixes: "no such column: width", "no such column: group_name"
--        in /api/media/search and SearchClips paths.

ALTER TABLE media_assets ADD COLUMN width INTEGER NOT NULL DEFAULT 0;
ALTER TABLE media_assets ADD COLUMN height INTEGER NOT NULL DEFAULT 0;
ALTER TABLE media_assets ADD COLUMN group_name TEXT NOT NULL DEFAULT '';
