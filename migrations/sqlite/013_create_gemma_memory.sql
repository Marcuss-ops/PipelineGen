-- 013_create_gemma_memory.sql
-- Gemma Memory Gate: exact cache, reusable memories, and script chunks for similarity search

-- Level 1: Exact cache — full outputs keyed by input hash
CREATE TABLE IF NOT EXISTS gemma_script_outputs (
    id TEXT PRIMARY KEY,
    channel_id TEXT NOT NULL DEFAULT 'default',
    mode TEXT NOT NULL DEFAULT 'generate',
    language TEXT DEFAULT 'en',
    title TEXT,
    prompt TEXT NOT NULL,
    normalized_input TEXT NOT NULL,
    input_hash TEXT NOT NULL,
    output_text TEXT,
    output_json TEXT,
    model TEXT,
    job_id TEXT,
    word_count INTEGER DEFAULT 0,
    created_at TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at TEXT NOT NULL DEFAULT (datetime('now')),
    UNIQUE(channel_id, mode, input_hash)
);

CREATE INDEX IF NOT EXISTS idx_gemma_outputs_hash ON gemma_script_outputs(channel_id, mode, input_hash);
CREATE INDEX IF NOT EXISTS idx_gemma_outputs_channel ON gemma_script_outputs(channel_id, mode);

-- Level 2+3: Reusable memories — channel rules, topic research, hooks, structures, profiles
CREATE TABLE IF NOT EXISTS gemma_memory_entries (
    id TEXT PRIMARY KEY,
    channel_id TEXT NOT NULL DEFAULT 'default',
    memory_type TEXT NOT NULL,
    topic_key TEXT,
    title TEXT,
    summary TEXT NOT NULL,
    content_text TEXT,
    content_json TEXT,
    source_generation_id TEXT,
    source_job_id TEXT,
    usefulness_score REAL DEFAULT 1.0,
    created_at TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE INDEX IF NOT EXISTS idx_gemma_memory_channel ON gemma_memory_entries(channel_id, memory_type);
CREATE INDEX IF NOT EXISTS idx_gemma_memory_topic ON gemma_memory_entries(channel_id, topic_key);
CREATE INDEX IF NOT EXISTS idx_gemma_memory_type ON gemma_memory_entries(memory_type);

-- Level 2: Script chunks for similarity search via LIKE on normalized search_text
CREATE TABLE IF NOT EXISTS gemma_script_chunks (
    id TEXT PRIMARY KEY,
    generation_id TEXT NOT NULL,
    channel_id TEXT NOT NULL DEFAULT 'default',
    chunk_index INTEGER NOT NULL,
    chunk_type TEXT DEFAULT 'paragraph',
    topic_key TEXT,
    title TEXT,
    text TEXT NOT NULL,
    search_text TEXT NOT NULL,
    embedding_json TEXT,
    created_at TEXT NOT NULL DEFAULT (datetime('now')),
    FOREIGN KEY(generation_id) REFERENCES gemma_script_outputs(id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_gemma_chunks_channel ON gemma_script_chunks(channel_id, topic_key);
CREATE INDEX IF NOT EXISTS idx_gemma_chunks_search ON gemma_script_chunks(channel_id, search_text);
