-- 001_create_voiceovers_table.sql
-- Voiceovers table for voiceover processing

CREATE TABLE IF NOT EXISTS voiceovers (
    id TEXT PRIMARY KEY,
    script_id INTEGER,
    voice TEXT NOT NULL DEFAULT '',
    model TEXT NOT NULL DEFAULT '',
    language TEXT NOT NULL DEFAULT 'en',
    speed REAL NOT NULL DEFAULT 1.0,
    stability REAL NOT NULL DEFAULT 0.5,
    similarity_boost REAL NOT NULL DEFAULT 0.75,
    style REAL NOT NULL DEFAULT 0.0,
    audio_path TEXT NOT NULL DEFAULT '',
    audio_url TEXT NOT NULL DEFAULT '',
    duration_secs REAL DEFAULT 0,
    file_size_bytes INTEGER DEFAULT 0,
    status TEXT NOT NULL DEFAULT 'pending',
    error TEXT NOT NULL DEFAULT '',
    metadata_json TEXT NOT NULL DEFAULT '{}',
    created_at TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at TEXT NOT NULL DEFAULT (datetime('now')),
    FOREIGN KEY (script_id) REFERENCES scripts(id) ON DELETE SET NULL
);

CREATE INDEX IF NOT EXISTS idx_voiceovers_script ON voiceovers(script_id);
CREATE INDEX IF NOT EXISTS idx_voiceovers_voice ON voiceovers(voice);
CREATE INDEX IF NOT EXISTS idx_voiceovers_language ON voiceovers(language);
CREATE INDEX IF NOT EXISTS idx_voiceovers_status ON voiceovers(status);
CREATE INDEX IF NOT EXISTS idx_voiceovers_created ON voiceovers(created_at DESC);
