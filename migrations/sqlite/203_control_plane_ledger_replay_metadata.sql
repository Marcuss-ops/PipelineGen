-- database: primary
-- Migration 203: Control Plane registry tables and replay metadata.
-- This follows migration 202 so already-applied 202 checksums remain valid.

CREATE TABLE IF NOT EXISTS registry_events (
    seq INTEGER PRIMARY KEY AUTOINCREMENT,
    event_id TEXT NOT NULL UNIQUE,
    asset_id TEXT,
    event_type TEXT NOT NULL,
    run_id TEXT,
    actor TEXT NOT NULL DEFAULT '',
    before_hash TEXT NOT NULL DEFAULT '',
    after_hash TEXT NOT NULL DEFAULT '',
    payload_json TEXT NOT NULL DEFAULT '{}',
    git_sha TEXT NOT NULL DEFAULT '',
    app_version TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_registry_events_asset_created ON registry_events(asset_id, created_at);
CREATE INDEX IF NOT EXISTS idx_registry_events_run_created ON registry_events(run_id, created_at);

CREATE TABLE IF NOT EXISTS registry_runs (
    run_id TEXT PRIMARY KEY,
    run_type TEXT NOT NULL,
    status TEXT NOT NULL,
    started_at TEXT NOT NULL,
    completed_at TEXT,
    git_sha TEXT NOT NULL DEFAULT '',
    parameters_json TEXT NOT NULL DEFAULT '{}',
    assets_seen INTEGER NOT NULL DEFAULT 0,
    assets_created INTEGER NOT NULL DEFAULT 0,
    assets_updated INTEGER NOT NULL DEFAULT 0,
    transcripts_before INTEGER NOT NULL DEFAULT 0,
    transcripts_after INTEGER NOT NULL DEFAULT 0,
    descriptions_before INTEGER NOT NULL DEFAULT 0,
    descriptions_after INTEGER NOT NULL DEFAULT 0,
    qdrant_points_before INTEGER NOT NULL DEFAULT 0,
    qdrant_points_after INTEGER NOT NULL DEFAULT 0,
    error TEXT NOT NULL DEFAULT ''
);

CREATE TABLE IF NOT EXISTS projection_registry (
    projection_id TEXT PRIMARY KEY,
    projection_type TEXT NOT NULL,
    collection_name TEXT NOT NULL,
    alias_name TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL,
    source_registry_seq INTEGER NOT NULL DEFAULT 0,
    embedding_model TEXT NOT NULL DEFAULT '',
    embedding_dimensions INTEGER NOT NULL DEFAULT 0,
    asset_count INTEGER NOT NULL DEFAULT 0,
    transcript_count INTEGER NOT NULL DEFAULT 0,
    collection_hash TEXT NOT NULL DEFAULT '',
    qdrant_version TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL,
    activated_at TEXT
);
CREATE INDEX IF NOT EXISTS idx_projection_registry_status ON projection_registry(status, source_registry_seq);

CREATE TABLE IF NOT EXISTS backup_registry (
    backup_id TEXT PRIMARY KEY,
    backup_type TEXT NOT NULL,
    source_revision INTEGER NOT NULL DEFAULT 0,
    path TEXT NOT NULL DEFAULT '',
    remote_uri TEXT NOT NULL DEFAULT '',
    sha256 TEXT NOT NULL DEFAULT '',
    size_bytes INTEGER NOT NULL DEFAULT 0,
    status TEXT NOT NULL,
    app_git_sha TEXT NOT NULL DEFAULT '',
    qdrant_version TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL,
    verified_at TEXT,
    restored_at TEXT
);
CREATE INDEX IF NOT EXISTS idx_backup_registry_status_created ON backup_registry(status, created_at);

ALTER TABLE canonical_mutations ADD COLUMN registry_seq INTEGER NOT NULL DEFAULT 0;
ALTER TABLE canonical_mutations ADD COLUMN outbox_event_id INTEGER NOT NULL DEFAULT 0;
