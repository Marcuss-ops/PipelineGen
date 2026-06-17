-- 011_create_characters.sql
-- Create characters table for AI Avatar registry

CREATE TABLE IF NOT EXISTS characters (
    id TEXT PRIMARY KEY,          -- slug like 'alex'
    name TEXT NOT NULL,           -- display name 'Alex'
    image_drive_id TEXT,          -- Google Drive file ID for the PNG face
    image_drive_link TEXT,        -- Google Drive web link
    voice_id TEXT,                -- Optional: preferred voice for this character
    metadata_json TEXT DEFAULT '{}',
    created_at TEXT DEFAULT (datetime('now')),
    updated_at TEXT DEFAULT (datetime('now'))
);

-- Index for faster lookup by name if needed
CREATE INDEX IF NOT EXISTS idx_characters_name ON characters(name);
