-- 001_initial_schema.sql
-- Consolidated schema for images tables

-- Subjects table for organizing images
CREATE TABLE IF NOT EXISTS subjects (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL DEFAULT '',
    description TEXT,
    metadata_json TEXT NOT NULL DEFAULT '{}',
    created_at TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE INDEX IF NOT EXISTS idx_subjects_name ON subjects(name);
CREATE INDEX IF NOT EXISTS idx_subjects_created ON subjects(created_at DESC);
CREATE INDEX IF NOT EXISTS idx_subjects_updated_at ON subjects(updated_at DESC);

-- Images table
CREATE TABLE IF NOT EXISTS images (
    id TEXT PRIMARY KEY,
    subject_id TEXT,
    source TEXT NOT NULL DEFAULT '',
    source_url TEXT NOT NULL DEFAULT '',
    local_path TEXT NOT NULL DEFAULT '',
    drive_link TEXT NOT NULL DEFAULT '',
    drive_file_id TEXT NOT NULL DEFAULT '',
    download_link TEXT NOT NULL DEFAULT '',
    width INTEGER DEFAULT 0,
    height INTEGER DEFAULT 0,
    file_size_bytes INTEGER DEFAULT 0,
    file_hash TEXT,
    mime_type TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL DEFAULT 'ready',
    description TEXT NOT NULL DEFAULT '',
    metadata_json TEXT NOT NULL DEFAULT '{}',
    created_at TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at TEXT NOT NULL DEFAULT (datetime('now')),
    FOREIGN KEY (subject_id) REFERENCES subjects(id) ON DELETE SET NULL
);

CREATE INDEX IF NOT EXISTS idx_images_subject ON images(subject_id);
CREATE INDEX IF NOT EXISTS idx_images_source ON images(source);
CREATE INDEX IF NOT EXISTS idx_images_hash ON images(file_hash);
CREATE INDEX IF NOT EXISTS idx_images_created ON images(created_at DESC);
CREATE INDEX IF NOT EXISTS idx_images_updated_at ON images(updated_at DESC);
CREATE INDEX IF NOT EXISTS idx_images_status ON images(status) WHERE status != '';
CREATE INDEX IF NOT EXISTS idx_images_drive_id ON images(drive_file_id);
CREATE INDEX IF NOT EXISTS idx_images_resolution ON images(width, height) WHERE width > 0 AND height > 0;
CREATE INDEX IF NOT EXISTS idx_images_size ON images(file_size_bytes DESC) WHERE file_size_bytes > 0;
CREATE INDEX IF NOT EXISTS idx_images_mime_type ON images(mime_type) WHERE mime_type != '';

CREATE UNIQUE INDEX IF NOT EXISTS ux_images_file_hash ON images(file_hash) WHERE file_hash IS NOT NULL AND file_hash != '';
