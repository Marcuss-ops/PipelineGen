-- clips_003_create_segment_embeddings.sql
-- Tabella per cache embedding dei segmenti script

CREATE TABLE IF NOT EXISTS segment_embeddings (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    script_key TEXT NOT NULL,
    source_hash TEXT NOT NULL DEFAULT '',
    topic TEXT NOT NULL DEFAULT '',
    language TEXT NOT NULL DEFAULT '',
    template TEXT NOT NULL DEFAULT '',
    duration INTEGER NOT NULL DEFAULT 0,
    segment_index INTEGER NOT NULL,
    raw_subject TEXT NOT NULL DEFAULT '',
    canonical_subject TEXT NOT NULL DEFAULT '',
    raw_keywords_json TEXT NOT NULL DEFAULT '[]',
    canonical_keywords_json TEXT NOT NULL DEFAULT '[]',
    raw_entities_json TEXT NOT NULL DEFAULT '[]',
    canonical_entities_json TEXT NOT NULL DEFAULT '[]',
    segment_json TEXT NOT NULL DEFAULT '{}',
    embedding_json TEXT NOT NULL DEFAULT '[]',
    best_source TEXT NOT NULL DEFAULT '',
    best_path TEXT NOT NULL DEFAULT '',
    best_link TEXT NOT NULL DEFAULT '',
    best_score INTEGER NOT NULL DEFAULT 0,
    created_at TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at TEXT NOT NULL DEFAULT (datetime('now')),
    UNIQUE(script_key, segment_index)
);

-- Indice univoco per evitare duplicati
CREATE UNIQUE INDEX IF NOT EXISTS idx_segment_embeddings_script_segment
ON segment_embeddings(script_key, segment_index);

-- Indici per query frequenti
CREATE INDEX IF NOT EXISTS idx_segment_embeddings_script_key
ON segment_embeddings(script_key);

CREATE INDEX IF NOT EXISTS idx_segment_embeddings_topic
ON segment_embeddings(topic);

CREATE INDEX IF NOT EXISTS idx_segment_embeddings_canonical_subject
ON segment_embeddings(canonical_subject);
