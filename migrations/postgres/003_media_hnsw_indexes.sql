-- 003_media_hnsw_indexes.sql
-- PostgreSQL + pgvector media domain — PRODUCTION ANN indexes.
-- Apply after 001_media_schema.sql and 002_media_vector_surfaces.sql,
-- only to the dedicated media database.
--
-- POSTGRES-MEDIA-CUTOVER acceptance gates:
--   SEMANTIC_HNSW_INDEX = TRUE
--   VISUAL_HNSW_INDEX   = TRUE
--
-- Why per-family partial indexes: media_embeddings stores every
-- (embedding_type, model_id) family in one table with an UNtyped vector
-- column (002 keeps dimension identity in the fail-closed family registry,
-- not in the column type). pgvector can only use an HNSW index when the
-- ORDER BY operand is CAST to a concrete vector(N) and the planner can
-- prove the partial predicate matches the query — so each production
-- family gets its own partial index on the cast expression, restricted to
-- exactly that family's rows.
--
-- Canonical production families (godlike/06 SSOT, kernel/models registry):
--   semantic: intfloat/multilingual-e5-base — 768 dims, cosine, normalized
--   visual:   google/siglip-so400m-patch14-384 — 768 dims, cosine, normalized
--
-- Both families are also registered in media_embedding_families below so
-- the 002 validation trigger accepts the vectors BEFORE the indexes exist
-- (index creation is an optimization; the family gate is correctness).
-- The family registration is idempotent (ON CONFLICT DO NOTHING): the dim
-- of the canonical models is fixed by the model registry, and a drift is a
-- fail-closed error raised by the 002 trigger, never a silent overwrite.
--
-- Operator note (CONCURRENTLY): this migration is applied by the canonical
-- self-bootstrapping runner inside a transaction block, where CREATE INDEX
-- CONCURRENTLY is not allowed. On an EMPTY database (fresh boot) the plain
-- CREATE INDEX below is instant. On a LARGE populated database an operator
-- may run the CONCURRENTLY variants manually (commented at the bottom);
-- the plain indexes stay valid regardless and CONCURRENTLY builds replace
-- them one-for-one.
--
-- Query plan acceptance (PGVECTOR_ANN_INDEX certification): a vector
-- search ORDER BY embedding <=> $query LIMIT k on a family-matching
-- predicate must plan an Index Scan using these HNSW indexes — never a
-- Seq Scan. Enforced by TestHNSW_VectorSearchPlansIndexScan (live DSN).

-- ── Production family registration (fail-closed gate unlock) ────────────
INSERT INTO media_embedding_families (embedding_type, model_id, dim, created_at)
VALUES
    ('text',   'intfloat/multilingual-e5-base',    768, ''),
    ('visual', 'google/siglip-so400m-patch14-384', 768, '')
ON CONFLICT (embedding_type, model_id) DO NOTHING;

-- ── SEMANTIC channel HNSW (text embeddings, E5 768d cosine) ─────────────
CREATE INDEX IF NOT EXISTS idx_media_embeddings_text_hnsw
    ON media_embeddings
    USING hnsw ((embedding::vector(768)) vector_cosine_ops)
    WHERE embedding_type = 'text'
      AND model_id = 'intfloat/multilingual-e5-base';

-- ── VISUAL channel HNSW (SigLIP so400m patch14-384, 768d cosine) ────────
CREATE INDEX IF NOT EXISTS idx_media_embeddings_visual_hnsw
    ON media_embeddings
    USING hnsw ((embedding::vector(768)) vector_cosine_ops)
    WHERE embedding_type = 'visual'
      AND model_id = 'google/siglip-so400m-patch14-384';

-- ── Optional operator path for large populated databases ────────────────
-- Run each statement OUTSIDE a transaction block, one at a time:
--
-- CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_media_embeddings_text_hnsw
--     ON media_embeddings
--     USING hnsw ((embedding::vector(768)) vector_cosine_ops)
--     WHERE embedding_type = 'text'
--       AND model_id = 'intfloat/multilingual-e5-base';
--
-- CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_media_embeddings_visual_hnsw
--     ON media_embeddings
--     USING hnsw ((embedding::vector(768)) vector_cosine_ops)
--     WHERE embedding_type = 'visual'
--       AND model_id = 'google/siglip-so400m-patch14-384';
--
-- Selective hard filters + ANN (iterative scan) — set per session/tx by the
-- searcher when hard-filter recall matters more than strict ANN ordering:
--   SET LOCAL hnsw.iterative_scan = strict_order;
--   SET LOCAL hnsw.ef_search = 100;
