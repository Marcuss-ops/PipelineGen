-- database: primary
-- Migration 297: retire the last vector-index column names inherited from the
-- removed engine. The values are unchanged; only the storage names move to the
-- canonical PostgreSQL/vector terminology used by the Go contracts.
ALTER TABLE registry_runs RENAME COLUMN qdrant_points_before TO vector_points_before;
ALTER TABLE registry_runs RENAME COLUMN qdrant_points_after TO vector_points_after;
ALTER TABLE projection_registry RENAME COLUMN qdrant_version TO vector_index_version;
ALTER TABLE backup_registry RENAME COLUMN qdrant_version TO vector_index_version;

ALTER TABLE clip_storage_index RENAME COLUMN has_qdrant TO has_vector;
ALTER TABLE clip_storage_index RENAME COLUMN qdrant_point_id TO vector_point_id;
DROP INDEX IF EXISTS ix_clip_storage_index_qdrant_missing;
CREATE INDEX IF NOT EXISTS ix_clip_storage_index_vector_missing
    ON clip_storage_index(has_vector, has_db)
    WHERE has_vector = 0 AND has_db = 1;

-- The baseline was generated from both database scopes, so a primary database
-- can carry the quarantined observability copies too. They are recovery-only
-- shells with no reader or writer; drop them where they exist.
DROP TABLE IF EXISTS legacy_observability_qdrant_cleanup_audit;
DROP TABLE IF EXISTS legacy_observability_qdrant_collections;
DROP TABLE IF EXISTS legacy_observability_qdrantprojection_checkpoints;
DROP TABLE IF EXISTS legacy_observability_qdrantprojection_dlq;
