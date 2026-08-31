package jobs

import job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"

// FutureJobReader is the application-tier lookahead port used by the
// preparation coordinator to inspect queued jobs without claiming them.
//
// Type alias of the canonical kernel port (internal/kernel/job/future_reader.go),
// mirroring the PreparationStore alias pattern: the interface method set lives
// in the kernel, the SQLite adapter satisfies it via PeekQueued
// (internal/platform/sqlite/jobs/repository_jobs_crud.go), and this alias
// keeps the application tier in lock-step with the canonical surface.
type FutureJobReader = job.FutureJobReader
