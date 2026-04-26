-- 0004_expand_clips.sql
-- Expand clips table to support Artlist metadata and other sources

ALTER TABLE clips ADD COLUMN source TEXT;
ALTER TABLE clips ADD COLUMN category TEXT;
ALTER TABLE clips ADD COLUMN external_url TEXT;
ALTER TABLE clips ADD COLUMN duration INTEGER DEFAULT 0;
ALTER TABLE clips ADD COLUMN metadata TEXT; -- JSON for extra fields
