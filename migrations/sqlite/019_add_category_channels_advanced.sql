-- 019_add_category_channels_advanced.sql
-- Adds per-channel check_interval and max_videos_per_run for fine-grained
-- Channel Monitor scheduling and selection.

ALTER TABLE category_channels ADD COLUMN check_interval TEXT NOT NULL DEFAULT '24h';
ALTER TABLE category_channels ADD COLUMN max_videos_per_run INTEGER NOT NULL DEFAULT 0;
