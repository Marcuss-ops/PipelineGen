-- Repair installations that applied migration 009 before chunk metadata was
-- added to that migration. IF NOT EXISTS keeps this safe for fresh schemas
-- and for those already carrying the columns.
ALTER TABLE render_jobs
    ADD COLUMN IF NOT EXISTS parent_job_id TEXT,
    ADD COLUMN IF NOT EXISTS chunk_index INTEGER NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS frame_range JSONB;

-- Accelerates deterministic parent-job chunk aggregation.
CREATE INDEX IF NOT EXISTS idx_render_jobs_parent_chunk
    ON render_jobs(parent_job_id, chunk_index)
    WHERE parent_job_id IS NOT NULL;
