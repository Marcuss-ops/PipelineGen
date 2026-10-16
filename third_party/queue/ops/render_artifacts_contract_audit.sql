-- Assembly certification audit — RenderingGen Postgres store (render_artifacts).
--
-- render_artifacts is the DURABLE certification store for assembly-eligible
-- overlay/clip artifacts (migrations/003_render_artifacts.sql): it holds the
-- copy-gate identity facts (profile_id, codec, codec_profile, closed_gop,
-- first_frame_keyframe, geometry, fps) that VeloxEditing relies on for
-- copy-only assembly without re-decoding.
--
-- This query produces the Postgres HALF of the evidence demanded by the
-- transform_assemble.rs REMOVAL GATE (the skip of the contract_id check for
-- contract-less certifications). The Rust gate accepts a certification when
-- contract_id == 'VELOX_ASSEMBLY_READY_V1' OR stream_signature_sha256 is
-- present; on this store those identities surface through a canonical output
-- profile id PLUS complete stream facts, because the Go bundle that feeds the
-- wire DROPS explicit contract_id / stream_signature_sha256 (queued via
-- RenderingGen pipeline.go; the metadata-decoder in Rust never receives those
-- keys).
--
-- The profile id itself is owned by the wire contract (queue/client,
-- CertifiedProfileVeloxH2641080p30V1; the worker's media registry aliases it)
-- and is deliberately NOT restated here: a hardcoded id in this audit could
-- never match the constant once it is renamed, and would turn the gate into a
-- silent false pass. Section 3 asserts the SHAPE (exactly one distinct profile
-- id) + completeness, never a literal value.
--
-- Criterion: every copy_eligible = TRUE row must carry a COMPLETE identity —
-- profile_id plus non-NULL codec/codec_profile/closed_gop/
-- first_frame_keyframe/geometry/fps. Any NULL below keeps the gate LOCKED and
-- names the rows to re-render or backfill.
--
-- The SQLite half lives at refactored/ops/maintenance/audit_assembly_certifications.py.
--
-- Run against the RenderingGen queue database (DSN per RenderingGen/queue README):
--   psql "$RENDERINGGEN_QUEUE_DSN" -f ops/render_artifacts_contract_audit.sql

-- ── 1. Per-row audit: copy-eligible artifacts missing ANY identity fact ──
SELECT
    a.id                                        AS artifact_id,
    j.id                                        AS job_id,
    a.created_at,
    a.profile_id IS NULL                        AS missing_profile,
    a.codec IS NULL                             AS missing_codec,
    a.codec_profile IS NULL                     AS missing_codec_profile,
    a.closed_gop IS NULL                        AS missing_closed_gop,
    a.first_frame_keyframe IS NULL              AS missing_keyframe_fact,
    a.width IS NULL OR a.height IS NULL         AS missing_geometry,
    a.fps_num IS NULL OR a.fps_den IS NULL      AS missing_fps
FROM render_artifacts AS a
JOIN render_jobs      AS j ON j.id = a.job_id
WHERE a.copy_eligible = TRUE
  AND (   a.profile_id IS NULL
       OR a.codec IS NULL
       OR a.codec_profile IS NULL
       OR a.closed_gop IS NULL
       OR a.first_frame_keyframe IS NULL
       OR a.width IS NULL OR a.height IS NULL
       OR a.fps_num IS NULL OR a.fps_den IS NULL)
ORDER BY a.created_at DESC;

-- ── 2. Aggregated verdict: these counts feed the gate decision directly ──
--
-- Gate UNLOCKED requires: copy_eligible_total > 0 AND every contractless_*
-- count == 0. (Zero verified certifications is NO EVIDENCE, not evidence of
-- absence — same rule as the SQLite half.)
SELECT
    COUNT(*) FILTER (WHERE a.copy_eligible = TRUE)                            AS copy_eligible_total,
    COUNT(*) FILTER (WHERE a.copy_eligible = TRUE AND a.profile_id IS NULL)   AS contractless_profile,
    COUNT(*) FILTER (WHERE a.copy_eligible = TRUE AND a.codec IS NULL)        AS contractless_codec,
    COUNT(*) FILTER (WHERE a.copy_eligible = TRUE AND a.codec_profile IS NULL)AS contractless_codec_profile,
    COUNT(*) FILTER (WHERE a.copy_eligible = TRUE AND a.closed_gop IS NULL)   AS contractless_closed_gop,
    COUNT(*) FILTER (WHERE a.copy_eligible = TRUE AND a.first_frame_keyframe IS NULL) AS contractless_keyframe_fact,
    COUNT(*) FILTER (WHERE a.copy_eligible = TRUE AND (a.width IS NULL OR a.height IS NULL)) AS contractless_geometry,
    COUNT(*) FILTER (WHERE a.copy_eligible = TRUE AND (a.fps_num IS NULL OR a.fps_den IS NULL)) AS contractless_fps
FROM render_artifacts AS a;

-- ── 3. Profile distribution: confirm exactly ONE certified profile shape ──
-- A second distinct profile_id here means a second certified shape exists;
-- before any gate removal each distinct profile needs its own contract
-- mapping. Assert cardinality, not a literal id: the canonical id lives in
-- queue/client (CertifiedProfileVeloxH2641080p30V1, the wire owner).
SELECT a.profile_id,
       COUNT(*)            AS artifacts,
       COUNT(*) FILTER (WHERE a.copy_eligible) AS copy_eligible
FROM render_artifacts AS a
GROUP BY a.profile_id
ORDER BY artifacts DESC;
