-- 004_add_drive_file_id.sql
-- Add drive_file_id and status columns if they don't exist

-- We use a safe approach for SQLite
-- Adding drive_file_id
ALTER TABLE images ADD COLUMN drive_file_id TEXT DEFAULT '';
ALTER TABLE images ADD COLUMN status TEXT DEFAULT 'ready';
ALTER TABLE images ADD COLUMN description TEXT DEFAULT '';

-- Add index for drive_file_id
CREATE INDEX IF NOT EXISTS idx_images_drive_id ON images(drive_file_id);
