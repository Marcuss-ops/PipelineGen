#!/bin/bash
# scripts/init_media_db.sh
# Inizializza il database media da zero per supportare la nuova struttura di archiviazione semantica.

DB_PATH="data/media.db.sqlite"

echo "Initializing media database at $DB_PATH..."

# Backup esistente se presente
if [ -f "$DB_PATH" ]; then
    echo "Backing up existing database to ${DB_PATH}.bak"
    cp "$DB_PATH" "${DB_PATH}.bak"
fi

# Rimuovi il database esistente per una pulizia completa
# (Oppure potremmo solo svuotare le tabelle)
# rm -f "$DB_PATH"

# Esegui le migrazioni
echo "Running migrations..."
sqlite3 "$DB_PATH" <<EOF
DROP TABLE IF EXISTS media_assets;
DROP TABLE IF EXISTS subjects;
DROP TABLE IF EXISTS monitored_sources;
DROP TABLE IF EXISTS clip_folders;
DROP TABLE IF EXISTS clip_manifests;

-- Re-create tables (simplified for this script, better use migrate tool if available)
CREATE TABLE media_assets (
    id TEXT PRIMARY KEY,
    source TEXT NOT NULL,
    name TEXT NOT NULL,
    tags TEXT,
    tags_norm TEXT,
    embedding_json TEXT,
    duration_ms INTEGER,
    url TEXT,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    metadata_json TEXT,
    media_type TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL DEFAULT 'ready',
    local_path TEXT,
    relative_path TEXT,
    drive_file_id TEXT,
    drive_folder_id TEXT,
    drive_link TEXT,
    download_link TEXT,
    file_hash TEXT,
    content_hash TEXT,
    width INTEGER DEFAULT 0,
    height INTEGER DEFAULT 0,
    updated_at TEXT,
    phash TEXT,
    visual_embedding_json TEXT
);

CREATE INDEX idx_media_source ON media_assets(source);
CREATE INDEX idx_media_tags ON media_assets(tags_norm);
CREATE INDEX idx_media_status ON media_assets(status);
CREATE INDEX idx_media_file_hash ON media_assets(file_hash);
CREATE INDEX idx_media_drive_file ON media_assets(drive_file_id);
CREATE INDEX idx_media_local_path ON media_assets(local_path);
CREATE INDEX idx_media_media_type ON media_assets(media_type);
CREATE INDEX idx_media_relative_path ON media_assets(relative_path);

CREATE TABLE subjects (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL DEFAULT '',
    description TEXT,
    metadata_json TEXT NOT NULL DEFAULT '{}',
    created_at TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE TABLE monitored_sources (
    id TEXT PRIMARY KEY,
    source TEXT NOT NULL,
    external_id TEXT,
    external_url TEXT,
    group_name TEXT,
    category TEXT,
    status TEXT,
    last_seen_at TEXT,
    created_at TEXT,
    updated_at TEXT,
    metadata_json TEXT,
    processed_count INTEGER DEFAULT 0
);

CREATE TABLE clip_folders (
    id TEXT PRIMARY KEY,
    source TEXT NOT NULL,
    source_url TEXT,
    video_id TEXT,
    folder_id TEXT,
    folder_path TEXT,
    local_folder_path TEXT,
    "group" TEXT,
    manifest_txt_path TEXT,
    manifest_json_path TEXT,
    clip_count INTEGER DEFAULT 0,
    processed_count INTEGER DEFAULT 0,
    failed_count INTEGER DEFAULT 0,
    skipped_count INTEGER DEFAULT 0,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
);
EOF

echo "Media database initialized successfully."
echo "NOTE: All existing media records have been cleared. Local files on disk are still present but no longer indexed."
