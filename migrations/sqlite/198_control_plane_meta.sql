-- database: primary
-- Canonical Control Plane identity.

CREATE TABLE IF NOT EXISTS control_plane_meta (
    singleton_id     INTEGER PRIMARY KEY CHECK (singleton_id = 1),
    database_id      TEXT NOT NULL UNIQUE,
    schema_family    TEXT NOT NULL,
    instance_role    TEXT NOT NULL CHECK (instance_role IN ('CANONICAL','READ_ONLY','MIGRATION_SOURCE','ARCHIVE')),
    canonical_version INTEGER NOT NULL,
    created_at       TEXT NOT NULL
);

INSERT INTO control_plane_meta
    (singleton_id, database_id, schema_family, instance_role, canonical_version, created_at)
SELECT 1, 'cp_' || lower(hex(randomblob(16))), 'pipelinegen-control-plane', 'CANONICAL', 1, datetime('now')
WHERE NOT EXISTS (SELECT 1 FROM control_plane_meta);

CREATE UNIQUE INDEX IF NOT EXISTS idx_control_plane_meta_singleton
    ON control_plane_meta(singleton_id);
CREATE UNIQUE INDEX IF NOT EXISTS idx_control_plane_meta_schema_family
    ON control_plane_meta(schema_family);
