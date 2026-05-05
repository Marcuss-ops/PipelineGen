-- 001_create_scripts_table.sql
-- Correct schema for scripts table matching ScriptRecord struct

-- Drop existing table (WARNING: data loss)
DROP TABLE IF EXISTS script_sections;
DROP TABLE IF EXISTS script_stock_matches;
DROP TABLE IF EXISTS scripts;

-- Create scripts table with correct schema
CREATE TABLE IF NOT EXISTS scripts (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    topic TEXT NOT NULL DEFAULT '',
    duration INTEGER NOT NULL DEFAULT 0,
    language TEXT NOT NULL DEFAULT 'en',
    template TEXT NOT NULL DEFAULT '',
    mode TEXT NOT NULL DEFAULT '',
    narrative_text TEXT,
    timeline_json TEXT,
    entities_json TEXT,
    metadata_json TEXT NOT NULL DEFAULT '{}',
    full_document TEXT,
    model_used TEXT NOT NULL DEFAULT '',
    ollama_base_url TEXT NOT NULL DEFAULT '',
    version INTEGER NOT NULL DEFAULT 1,
    parent_script_id INTEGER,
    is_deleted INTEGER NOT NULL DEFAULT 0,
    created_at TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE INDEX IF NOT EXISTS idx_scripts_topic ON scripts(topic);
CREATE INDEX IF NOT EXISTS idx_scripts_language ON scripts(language);
CREATE INDEX IF NOT EXISTS idx_scripts_template ON scripts(template);
CREATE INDEX IF NOT EXISTS idx_scripts_version ON scripts(version);
CREATE INDEX IF NOT EXISTS idx_scripts_parent ON scripts(parent_script_id);
CREATE INDEX IF NOT EXISTS idx_scripts_deleted ON scripts(is_deleted);
CREATE INDEX IF NOT EXISTS idx_scripts_created ON scripts(created_at DESC);

-- Create script_sections table
CREATE TABLE IF NOT EXISTS script_sections (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    script_id INTEGER NOT NULL,
    section_type TEXT NOT NULL DEFAULT '',
    section_title TEXT NOT NULL DEFAULT '',
    content TEXT,
    sort_order INTEGER NOT NULL DEFAULT 0,
    FOREIGN KEY (script_id) REFERENCES scripts(id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_script_sections_script ON script_sections(script_id);
CREATE INDEX IF NOT EXISTS idx_script_sections_order ON script_sections(script_id, sort_order);

-- Create script_stock_matches table with correct schema
CREATE TABLE IF NOT EXISTS script_stock_matches (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    script_id INTEGER NOT NULL,
    segment_index INTEGER NOT NULL DEFAULT 0,
    stock_path TEXT NOT NULL DEFAULT '',
    stock_source TEXT NOT NULL DEFAULT '',
    score REAL NOT NULL DEFAULT 0,
    matched_terms TEXT NOT NULL DEFAULT '',
    FOREIGN KEY (script_id) REFERENCES scripts(id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_script_stock_matches_script ON script_stock_matches(script_id);
CREATE INDEX IF NOT EXISTS idx_script_stock_matches_segment ON script_stock_matches(script_id, segment_index);
