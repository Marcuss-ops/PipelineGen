-- Aggiunge UNIQUE constraint su id per permettere ON CONFLICT(id) DO UPDATE SET
-- nelle operazioni UpsertClip/AddImage.
-- La tabella originale aveva id TEXT PRIMARY KEY ma in alcuni database
-- esistenti il vincolo PRIMARY KEY è stato perso durante migration successive.
CREATE UNIQUE INDEX IF NOT EXISTS idx_media_assets_id ON media_assets(id);
