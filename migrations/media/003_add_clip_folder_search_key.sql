ALTER TABLE clip_folders ADD COLUMN search_key TEXT;

UPDATE clip_folders
SET search_key = lower(replace(COALESCE(group_name, '') || ' ' || COALESCE(folder_path, ''), ' ', ''))
WHERE search_key IS NULL OR search_key = '';

CREATE INDEX IF NOT EXISTS idx_clip_folders_search_key ON clip_folders(search_key);
