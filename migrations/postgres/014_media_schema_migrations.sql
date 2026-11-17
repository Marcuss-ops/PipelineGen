-- 014_media_schema_migrations.sql — PostgreSQL media migration ledger.
--
-- PLANE_INVENTORY.md divergence D2: the PostgreSQL media plane had no ledger;
-- the applied set was the pinned MediaSelfBootstrapDDL order plus the
-- deployment-managed one-shots 010/011. RunMediaMigrations creates this table
-- before applying the ordered DDL and records one row per applied migration
-- (version, filename, SHA-256 of the embedded DDL, duration and build
-- revision). A recorded checksum that no longer matches the embedded DDL
-- fails closed, so "which migration produced this column?" is answered by a
-- tracked artifact instead of git archaeology.
CREATE TABLE IF NOT EXISTS schema_migrations (
    version BIGINT PRIMARY KEY,
    migration_id TEXT NOT NULL DEFAULT '',
    filename TEXT NOT NULL,
    checksum_sha256 TEXT NOT NULL,
    applied_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    duration_ms BIGINT NOT NULL DEFAULT 0,
    app_git_sha TEXT NOT NULL DEFAULT ''
);

CREATE INDEX IF NOT EXISTS idx_schema_migrations_applied_at
    ON schema_migrations (applied_at);
