CREATE TABLE IF NOT EXISTS voiceovers (
    id TEXT PRIMARY KEY,
    request_id TEXT NOT NULL,
    text_hash TEXT NOT NULL,
    text_preview TEXT NOT NULL,
    language TEXT NOT NULL,
    voice TEXT,
    filename TEXT NOT NULL,
    local_path TEXT,
    cleaned_path TEXT,
    folder_id TEXT,
    folder_path TEXT,
    drive_file_id TEXT,
    drive_link TEXT,
    download_link TEXT,
    file_hash TEXT,
    duration_seconds REAL DEFAULT 0,
    status TEXT NOT NULL DEFAULT 'created',
    error TEXT,
    strategy TEXT DEFAULT 'verify',
    metadata TEXT,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_voiceovers_text_lang
ON voiceovers(text_hash, language);

CREATE INDEX IF NOT EXISTS idx_voiceovers_status
ON voiceovers(status);

CREATE INDEX IF NOT EXISTS idx_voiceovers_drive
ON voiceovers(drive_file_id);

CREATE UNIQUE INDEX IF NOT EXISTS idx_voiceovers_dedupe
ON voiceovers(text_hash, language, voice, folder_id);
