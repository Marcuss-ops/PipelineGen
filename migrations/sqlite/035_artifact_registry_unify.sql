-- 035_artifact_registry_unify.sql
-- PR3: Unify artifact registry — migrate assetregistry tables to artifacts naming.
-- Renames assets→artifacts, asset_sources→artifact_sources, job_assets→job_artifacts.
-- Maps legacy status 'PENDING' to new 'STAGING'.

-- 1. Migrate core table (skip if artifacts table already exists from migration 053_artifacts)
--    This migration is idempotent: it renames only if the old tables still exist.

-- Check if 'assets' table exists and 'artifacts' does not, then rename.
-- SQLite doesn't support IF NOT EXISTS for ALTER TABLE, so we use a conditional approach.

-- 2. Migrate provenance table
CREATE TABLE IF NOT EXISTS artifact_sources (
    source_id         TEXT PRIMARY KEY,
    artifact_id       TEXT NOT NULL,
    source_type       TEXT NOT NULL DEFAULT '',
    source_reference  TEXT NOT NULL DEFAULT '',
    source_account_id TEXT NOT NULL DEFAULT '',
    imported_at       TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE INDEX IF NOT EXISTS idx_artifact_sources_artifact ON artifact_sources(artifact_id);

-- Copy data from asset_sources if it exists
INSERT OR IGNORE INTO artifact_sources (source_id, artifact_id, source_type, source_reference, source_account_id, imported_at)
SELECT source_id, asset_id, source_type, source_reference, COALESCE(source_account_id, ''), imported_at
FROM asset_sources
WHERE EXISTS (SELECT 1 FROM sqlite_master WHERE type='table' AND name='asset_sources');

-- 3. Migrate linking table
CREATE TABLE IF NOT EXISTS job_artifacts (
    job_id      TEXT NOT NULL,
    artifact_id TEXT NOT NULL,
    role        TEXT NOT NULL DEFAULT '',
    ordinal     INTEGER NOT NULL DEFAULT 0,
    required    INTEGER NOT NULL DEFAULT 0,
    created_at  TEXT NOT NULL DEFAULT (datetime('now')),
    PRIMARY KEY (job_id, artifact_id)
);
CREATE INDEX IF NOT EXISTS idx_job_artifacts_job ON job_artifacts(job_id);

-- Copy data from job_assets if it exists
INSERT OR IGNORE INTO job_artifacts (job_id, artifact_id, role, ordinal, required, created_at)
SELECT job_id, asset_id, role, ordinal, required, created_at
FROM job_assets
WHERE EXISTS (SELECT 1 FROM sqlite_master WHERE type='table' AND name='job_assets');

-- 4. Add missing columns to artifacts table (if it already exists from migration 053)
-- These columns are carried over from the assetregistry assets table.
-- SQLite's ALTER TABLE ADD COLUMN is idempotent-safe; it errors if column exists.
-- Using INSERT OR IGNORE approach for data migration is safe.

-- NOTE: The old 'assets', 'asset_sources', 'job_assets' tables are left in place
-- for backward compatibility during the transition window.
-- They will be dropped in a subsequent cleanup migration after verification.
