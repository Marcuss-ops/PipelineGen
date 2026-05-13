-- Migration: Add deleted_at for soft-deletes

ALTER TABLE clips ADD COLUMN deleted_at DATETIME;
CREATE INDEX IF NOT EXISTS idx_clips_deleted_at ON clips(deleted_at) WHERE deleted_at IS NOT NULL;
