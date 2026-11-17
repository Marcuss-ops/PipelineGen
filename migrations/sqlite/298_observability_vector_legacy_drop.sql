-- database: observability
-- Migration 298: the observability database keeps only telemetry. The four
-- quarantined copies of the removed vector registry held no rows and no
-- readers/writers; drop them and align the quarantined clip table names with
-- the canonical vector terminology.
--
-- The status view was NOT renamed by the 267 quarantine (only its base table
-- was), so it still points at legacy_observability_qdrant_collections. Drop it
-- first: SQLite refuses the base-table DROP while a view references it and the
-- invalid view would abort the following ALTER with "error in view ...".
DROP VIEW IF EXISTS qdrant_collections_status_v1;
DROP TABLE IF EXISTS legacy_observability_qdrant_cleanup_audit;
DROP TABLE IF EXISTS legacy_observability_qdrant_collections;
DROP TABLE IF EXISTS legacy_observability_qdrantprojection_checkpoints;
DROP TABLE IF EXISTS legacy_observability_qdrantprojection_dlq;

ALTER TABLE legacy_observability_clip_storage_index RENAME COLUMN has_qdrant TO has_vector;
ALTER TABLE legacy_observability_clip_storage_index RENAME COLUMN qdrant_point_id TO vector_point_id;
DROP INDEX IF EXISTS ix_clip_storage_index_qdrant_missing;
CREATE INDEX IF NOT EXISTS ix_legacy_observability_clip_storage_index_vector_missing
    ON legacy_observability_clip_storage_index(has_vector, has_db)
    WHERE has_vector = 0 AND has_db = 1;
