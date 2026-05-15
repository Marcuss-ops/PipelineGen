-- Migrazione per aggiungere il supporto ai modelli 3D di Sketchfab
-- Tabella specifica per i modelli Sketchfab

CREATE TABLE IF NOT EXISTS sketchfab_models (
    uid TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    user_name TEXT,
    license_type TEXT,
    thumb_url TEXT,
    view_url TEXT,
    download_url TEXT,
    download_expires_at DATETIME,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    metadata_json TEXT DEFAULT '{}'
);

CREATE INDEX IF NOT EXISTS idx_sketchfab_name ON sketchfab_models(name);
