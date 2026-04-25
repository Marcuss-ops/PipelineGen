-- 0001_initial_scripts.sql
-- Initial schema for script history storage
-- Separate from stock.db.sqlite for clean separation of concerns

CREATE TABLE IF NOT EXISTS scripts (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    topic TEXT NOT NULL,
    duration INTEGER NOT NULL DEFAULT 60,
    language TEXT NOT NULL DEFAULT 'en',
    template TEXT NOT NULL DEFAULT 'documentary',
    mode TEXT NOT NULL DEFAULT 'modular',
    narrative_text TEXT,
    timeline_json TEXT,
    entities_json TEXT,
    metadata_json TEXT,
    full_document TEXT NOT NULL,
    model_used TEXT,
    ollama_base_url TEXT,
    created_at TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at TEXT NOT NULL DEFAULT (datetime('now')),
    version INTEGER NOT NULL DEFAULT 1,
    parent_script_id INTEGER REFERENCES scripts(id) ON DELETE SET NULL,
    is_deleted BOOLEAN NOT NULL DEFAULT 0
);

CREATE INDEX IF NOT EXISTS idx_scripts_topic ON scripts (topic);
CREATE INDEX IF NOT EXISTS idx_scripts_created_at ON scripts (created_at DESC);
CREATE INDEX IF NOT EXISTS idx_scripts_language ON scripts (language);
CREATE INDEX IF NOT EXISTS idx_scripts_template ON scripts (template);
CREATE INDEX IF NOT EXISTS idx_scripts_parent ON scripts (parent_script_id);

CREATE TABLE IF NOT EXISTS script_sections (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    script_id INTEGER NOT NULL REFERENCES scripts(id) ON DELETE CASCADE,
    section_type TEXT NOT NULL,
    section_title TEXT,
    content TEXT,
    sort_order INTEGER NOT NULL DEFAULT 0,
    created_at TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE INDEX IF NOT EXISTS idx_script_sections_script_id ON script_sections (script_id, sort_order);

CREATE TABLE IF NOT EXISTS script_stock_matches (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    script_id INTEGER NOT NULL REFERENCES scripts(id) ON DELETE CASCADE,
    segment_index INTEGER NOT NULL DEFAULT 0,
    stock_path TEXT,
    stock_source TEXT,
    score REAL,
    matched_terms TEXT,
    created_at TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE INDEX IF NOT EXISTS idx_script_stock_matches_script_id ON script_stock_matches (script_id, segment_index);
