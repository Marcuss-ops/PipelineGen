-- Add local_path column to clips table
ALTER TABLE clips ADD COLUMN local_path TEXT DEFAULT '';
