package jobs

import job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"

// FutureJobReader port — adapter site.
//
// PeekQueued (defined in repository_jobs_crud.go) is the sole implementation
// of the canonical read-only lookahead port. It is a SELECT-only query:
// it never writes started_at / lease_id / lease_expiry / status / revision /
// retry_count, so the job formally keeps waiting and the claim path
// (ClaimNext / Start / RenewLease in repository_claims.go) stays the only
// mutation surface. The ordering matches ClaimNext
// (priority DESC, created_at ASC) so preparation sees the jobs the worker
// will actually pick next.
//
// Compile-time assertion (defence-in-depth; the twin assertion lives at
// internal/capabilities/jobs/future_reader.go, per the QueueNotifier
// precedent).
var _ job.FutureJobReader = (*SQLiteStore)(nil)
