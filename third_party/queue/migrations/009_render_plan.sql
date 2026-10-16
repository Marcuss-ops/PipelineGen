-- renderinggen.job.v1: the job payload becomes a full render SEGMENT.
--
-- The old `overlay_spec` column held a single overlay; the new contract
-- carries a chronon.render-plan.v1 document (all layers of the segment) in
-- `render_plan`, identified by the job_schema/job_schema_version envelope.
ALTER TABLE render_jobs RENAME COLUMN overlay_spec TO render_plan;
ALTER TABLE render_jobs RENAME COLUMN overlay_schema_version TO job_schema_version;

ALTER TABLE render_jobs
    ADD COLUMN job_schema TEXT NOT NULL DEFAULT 'renderinggen.job';

ALTER TABLE render_jobs
    ADD COLUMN parent_job_id TEXT,
    ADD COLUMN chunk_index INTEGER NOT NULL DEFAULT 0,
    ADD COLUMN frame_range JSONB;
