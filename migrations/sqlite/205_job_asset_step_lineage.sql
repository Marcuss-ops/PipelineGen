-- database: primary
-- Migration 205: make job-asset edges step-aware.
-- Existing rows are retained with an empty step_id for legacy producers.
PRAGMA foreign_keys = OFF;

CREATE TABLE IF NOT EXISTS job_asset_relations_v205 (
    job_id TEXT NOT NULL,
    asset_id TEXT NOT NULL,
    relation TEXT NOT NULL,
    step_id TEXT NOT NULL DEFAULT '',
    ordinal INTEGER NOT NULL DEFAULT 0,
    created_at TEXT NOT NULL,
    PRIMARY KEY (job_id, asset_id, relation, step_id),
    FOREIGN KEY (job_id) REFERENCES jobs(id) ON DELETE CASCADE
);

INSERT OR IGNORE INTO job_asset_relations_v205
    (job_id, asset_id, relation, step_id, ordinal, created_at)
SELECT job_id, asset_id, relation, '', ordinal, created_at
FROM job_asset_relations;

DROP TABLE job_asset_relations;
ALTER TABLE job_asset_relations_v205 RENAME TO job_asset_relations;

CREATE INDEX IF NOT EXISTS idx_job_asset_relations_asset
    ON job_asset_relations(asset_id, relation, created_at);
CREATE INDEX IF NOT EXISTS idx_job_asset_relations_job_relation
    ON job_asset_relations(job_id, relation, ordinal);
CREATE INDEX IF NOT EXISTS idx_job_asset_relations_step
    ON job_asset_relations(step_id, created_at);

PRAGMA foreign_keys = ON;
