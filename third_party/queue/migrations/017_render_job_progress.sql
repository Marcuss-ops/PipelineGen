-- Per-job render progress, pushed by the owning worker while a job runs.
--
-- A small column set on render_jobs keeps the last reported frame position
-- and the wall-clock time of the last report. The queue stays the source of
-- truth for observability: GET /jobs/{id} exposes progress without having to
-- ask the worker, and each report doubles as a liveness signal for the
-- render (a parent job whose chunks all report frames_done=0 for minutes is
-- distinguishable from one that is actually rendering).
--
-- frames_done is the last frame position the renderer reported (absolute,
-- already offset for chunked execution), NOT a count of completed chunks.

ALTER TABLE render_jobs
    ADD COLUMN IF NOT EXISTS progress_frames_done INTEGER NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS progress_total_frames INTEGER NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS progress_last_frame_at TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS progress_worker TEXT NOT NULL DEFAULT '';
