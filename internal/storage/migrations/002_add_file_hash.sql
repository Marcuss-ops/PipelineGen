-- Add file_hash column to clips table to detect duplicate files
ALTER TABLE clips ADD COLUMN file_hash TEXT;
CREATE INDEX idx_clips_file_hash ON clips(file_hash);
