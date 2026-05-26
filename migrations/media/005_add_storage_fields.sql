-- Aggiunge campi di storage fisici fuori da metadata_json.
-- Questa migration trasforma media_assets in una tabella con colonne reali
-- per i campi fondamentali (local_path, drive_file_id, hash, etc.) invece
-- di seppellirli dentro metadata_json.

ALTER TABLE media_assets ADD COLUMN media_type TEXT NOT NULL DEFAULT '';
ALTER TABLE media_assets ADD COLUMN status TEXT NOT NULL DEFAULT 'ready';
ALTER TABLE media_assets ADD COLUMN local_path TEXT;
ALTER TABLE media_assets ADD COLUMN relative_path TEXT;
ALTER TABLE media_assets ADD COLUMN drive_file_id TEXT;
ALTER TABLE media_assets ADD COLUMN drive_folder_id TEXT;
ALTER TABLE media_assets ADD COLUMN drive_link TEXT;
ALTER TABLE media_assets ADD COLUMN download_link TEXT;
ALTER TABLE media_assets ADD COLUMN file_hash TEXT;
ALTER TABLE media_assets ADD COLUMN content_hash TEXT;
ALTER TABLE media_assets ADD COLUMN width INTEGER DEFAULT 0;
ALTER TABLE media_assets ADD COLUMN height INTEGER DEFAULT 0;
ALTER TABLE media_assets ADD COLUMN updated_at TEXT;

-- Backfill: estrai da metadata_json per immagini (source='image')
UPDATE media_assets
SET file_hash = json_extract(metadata_json, '$.hash')
WHERE source = 'image'
  AND file_hash IS NULL
  AND json_extract(metadata_json, '$.hash') IS NOT NULL;

UPDATE media_assets
SET local_path = json_extract(metadata_json, '$.local_path')
WHERE source = 'image'
  AND local_path IS NULL
  AND json_extract(metadata_json, '$.local_path') IS NOT NULL;

UPDATE media_assets
SET drive_file_id = json_extract(metadata_json, '$.drive_file_id')
WHERE source = 'image'
  AND drive_file_id IS NULL
  AND json_extract(metadata_json, '$.drive_file_id') IS NOT NULL;

UPDATE media_assets
SET status = json_extract(metadata_json, '$.status')
WHERE source = 'image'
  AND status = 'ready'
  AND json_extract(metadata_json, '$.status') IS NOT NULL;

UPDATE media_assets
SET media_type = 'clip'
WHERE source IN ('youtube', 'artlist', 'stock')
  AND media_type = '';

UPDATE media_assets
SET media_type = 'image'
WHERE source = 'image'
  AND media_type = '';

UPDATE media_assets
SET media_type = 'voiceover'
WHERE source = 'voiceover'
  AND media_type = '';

-- Backfill: updated_at da created_at dove presente
UPDATE media_assets
SET updated_at = created_at
WHERE updated_at IS NULL;

-- Indici per ricerca su campi nuovi
CREATE INDEX IF NOT EXISTS idx_media_status ON media_assets(status);
CREATE INDEX IF NOT EXISTS idx_media_file_hash ON media_assets(file_hash);
CREATE INDEX IF NOT EXISTS idx_media_drive_file ON media_assets(drive_file_id);
CREATE INDEX IF NOT EXISTS idx_media_local_path ON media_assets(local_path);
CREATE INDEX IF NOT EXISTS idx_media_media_type ON media_assets(media_type);
CREATE INDEX IF NOT EXISTS idx_media_relative_path ON media_assets(relative_path);
