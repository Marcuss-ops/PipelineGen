-- 0001_initial.sql
-- Core tables for script storage.

CREATE TABLE IF NOT EXISTS scripts (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    topic TEXT NOT NULL,
    duration INTEGER NOT NULL DEFAULT 0,
    language TEXT NOT NULL DEFAULT 'it',
    template TEXT NOT NULL DEFAULT '',
    mode TEXT NOT NULL DEFAULT '',
    narrative_text TEXT NOT NULL DEFAULT '',
    timeline_json TEXT NOT NULL DEFAULT '',
    entities_json TEXT NOT NULL DEFAULT '',
    metadata_json TEXT NOT NULL DEFAULT '',
    full_document TEXT NOT NULL DEFAULT '',
    model_used TEXT NOT NULL DEFAULT '',
    ollama_base_url TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at TEXT NOT NULL DEFAULT (datetime('now')),
    version INTEGER NOT NULL DEFAULT 1,
    parent_script_id INTEGER,
    is_deleted BOOLEAN NOT NULL DEFAULT 0
);

CREATE INDEX IF NOT EXISTS idx_scripts_created_at ON scripts(created_at);
CREATE INDEX IF NOT EXISTS idx_scripts_topic ON scripts(topic);
CREATE INDEX IF NOT EXISTS idx_scripts_parent_script_id ON scripts(parent_script_id);
CREATE INDEX IF NOT EXISTS idx_scripts_is_deleted ON scripts(is_deleted);

CREATE TABLE IF NOT EXISTS script_sections (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    script_id INTEGER NOT NULL,
    section_type TEXT NOT NULL DEFAULT '',
    section_title TEXT NOT NULL DEFAULT '',
    content TEXT NOT NULL DEFAULT '',
    sort_order INTEGER NOT NULL DEFAULT 0,
    created_at TEXT NOT NULL DEFAULT (datetime('now')),
    FOREIGN KEY (script_id) REFERENCES scripts(id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_script_sections_script_id ON script_sections(script_id);
CREATE INDEX IF NOT EXISTS idx_script_sections_sort_order ON script_sections(script_id, sort_order);

CREATE TABLE IF NOT EXISTS script_stock_matches (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    script_id INTEGER NOT NULL,
    segment_index INTEGER NOT NULL DEFAULT 0,
    stock_path TEXT NOT NULL DEFAULT '',
    stock_source TEXT NOT NULL DEFAULT '',
    score REAL NOT NULL DEFAULT 0,
    matched_terms TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL DEFAULT (datetime('now')),
    FOREIGN KEY (script_id) REFERENCES scripts(id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_script_stock_matches_script_id ON script_stock_matches(script_id);
CREATE INDEX IF NOT EXISTS idx_script_stock_matches_score ON script_stock_matches(script_id, score DESC);
