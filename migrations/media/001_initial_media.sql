-- Script di creazione per il nuovo database media.db.sqlite
-- Contiene tutti gli asset unificati (YouTube, Artlist, Stock, Immagini)

CREATE TABLE IF NOT EXISTS media_assets (
    id TEXT PRIMARY KEY,
    source TEXT NOT NULL, -- 'youtube','artlist','stock','image', 'voiceover'
    name TEXT NOT NULL,
    tags TEXT, -- per ricerca lineare
    tags_norm TEXT, -- lowercase senza accenti
    embedding_json TEXT, -- vettore normalizzato 384d
    duration_ms INTEGER,
    url TEXT, -- link webview o download
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    metadata_json TEXT -- JSON flessibile per campi specifici (folder_id, drive_file_id, bpm, etc.)
);

CREATE INDEX IF NOT EXISTS idx_media_source ON media_assets(source);
CREATE INDEX IF NOT EXISTS idx_media_tags ON media_assets(tags_norm);

-- Tabella subjects per le immagini
CREATE TABLE IF NOT EXISTS subjects (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL DEFAULT '',
    description TEXT,
    metadata_json TEXT NOT NULL DEFAULT '{}',
    created_at TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at TEXT NOT NULL DEFAULT (datetime('now'))
);
