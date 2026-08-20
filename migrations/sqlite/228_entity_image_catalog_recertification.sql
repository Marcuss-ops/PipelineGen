-- database: primary
-- 228_entity_image_catalog_recertification.sql
-- Persist bounded remote-validation retry state separately from URL lifecycle.

ALTER TABLE entity_image_catalog_candidates
    ADD COLUMN validation_attempts INTEGER NOT NULL DEFAULT 0
        CHECK (validation_attempts >= 0);
ALTER TABLE entity_image_catalog_candidates
    ADD COLUMN last_validation_at TEXT NOT NULL DEFAULT '';
ALTER TABLE entity_image_catalog_candidates
    ADD COLUMN next_retry_at TEXT NOT NULL DEFAULT '';
ALTER TABLE entity_image_catalog_candidates
    ADD COLUMN last_validation_error TEXT NOT NULL DEFAULT '';

CREATE INDEX IF NOT EXISTS idx_entity_image_catalog_candidates_recertification
    ON entity_image_catalog_candidates(status, next_retry_at, last_seen_at, validation_attempts);
