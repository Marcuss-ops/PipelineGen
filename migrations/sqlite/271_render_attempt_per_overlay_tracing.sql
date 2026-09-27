-- database: primary
-- Migration 271: per-overlay tracing — complete the durable per-attempt row
-- so each short video produced through the RenderingGen queue carries its own
-- correlation identity and full phase breakdown without inventing a new table.
--
-- CONTEXT
--
-- Production renders each semantic overlay item as its own short video
-- (overlay.render per-item pool, 4 concurrent), then publishes each artifact
-- to Drive. Until this migration the per-attempt analytics recorded only the
-- coarse render_ms/encode_ms + output facts + Drive identity on the FULL-
-- timeline key (plan id == job id), and the canonical run model projected
-- only 7 of the 8 worker-reported phases into operations (encode was missing).
-- The RenderingGen worker already reported the full breakdown — the pipeline
-- dropped half of it on the floor.
--
-- WHAT CHANGES
--
-- render_attempt_analytics becomes the canonical durable per-item row:
--
--  * item_id                — per-overlay correlation key (OverlayItem.ID);
--                             empty for the legacy full-timeline path;
--                             together with attempt_id (= real queue job id)
--                             it lets a batch trace "which video took how long,
--                             in which phase, on which backend".
--  * backend / chronon_version / profile_id / codec / codec_profile /
--    container / pixel_format
--                           — the worker's certified profile, kept verbatim so
--                             a per-item slow render can be distinguished from
--                             a queue/GPU-congested render.
--  * materialize_ms / plan_ms / probe_ms / sha256_ms / objectstore_upload_ms
--    / drive_publish_ms
--                           — the remaining worker-reported phase durations.
--                             Zero means the worker did not report the phase;
--                             a missing measurement is never fabricated as zero
--                             in the breakdown — the run projection skips it,
--                             the row stores 0.
--  * metrics_json           — the numeric Chronon metrics map (queue_metrics.go
--                             key vocabulary: chronon_exclusive_wall_timeline_*,
--                             chronon_job_gpu_*, chronon_job_hardware_*, etc.)
--                             marshaled verbatim.
--  * chronon_telemetry      — the BOUNDED telemetry summary (chronon_telemetry,
--                             schema chronon3d.render-telemetry-summary.v1)
--                             produced by Chronon and relayed verbatim through
--                             the queue. It is the ledger telemetry: exclusive
--                             wall, GPU backend, asset-cache, summary — sans the
--                             unbounded per-frame array.
--  * chronon_timing_storage_key / url / sha256 / size_bytes / content_type
--                           — content-addressed reference to the RAW deep-
--                             profile sidecar (<output>.timing.json, including
--                             frame_times_ms) preserved in the RenderingGen
--                             object store. Only the small reference rides the
--                             row — the per-frame array is never inlined.
--
-- CANONICAL-STORE CONTRACT
--
--  * SQLite (render_attempt_analytics + performance_operations) is the
--    canonical history of metrics. The sidecar JSON is a transport/debug
--    payload. This migration extends the existing canonical per-attempt row —
--    it does NOT create a new table (NO_NEW_TABLES).
--  * Dual write in parallel: render_attempt_analytics (coarse + per-phase +
--    context) + performance_operations (granular Chronon phases via
--    ChrononMetricsAdapter) — both existing, both fed from the same render.
--  * Best-effort, fail-open on the store seam; fail-closed on idempotency key.
--
-- AUTHORED MEASUREMENT
--
--  * The worker owns phase walls; PipelineGen never re-times a phase the
--    worker already measured. A missing phase stays absent (run) / 0 (row),
--    never a fabricated value.
--  * The analytics builder (BuildRenderAttemptAnalyticsWithWait) is pure and
--    deterministic; the attempt_id is the real queue job id (freshRender path)
--    or the plan id (idempotent path).
--
-- ACCEPTANCE
--
--  ATTEMPT_ROW_IN_RENDER_ANALYTICS — one row per item attempt, upsert by
--    attempt_id, correlated by item_id and queue job id, with the full phase
--    breakdown and backend/GPU context.
--  NO_ZERO_FABRICATION — absent phase = skipped operation / stored 0.
--  DUAL_WRITE_PARALLEL — granular performance_operations + this row both
--    receive rows for the same render.

ALTER TABLE render_attempt_analytics ADD COLUMN item_id TEXT NOT NULL DEFAULT '';
ALTER TABLE render_attempt_analytics ADD COLUMN backend TEXT NOT NULL DEFAULT '';
ALTER TABLE render_attempt_analytics ADD COLUMN chronon_version TEXT NOT NULL DEFAULT '';
ALTER TABLE render_attempt_analytics ADD COLUMN profile_id TEXT NOT NULL DEFAULT '';
ALTER TABLE render_attempt_analytics ADD COLUMN codec TEXT NOT NULL DEFAULT '';
ALTER TABLE render_attempt_analytics ADD COLUMN codec_profile TEXT NOT NULL DEFAULT '';
ALTER TABLE render_attempt_analytics ADD COLUMN container TEXT NOT NULL DEFAULT '';
ALTER TABLE render_attempt_analytics ADD COLUMN pixel_format TEXT NOT NULL DEFAULT '';
ALTER TABLE render_attempt_analytics ADD COLUMN materialize_ms INTEGER NOT NULL DEFAULT 0;
ALTER TABLE render_attempt_analytics ADD COLUMN plan_ms INTEGER NOT NULL DEFAULT 0;
ALTER TABLE render_attempt_analytics ADD COLUMN probe_ms INTEGER NOT NULL DEFAULT 0;
ALTER TABLE render_attempt_analytics ADD COLUMN hash_ms INTEGER NOT NULL DEFAULT 0;
ALTER TABLE render_attempt_analytics ADD COLUMN upload_ms INTEGER NOT NULL DEFAULT 0;
ALTER TABLE render_attempt_analytics ADD COLUMN drive_publish_ms INTEGER NOT NULL DEFAULT 0;
ALTER TABLE render_attempt_analytics ADD COLUMN metrics_json TEXT NOT NULL DEFAULT '';
ALTER TABLE render_attempt_analytics ADD COLUMN chronon_telemetry TEXT NOT NULL DEFAULT '';
ALTER TABLE render_attempt_analytics ADD COLUMN chronon_timing_storage_key TEXT NOT NULL DEFAULT '';
ALTER TABLE render_attempt_analytics ADD COLUMN chronon_timing_url TEXT NOT NULL DEFAULT '';
ALTER TABLE render_attempt_analytics ADD COLUMN chronon_timing_sha256 TEXT NOT NULL DEFAULT '';
ALTER TABLE render_attempt_analytics ADD COLUMN chronon_timing_size_bytes INTEGER NOT NULL DEFAULT 0;
ALTER TABLE render_attempt_analytics ADD COLUMN chronon_timing_content_type TEXT NOT NULL DEFAULT '';
