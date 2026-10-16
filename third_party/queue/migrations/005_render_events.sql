-- render_events: append-only history of what happened to a job.
--
-- render_jobs.state says "how the job is now"; render_events reconstructs
-- "what actually happened". The event_type vocabulary is owned by the public
-- wire contract (queue/client/events.go, client.AllRenderEvents): JOB_CREATED,
-- JOB_CLAIMED, LEASE_RENEWED, JOB_RENDERED, JOB_COMPLETED, JOB_FAILED,
-- JOB_REQUEUED, JOB_CANCELLED. This comment is pinned to that list by
-- queue/internal/repository/postgres/events_vocabulary_test.go, so a renamed
-- event cannot live on here.
CREATE TABLE render_events (
    id         BIGSERIAL PRIMARY KEY,
    job_id     TEXT NOT NULL REFERENCES render_jobs (id) ON DELETE CASCADE,
    attempt_id TEXT,
    worker_id  TEXT,
    event_type TEXT NOT NULL,
    payload    JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
