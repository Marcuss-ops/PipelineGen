-- database: primary
-- Migration 216: durable per-unit checkpoint registry.
--
-- The canonical authority for resume decisions, separate from the runner's
-- best-effort workflow checkpoint (workflow_payload_json). One row per
-- completed (job_id, stage, unit_id): stage = research/script/clips/
-- asset_resolution/audio/render_scene/assemble/publish, unit_id = "global"
-- for whole-job stages or the scene id for per-scene stages.
--
-- input_fingerprint is the unit's canonical input identity (scene
-- fingerprint for render_scene units) — resume only SKIPs when it still
-- matches the current inputs AND the artifact (artifact_sha256/uri) still
-- exists and matches AND the processor version is compatible. Invalidate
-- deletes the row: an invalidated checkpoint is indistinguishable from a
-- missing one for resume.

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