-- database: primary
-- 226_entity_image_catalog_quality.sql
-- Persist the quality gate decision independently from URL lifecycle state.

ALTER TABLE entity_image_catalog_candidates
    ADD COLUMN semantic_status TEXT NOT NULL DEFAULT 'unknown'
        CHECK (semantic_status IN ('unknown', 'accepted', 'rejected'));
ALTER TABLE entity_image_catalog_candidates
    ADD COLUMN semantic_score REAL NOT NULL DEFAULT 0
        CHECK (semantic_score >= 0 AND semantic_score <= 1);
ALTER TABLE entity_image_catalog_candidates
    ADD COLUMN technical_score REAL NOT NULL DEFAULT 0
        CHECK (technical_score >= 0 AND technical_score <= 1);
ALTER TABLE entity_image_catalog_candidates
    ADD COLUMN quality_reason TEXT NOT NULL DEFAULT '';

CREATE INDEX IF NOT EXISTS idx_entity_image_catalog_candidates_quality
    ON entity_image_catalog_candidates(canonical_entity_id, semantic_status, technical_score, rank);
