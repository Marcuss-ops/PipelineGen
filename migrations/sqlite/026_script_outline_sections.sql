-- 026_script_outline_sections.sql
-- Tracks outline sections as intermediate steps for debugging and reconstruction.

CREATE TABLE IF NOT EXISTS script_outline_sections (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    script_id INTEGER NOT NULL,
    section_index INTEGER NOT NULL DEFAULT 0,
    title TEXT NOT NULL DEFAULT '',
    purpose TEXT NOT NULL DEFAULT '',
    target_words INTEGER NOT NULL DEFAULT 0,
    key_points_json TEXT NOT NULL DEFAULT '[]',
    created_at TEXT NOT NULL DEFAULT (datetime('now')),
    FOREIGN KEY (script_id) REFERENCES scripts(id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_outline_sections_script ON script_outline_sections(script_id);
CREATE INDEX IF NOT EXISTS idx_outline_sections_index ON script_outline_sections(script_id, section_index);
