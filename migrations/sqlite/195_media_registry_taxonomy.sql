-- database: primary
-- Canonical media taxonomy columns.

ALTER TABLE media_assets ADD COLUMN namespace TEXT NOT NULL DEFAULT '';
ALTER TABLE media_assets ADD COLUMN asset_kind TEXT NOT NULL DEFAULT '';
ALTER TABLE media_assets ADD COLUMN source_type TEXT NOT NULL DEFAULT '';
ALTER TABLE media_assets ADD COLUMN semantic_role TEXT NOT NULL DEFAULT '';
