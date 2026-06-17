-- 017_add_category_channels_columns.sql
-- Adds columns that were added to the 015 migration after it was already
-- applied to production databases. Safe to run on existing tables.

ALTER TABLE category_channels ADD COLUMN semantic_keywords TEXT NOT NULL DEFAULT '[]';
ALTER TABLE category_channels ADD COLUMN min_semantic_score INTEGER NOT NULL DEFAULT 60;
ALTER TABLE category_channels ADD COLUMN playlist_end INTEGER NOT NULL DEFAULT -1;
