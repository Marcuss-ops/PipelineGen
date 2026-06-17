-- 031_category_channels_priority.sql
-- Add priority column to category_channels for batch scheduling.
-- hot(1) = check interval / 2, normal(2) = default, cold(3) = interval × 2.

ALTER TABLE category_channels ADD COLUMN priority INTEGER NOT NULL DEFAULT 2;
