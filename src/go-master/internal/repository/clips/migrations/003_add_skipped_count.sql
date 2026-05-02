-- Add skipped_count to clip_folders table
ALTER TABLE clip_folders ADD COLUMN skipped_count INTEGER DEFAULT 0;
