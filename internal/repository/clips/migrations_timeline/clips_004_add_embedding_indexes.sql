-- clips_004_add_embedding_indexes.sql
-- Additional performance indexes for segment_embeddings table

CREATE INDEX IF NOT EXISTS idx_segment_embeddings_language
ON segment_embeddings(language);

CREATE INDEX IF NOT EXISTS idx_segment_embeddings_template
ON segment_embeddings(template);

CREATE INDEX IF NOT EXISTS idx_segment_embeddings_duration
ON segment_embeddings(duration)
WHERE duration > 0;

CREATE INDEX IF NOT EXISTS idx_segment_embeddings_best_source
ON segment_embeddings(best_source)
WHERE best_source != '';

CREATE INDEX IF NOT EXISTS idx_segment_embeddings_best_score
ON segment_embeddings(best_score DESC)
WHERE best_score > 0;

CREATE INDEX IF NOT EXISTS idx_segment_embeddings_created_at
ON segment_embeddings(created_at DESC);

CREATE INDEX IF NOT EXISTS idx_segment_embeddings_updated_at
ON segment_embeddings(updated_at DESC);

-- Index for searching by source_hash (for deduplication)
CREATE INDEX IF NOT EXISTS idx_segment_embeddings_source_hash
ON segment_embeddings(source_hash)
WHERE source_hash != '';
