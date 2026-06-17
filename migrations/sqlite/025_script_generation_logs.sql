-- 025_script_generation_logs.sql
-- Tracks each phase of script generation for debugging and observability.

CREATE TABLE IF NOT EXISTS script_generation_logs (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    script_id INTEGER NOT NULL,
    phase TEXT NOT NULL DEFAULT '',
    prompt_hash TEXT NOT NULL DEFAULT '',
    model TEXT NOT NULL DEFAULT '',
    input_words INTEGER DEFAULT 0,
    output_words INTEGER DEFAULT 0,
    duration_ms INTEGER DEFAULT 0,
    retry_count INTEGER DEFAULT 0,
    cache_status TEXT DEFAULT 'miss',
    error TEXT DEFAULT '',
    created_at TEXT NOT NULL DEFAULT (datetime('now')),
    FOREIGN KEY (script_id) REFERENCES scripts(id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_gen_logs_script ON script_generation_logs(script_id);
CREATE INDEX IF NOT EXISTS idx_gen_logs_phase ON script_generation_logs(script_id, phase);
