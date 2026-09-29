-- database: primary
-- Migration 272: RESTORE job_checkpoints on databases that lost it.
--
-- Why this file exists (observed in production, 2026-09-28): the live primary
-- DB (data/media/media.db.sqlite) carries a schema_migrations row for version
-- 216 whose checksum matches 216_job_checkpoints.sql byte-for-byte, while the
-- table it declares does NOT exist in that database. Every other table from
-- the 217+ migrations is present, so the database is not a stale restore.
--
-- Consequence before this migration: the durable checkpoint resolver
-- (platform/sqlite/checkpoint, wired on the primary handle at
-- app/wiring/script_generation_runtime.go) failed on every write —
-- "durable audio checkpoint write failed ... no such table: job_checkpoints" —
-- so resume decisions fell back to the best-effort workflow checkpoint.
--
-- The repair MUST be a NEW numbered file, never an edit of 216: a file that is
-- already recorded as applied is skipped by the runner (the ledger is the
-- authority), so adding statements to it is invisible on every deployed
-- database. That is the failure mode this migration exists to undo, and
-- platform/sqlite's boot-time declared-vs-live verification now reports it.
--
-- Schema is byte-identical to 216 (and to the baseline's copy) on purpose:
-- the canonical shape has ONE owner, so if this file ever drifts from it the
-- schema-contract test fails rather than letting two shapes coexist.
CREATE TABLE IF NOT EXISTS job_checkpoints (
    job_id TEXT NOT NULL,
    stage TEXT NOT NULL,
    unit_id TEXT NOT NULL,

    input_fingerprint TEXT NOT NULL,

    status TEXT NOT NULL,

    artifact_sha256 TEXT NOT NULL DEFAULT '',
    artifact_uri TEXT NOT NULL DEFAULT '',

    processor_version TEXT NOT NULL,

    completed_at TEXT NOT NULL,

    PRIMARY KEY(job_id, stage, unit_id)
);
CREATE INDEX IF NOT EXISTS idx_job_checkpoints_job ON job_checkpoints(job_id, completed_at);
