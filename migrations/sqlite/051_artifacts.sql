-- Migration 051: artifacts table for content-addressed artifact storage.
-- Artifacts are the output of render/processing jobs (videos, audio, thumbnails).
-- Lifecycle: STAGING → VERIFYING → READY → (optionally) DELETED.
-- Storage is content-addressed via SHA-256 in local BlobStore (blobs/sha256/XX/...).

CREATE TABLE IF NOT EXISTS artifacts (
    id              TEXT PRIMARY KEY,
    job_id          TEXT NOT NULL DEFAULT '',
    kind            TEXT NOT NULL DEFAULT 'unknown',
    status          TEXT NOT NULL DEFAULT 'STAGING'
        CHECK (status IN ('STAGING','VERIFYING','READY','FAILED','QUARANTINED','DELETED')),
    storage_backend TEXT NOT NULL DEFAULT 'local',
    storage_key     TEXT NOT NULL DEFAULT '',
    sha256          TEXT NOT NULL DEFAULT '',
    size_bytes      INTEGER NOT NULL DEFAULT 0,
    mime_type       TEXT NOT NULL DEFAULT '',
    created_at      TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at      TEXT NOT NULL DEFAULT (datetime('now')),
    verified_at     TEXT
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_artifacts_sha256 ON artifacts(sha256) WHERE sha256 != '';
CREATE INDEX IF NOT EXISTS idx_artifacts_job ON artifacts(job_id);
CREATE INDEX IF NOT EXISTS idx_artifacts_status ON artifacts(status);
