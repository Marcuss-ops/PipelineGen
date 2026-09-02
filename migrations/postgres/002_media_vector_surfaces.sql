-- 002_media_vector_surfaces.sql
-- PostgreSQL + pgvector media domain — derived / vector surfaces.
-- Apply after 001_media_schema.sql, only to the dedicated media database.
--
-- Separation of concerns (FASE 4 of the media cutover plan):
--   - 001 owns the transactional parity core (canonical committer writes).
--   - 002 owns DERIVED surfaces: hard visual features and pgvector
--     embeddings. They are rebuildable by the enrichment pipeline and are
--     never written by producers.
--
-- Embedding families (godlike/06 SSOT):
--   Vectors of incompatible models/dimensions must never share one HNSW
--   index. media_embedding_families is the fail-closed registry of allowed
--   (embedding_type, model_id, dim) triples; the trigger below rejects any
--   insert whose family is unregistered or whose dimension does not match.
--   Once the production model is selected, register the family and create
--   the typed HNSW index, e.g.:
--
--     INSERT INTO media_embedding_families (embedding_type, model_id, dim)
--     VALUES ('visual', '<model>', 768);
--     CREATE INDEX idx_media_embeddings_hnsw
--       ON media_embeddings USING hnsw (embedding vector_cosine_ops)
--       WHERE model_id = '<model>';
--
--   With selective hard filters, enable iterative scans per session:
--     SET LOCAL hnsw.iterative_scan = strict_order;
--     SET LOCAL hnsw.ef_search = 100;

-- ── media_asset_features ────────────────────────────────────────────────
-- Derived hard features written by PostgresAssetFeatureWriter. One row per
-- asset; largest_face_ratio distinguishes a close-up from a crowd.
CREATE TABLE IF NOT EXISTS media_asset_features (
    asset_id           TEXT PRIMARY KEY
                       REFERENCES media_assets(id) ON DELETE CASCADE,
    dominant_color     TEXT NOT NULL DEFAULT '',
    motion_score       REAL,
    has_faces          SMALLINT CHECK (has_faces IN (0, 1)),
    face_count         INTEGER,
    largest_face_ratio REAL,
    analyzed_at        TEXT NOT NULL DEFAULT '',
    analyzer_version   TEXT NOT NULL DEFAULT ''
);

CREATE INDEX IF NOT EXISTS idx_features_faces_motion
    ON media_asset_features (has_faces, motion_score);

-- ── media_embedding_families ────────────────────────────────────────────
-- Fail-closed registry of allowed embedding families. No producer may
-- insert an embedding whose family is not registered here.
CREATE TABLE IF NOT EXISTS media_embedding_families (
    embedding_type TEXT NOT NULL,
    model_id       TEXT NOT NULL,
    dim            INTEGER NOT NULL CHECK (dim > 0),
    created_at     TEXT NOT NULL DEFAULT '',
    PRIMARY KEY (embedding_type, model_id)
);

-- ── media_embeddings ────────────────────────────────────────────────────
-- pgvector surface. The vector column is intentionally NOT dimension-typed
-- here; dimension identity is enforced per-family by the trigger so that
-- registering a family is the single gate that unlocks a model's vectors.
CREATE TABLE IF NOT EXISTS media_embeddings (
    asset_id       TEXT NOT NULL
                   REFERENCES media_assets(id) ON DELETE CASCADE,
    embedding_type TEXT NOT NULL,
    model_id       TEXT NOT NULL,
    embedding      vector NOT NULL,
    created_at     TEXT NOT NULL DEFAULT '',
    PRIMARY KEY (asset_id, embedding_type, model_id)
);

CREATE INDEX IF NOT EXISTS idx_media_embeddings_asset
    ON media_embeddings (asset_id);

CREATE OR REPLACE FUNCTION media_embeddings_validate_family()
RETURNS trigger AS $$
DECLARE
    expected_dim INTEGER;
BEGIN
    SELECT dim INTO expected_dim
    FROM media_embedding_families
    WHERE model_id = NEW.model_id
      AND embedding_type = NEW.embedding_type;
    IF NOT FOUND THEN
        RAISE EXCEPTION
            'media_embeddings: unregistered embedding family (embedding_type=%, model_id=%)',
            NEW.embedding_type, NEW.model_id;
    END IF;
    IF vector_dims(NEW.embedding) <> expected_dim THEN
        RAISE EXCEPTION
            'media_embeddings: vector dim % does not match family dim % for model %',
            vector_dims(NEW.embedding), expected_dim, NEW.model_id;
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS trg_media_embeddings_family ON media_embeddings;
CREATE TRIGGER trg_media_embeddings_family
    BEFORE INSERT OR UPDATE ON media_embeddings
    FOR EACH ROW EXECUTE FUNCTION media_embeddings_validate_family();

-- ── Derived-index conveniences ──────────────────────────────────────────
-- Tags are stored as JSON TEXT (SQLite parity). The GIN expression index
-- mirrors the canonical idx_media_tags intent for containment queries.
CREATE INDEX IF NOT EXISTS idx_media_assets_tags_gin
    ON media_assets USING gin ((NULLIF(tags, '')::jsonb));

-- Lexical fallback surface for resolver fusion (title/search_text).
CREATE INDEX IF NOT EXISTS idx_media_assets_search_text_fts
    ON media_assets USING gin (to_tsvector('english', search_text));
