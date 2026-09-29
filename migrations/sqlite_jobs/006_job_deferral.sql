-- database: jobs
-- Non-consuming deferral. A handler that is WAITING on work outside the jobs
-- plane (a remote render, a provider window) hands its attempt back with
-- job.DeferredAfter instead of spending a retry: the row goes to RETRY_WAIT
-- with retry_count UNCHANGED and deferred_until set to the instant it may be
-- re-dispatched. The worker's requeue sweep (capabilities/jobs/scheduling
-- .RetryDue) re-enqueues it exactly then.
--
-- NULL is meaningful and is the historical behaviour: a RETRY_WAIT row with no
-- stated delay follows the retry_count-derived backoff, so every pre-existing
-- retry keeps working untouched and the two wait kinds stay distinguishable.
ALTER TABLE jobs ADD COLUMN deferred_until TEXT;

-- The requeue sweep lists RETRY_WAIT rows; this index keeps that listing
-- proportional to the rows actually waiting rather than to the jobs table.
CREATE INDEX IF NOT EXISTS idx_jobs_deferred_until ON jobs(status, deferred_until);
