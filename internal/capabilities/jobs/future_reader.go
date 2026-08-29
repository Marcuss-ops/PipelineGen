package jobs

import (
	sqljobs "github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/jobs"

	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
)

// FutureJobReader is the application-tier lookahead port used by the
// preparation coordinator to inspect queued jobs without claiming them.
//
// Type alias of the canonical kernel port (internal/kernel/job/future_reader.go),
// mirroring the PreparationStore alias pattern: the interface method set lives
// in the kernel, the SQLite adapter satisfies it via PeekQueued
// (internal/platform/sqlite/jobs/repository_jobs_crud.go), and this alias
// keeps the application tier in lock-step with the canonical surface.
type FutureJobReader = job.FutureJobReader

// Compile-time assertion: *sqljobs.SQLiteStore satisfies the canonical
// FutureJobReader port (its PeekQueued is read-only by contract and does not
// acquire leases or mutate job state). The same assertion lives at the
// adapter site (internal/platform/sqlite/jobs/future_reader.go) — intentional
// defence-in-depth per the QueueNotifier precedent.
var _ FutureJobReader = (*sqljobs.SQLiteStore)(nil)
