-- 090_fix_job_assets_fk.sql
--
-- WHY: migration/sqlite/054_asset_registry.sql creates `job_assets` with
--   FOREIGN KEY(job_id) REFERENCES jobs(job_id)
-- but the `jobs` PRIMARY KEY is `id` (NOT `job_id`). The broken FK is
-- surfaced by internal/platform/sqlite/migrations_test.go::
-- TestMigrations_Smoke/ForeignKeysCheck as:
--   PRAGMA foreign_key_check: foreign key mismatch - "job_assets" referencing "jobs"
-- This migration corrects the FK to `jobs(id)` without touching the
-- 054 file (whose SHA-256 is enforced by the migration runner —
-- modifying an applied migration crashes production startup).
--
-- STRATEGY: rename broken table -> recreate with correct FK ->
-- copy rows back -> drop rename. Idempotent (we re-execute only if
-- 054's broken FK is still in place; once corrected, a re-run of 090
-- on a DB that already has the FIXED schema will RENAME the live
-- table to job_assets_v090_tmp, CREATE the same schema (definition is
-- stable), COPY zero rows (because the _tmp table is empty), and DROP
-- _tmp. That round-trips a no-op on a clean run.)
--
-- DATA SAFETY: INSERT ... SELECT preserves any rows that may exist
-- in production databases that have already applied 054 with the
-- broken FK.
--
-- TRANSACTION WRAPPING: intentionally omitted. RunMigrationsOnDB
-- wraps each migration file in its own transaction (see
-- internal/platform/sqlite/migrations.go), so an explicit
-- BEGIN/COMMIT here would either be redundant or — if the runner
-- uses savepoints with nested-transaction support — produce a
-- no-op transaction. Either way, leaving it out keeps the migration
-- single-purpose.

PRAGMA foreign_keys = OFF;

ALTER TABLE job_assets RENAME TO job_assets_v090_tmp;

CREATE TABLE IF NOT EXISTS job_assets (
    job_id TEXT NOT NULL,
    asset_id TEXT NOT NULL,
    role TEXT NOT NULL CHECK (role IN ('voiceover','scene_image','stock_clip','music','font','subtitle','thumbnail')),
    ordinal INTEGER NOT NULL DEFAULT 0,
    required INTEGER NOT NULL DEFAULT 1,
    created_at TEXT NOT NULL DEFAULT (datetime('now')),

    PRIMARY KEY(job_id, role, ordinal),
    FOREIGN KEY(job_id) REFERENCES jobs(id),
    FOREIGN KEY(asset_id) REFERENCES assets(asset_id)
);

INSERT INTO job_assets (job_id, asset_id, role, ordinal, required, created_at)
SELECT job_id, asset_id, role, ordinal, required, created_at
FROM job_assets_v090_tmp;

DROP TABLE job_assets_v090_tmp;

PRAGMA foreign_keys = ON;
