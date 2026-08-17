-- database: primary
-- Migration 214: residual job-registry projection columns on jobs.
--
-- The job-registry ledger projection (internal/platform/sqlite/jobregistry)
-- reads/writes project_id, video_id, payload_hash, host and duration_ms on
-- the canonical jobs table. Those five columns existed in the live primary
-- DB (and in the projection's own test fixture) but were never declared by
-- any migration, so a fresh DB would lack them and the projection's
-- INSERT/UPDATE would fail. This migration closes that schema gap.
--
-- No value backfill: project_id/video_id/host are runtime identity supplied
-- by the caller at write time (empty when unknown), payload_hash is a
-- canonicalized-JSON sha256 that must be computed in Go, and duration_ms was
-- already backfilled by the media-durations backfill. The runner's
-- duplicate-ADD-COLUMN soft-skip makes these statements no-ops on legacy
-- databases that already carry the columns.

ALTER TABLE jobs ADD COLUMN project_id TEXT NOT NULL DEFAULT '';
ALTER TABLE jobs ADD COLUMN video_id TEXT NOT NULL DEFAULT '';
ALTER TABLE jobs ADD COLUMN payload_hash TEXT NOT NULL DEFAULT '';
ALTER TABLE jobs ADD COLUMN host TEXT NOT NULL DEFAULT '';
ALTER TABLE jobs ADD COLUMN duration_ms INTEGER NOT NULL DEFAULT 0;
