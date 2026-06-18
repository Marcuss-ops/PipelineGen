-- PR-7: Asset Registry — generic database-backed asset store
-- Replaces directory-based Glob lookups with structured tables.

CREATE TABLE IF NOT EXISTS assets (
    asset_id TEXT PRIMARY KEY,
    kind TEXT NOT NULL CHECK (kind IN ('voiceover','scene_image','stock_clip','music','font','subtitle','thumbnail')),
    status TEXT NOT NULL DEFAULT 'PENDING'
        CHECK (status IN ('PENDING','READY','FAILED','DELETED')),

    sha256 TEXT NOT NULL UNIQUE,
    storage_backend TEXT NOT NULL DEFAULT 'local',
    storage_key TEXT NOT NULL UNIQUE,

    mime_type TEXT,
    size_bytes INTEGER NOT NULL DEFAULT 0,
    duration_ms INTEGER,
    width INTEGER,
    height INTEGER,

    created_at TEXT NOT NULL DEFAULT (datetime('now')),
    verified_at TEXT,
    last_accessed_at TEXT,
    deleted_at TEXT
);

CREATE INDEX IF NOT EXISTS idx_assets_sha256 ON assets(sha256);
CREATE INDEX IF NOT EXISTS idx_assets_kind ON assets(kind, status);
CREATE INDEX IF NOT EXISTS idx_assets_storage ON assets(storage_backend, storage_key);

CREATE TABLE IF NOT EXISTS asset_sources (
    source_id TEXT PRIMARY KEY,
    asset_id TEXT NOT NULL,
    source_type TEXT NOT NULL,
    source_reference TEXT NOT NULL,
    source_account_id TEXT,
    imported_at TEXT NOT NULL DEFAULT (datetime('now')),

    FOREIGN KEY(asset_id) REFERENCES assets(asset_id)
);

CREATE INDEX IF NOT EXISTS idx_asset_sources_asset ON asset_sources(asset_id);

CREATE TABLE IF NOT EXISTS job_assets (
    job_id TEXT NOT NULL,
    asset_id TEXT NOT NULL,
    role TEXT NOT NULL CHECK (role IN ('voiceover','scene_image','stock_clip','music','font','subtitle','thumbnail')),
    ordinal INTEGER NOT NULL DEFAULT 0,
    required INTEGER NOT NULL DEFAULT 1,
    created_at TEXT NOT NULL DEFAULT (datetime('now')),

    PRIMARY KEY(job_id, role, ordinal),
    FOREIGN KEY(job_id) REFERENCES jobs(job_id),
    FOREIGN KEY(asset_id) REFERENCES assets(asset_id)
);

CREATE INDEX IF NOT EXISTS idx_job_assets_asset ON job_assets(asset_id);
