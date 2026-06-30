-- Migration 103: Create voiceovers table for AI voiceover storage & tracking
CREATE TABLE IF NOT EXISTS voiceovers (
    id TEXT PRIMARY KEY,
    request_id TEXT NOT NULL DEFAULT '',
    text_hash TEXT NOT NULL DEFAULT '',
    text_preview TEXT NOT NULL DEFAULT '',
    language TEXT NOT NULL DEFAULT 'it',
    voice TEXT NOT NULL DEFAULT '',
    filename TEXT NOT NULL DEFAULT '',
    local_path TEXT NOT NULL DEFAULT '',
    cleaned_path TEXT NOT NULL DEFAULT '',
    folder_id TEXT NOT NULL DEFAULT '',
    folder_path TEXT NOT NULL DEFAULT '',
    drive_file_id TEXT NOT NULL DEFAULT '',
    drive_link TEXT NOT NULL DEFAULT '',
    download_link TEXT NOT NULL DEFAULT '',
    file_hash TEXT NOT NULL DEFAULT '',
    fingerprint TEXT NOT NULL DEFAULT '',
    duration_seconds REAL NOT NULL DEFAULT 0.0,
    status TEXT NOT NULL DEFAULT 'pending',
    error TEXT NOT NULL DEFAULT '',
    strategy TEXT NOT NULL DEFAULT '',
    metadata TEXT NOT NULL DEFAULT '{}',
    created_at TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE INDEX IF NOT EXISTS idx_voiceovers_request_id ON voiceovers(request_id);
CREATE INDEX IF NOT EXISTS idx_voiceovers_text_hash ON voiceovers(text_hash);
CREATE INDEX IF NOT EXISTS idx_voiceovers_folder_id ON voiceovers(folder_id);
CREATE INDEX IF NOT EXISTS idx_voiceovers_fingerprint ON voiceovers(fingerprint);
