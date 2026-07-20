-- 161_stock_batches.sql — durable stock batch / group / artifact state.
--
-- Purpose: track every stock clip through its lifecycle
-- (PLANNED → EXTRACTING → EXTRACTED → ... → VERIFIED) so that retry,
-- resume, and reconciliation can operate on a single artifact rather
-- than re-running an entire job.
--
-- Companion code path:
--   internal/application/assets/providers/stock/stockpipeline/batch_repository.go
--   internal/infrastructure/database/sqlite/stockbatches/repository.go
--
-- Down migration: DROP TABLE IF EXISTS stock_artifacts; DROP TABLE IF EXISTS stock_batch_groups; DROP TABLE IF EXISTS stock_batches;

CREATE TABLE IF NOT EXISTS stock_batches (
    id                TEXT PRIMARY KEY,
    fingerprint       TEXT NOT NULL DEFAULT '',
    source_url        TEXT NOT NULL DEFAULT '',
    source_cache_key  TEXT NOT NULL DEFAULT '',
    root_folder_id    TEXT NOT NULL DEFAULT '',
    root_folder_name  TEXT NOT NULL DEFAULT '',
    status            TEXT NOT NULL DEFAULT 'PLANNED'
                      CHECK (status IN ('PLANNED','RUNNING','SUCCEEDED','FAILED','RETRY_WAIT')),
    expected_groups   INTEGER NOT NULL DEFAULT 0,
    expected_clips    INTEGER NOT NULL DEFAULT 0,
    verified_clips    INTEGER NOT NULL DEFAULT 0,
    policy_version    TEXT NOT NULL DEFAULT '',
    last_error        TEXT NOT NULL DEFAULT '',
    created_at        TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at        TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE INDEX IF NOT EXISTS idx_stock_batches_status ON stock_batches(status);
CREATE INDEX IF NOT EXISTS idx_stock_batches_source_cache_key ON stock_batches(source_cache_key);
CREATE INDEX IF NOT EXISTS idx_stock_batches_fingerprint ON stock_batches(fingerprint);

CREATE TABLE IF NOT EXISTS stock_batch_groups (
    id               TEXT PRIMARY KEY,
    batch_id         TEXT NOT NULL REFERENCES stock_batches(id) ON DELETE CASCADE,
    group_key        TEXT NOT NULL DEFAULT '',
    title            TEXT NOT NULL DEFAULT '',
    folder_name      TEXT NOT NULL DEFAULT '',
    drive_folder_id  TEXT NOT NULL DEFAULT '',
    start_sec        REAL NOT NULL DEFAULT 0,
    end_sec          REAL NOT NULL DEFAULT 0,
    expected_clips   INTEGER NOT NULL DEFAULT 0,
    verified_clips   INTEGER NOT NULL DEFAULT 0,
    status           TEXT NOT NULL DEFAULT 'PLANNED'
                     CHECK (status IN ('PLANNED','RUNNING','SUCCEEDED','FAILED','RETRY_WAIT')),
    child_job_id     TEXT NOT NULL DEFAULT '',
    attempts         INTEGER NOT NULL DEFAULT 0,
    last_error       TEXT NOT NULL DEFAULT '',
    created_at       TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at       TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE INDEX IF NOT EXISTS idx_stock_batch_groups_batch_id ON stock_batch_groups(batch_id);
CREATE INDEX IF NOT EXISTS idx_stock_batch_groups_status ON stock_batch_groups(status);
CREATE INDEX IF NOT EXISTS idx_stock_batch_groups_child_job_id ON stock_batch_groups(child_job_id);

CREATE TABLE IF NOT EXISTS stock_artifacts (
    id                   TEXT PRIMARY KEY,
    batch_id             TEXT NOT NULL REFERENCES stock_batches(id) ON DELETE CASCADE,
    group_id             TEXT NOT NULL REFERENCES stock_batch_groups(id) ON DELETE CASCADE,
    ordinal              INTEGER NOT NULL DEFAULT 0,
    artifact_key         TEXT NOT NULL DEFAULT '',
    source_url           TEXT NOT NULL DEFAULT '',
    start_sec            REAL NOT NULL DEFAULT 0,
    end_sec              REAL NOT NULL DEFAULT 0,
    expected_duration_ms INTEGER NOT NULL DEFAULT 0,
    actual_duration_ms   INTEGER NOT NULL DEFAULT 0,
    local_path           TEXT NOT NULL DEFAULT '',
    sha256               TEXT NOT NULL DEFAULT '',
    status               TEXT NOT NULL DEFAULT 'PLANNED'
                         CHECK (status IN ('PLANNED','EXTRACTING','EXTRACTED','COMPOSING','COMPOSED','PUBLISHING','PUBLISHED','VERIFIED','RETRY_WAIT','FAILED_PERMANENT','QUARANTINED')),
    drive_file_id        TEXT NOT NULL DEFAULT '',
    drive_folder_id      TEXT NOT NULL DEFAULT '',
    drive_link           TEXT NOT NULL DEFAULT '',
    attempts             INTEGER NOT NULL DEFAULT 0,
    last_error           TEXT NOT NULL DEFAULT '',
    created_at           TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at           TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE INDEX IF NOT EXISTS idx_stock_artifacts_batch_id ON stock_artifacts(batch_id);
CREATE INDEX IF NOT EXISTS idx_stock_artifacts_group_id ON stock_artifacts(group_id);
CREATE INDEX IF NOT EXISTS idx_stock_artifacts_status ON stock_artifacts(status);
CREATE INDEX IF NOT EXISTS idx_stock_artifacts_ordinal ON stock_artifacts(group_id, ordinal);
