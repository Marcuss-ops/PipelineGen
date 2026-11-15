-- database: primary
-- 294_entity_image_catalog_retention.sql
-- Retention scan for rejected/retired PERSON image candidates.
-- The policy is documented in docs/operations/entity-image-catalog-retention.md.
-- Active candidates and every recent rejection stay readable; only rows older
-- than the cutoff are removed by the canonical maintenance command.

CREATE INDEX IF NOT EXISTS idx_entity_image_catalog_candidates_retention
    ON entity_image_catalog_candidates(updated_at, status, semantic_status);
