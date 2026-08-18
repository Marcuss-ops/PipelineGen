-- database: primary
-- Migration 218: durable replay bundles.
--
-- One row per original job: the complete, self-contained snapshot needed to
-- re-execute a render deterministically (zero LLM/research/editorial), keyed
-- by the original job id. render_plan_json is the sealed render plan;
-- assets_json is the content-addressable asset identity (sha256 + cas_uri,
-- NEVER local paths) that replay materializes and re-verifies byte-for-byte
-- before rewriting execution paths in memory.
--
-- The exact execution environment (renderer, Rust protocol, FFmpeg, encoder
-- policy) is pinned so "exact" replay can FAIL on a version mismatch instead
-- of silently changing the result. Save upserts (latest canonical snapshot
-- wins); plan_sha256 is indexed for lookups by plan identity.

CREATE TABLE IF NOT EXISTS replay_bundles (
    original_job_id TEXT PRIMARY KEY,

    version TEXT NOT NULL,
    plan_sha256 TEXT NOT NULL,

    renderer_version TEXT NOT NULL,
    rust_protocol_version TEXT NOT NULL,
    ffmpeg_version TEXT NOT NULL,
    encoder_policy_hash TEXT NOT NULL DEFAULT '',

    render_plan_json TEXT NOT NULL,
    assets_json TEXT NOT NULL,

    created_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_replay_bundles_plan ON replay_bundles(plan_sha256);
