# Script submission run reconciliation

The SQLite jobs plane is authoritative for whether a `script.generate` job was
accepted and for its execution state. `run_observability` is the durable
workflow projection used by the API and operator views.

The HTTP producer persists a generation run before enqueue and initially uses
the run ID as a placeholder `job_id`. Enqueue and the subsequent job-ID update
cross separate SQLite/service boundaries. If the process stops after the job
is accepted, the job's `correlation_id` still contains the request's
idempotency key, so the worker can recover the run by that key when it starts.

The maintenance lifecycle also runs a durable reconciliation sweep:

1. It selects stale submission rows whose workflow payload remains `PENDING`
   or `RUNNING`.
2. A linked row is checked by its stored job ID. An unlinked row is looked up
   as `script.generate` by the persisted idempotency key in `correlation_id`.
3. Exactly one match repairs `job_attempts` and `run_observability` together in
   one transaction through `SetJobID`.
4. A queued or active job stays nonterminal; its run is checked again after the
   grace period. A succeeded job marks the run completed only when its
   `GenerateResult` checkpoint is already durable in `run_observability`. The
   broker result for this artifact-producing job is the artifact manifest, so
   it is not decoded as a generation result. A failed, cancelled, or partially
   succeeded job projects a failed run with the authoritative job outcome.
5. No match for an unlinked run marks it `SUBMISSION_ORPHANED`; the reconciler
   does not re-enqueue work. A missing linked job, multiple idempotency matches,
   malformed terminal result, or jobs-service error leaves the run unchanged
   and is surfaced for a later retry or operator review. A successful broker
   job without a durable run result also remains unresolved and is surfaced;
   the reconciler never fabricates a result from the artifact manifest.

The 15-minute grace period exceeds the synchronous enqueue timeout. The sweep
uses bounded batches and a per-pass timeout. It runs only when both the
observability database and the jobs service are wired. It reuses the existing
job broker and run repository; it does not create a second workflow engine.
