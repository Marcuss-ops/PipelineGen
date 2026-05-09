-- clips_006_add_hierarchy_columns.sql
-- Aggiunge campi per la navigazione gerarchica vera

ALTER TABLE clips ADD COLUMN parent_folder_id TEXT DEFAULT '';
ALTER TABLE clips ADD COLUMN depth INTEGER DEFAULT 0;
ALTER TABLE clips ADD COLUMN is_folder INTEGER DEFAULT 0;

CREATE INDEX IF NOT EXISTS idx_clips_parent_folder_id ON clips(parent_folder_id);
CREATE INDEX IF NOT EXISTS idx_clips_is_folder ON clips(is_folder);
CREATE INDEX IF NOT EXISTS idx_clips_parent_folder_sort ON clips(parent_folder_id, is_folder DESC, name ASC);

-- Backfill data
UPDATE clips 
SET parent_folder_id = COALESCE(folder_id, '')
WHERE parent_folder_id = '' OR parent_folder_id IS NULL;

UPDATE clips 
SET is_folder = 1 
WHERE category = 'folder';

-- Set root folders parent_id to empty string
UPDATE clips 
SET parent_folder_id = '' 
WHERE is_folder = 1 AND parent_folder_id = id;

-- Calculate depth based on folder_path
UPDATE clips 
SET depth = length(folder_path) - length(replace(folder_path, '/', ''));
