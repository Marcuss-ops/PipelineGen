package jobs

import (
	"time"

	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
	"go.uber.org/zap"
)

// WorkerIdentityDeps owns stable worker identity and accepted job types.
type WorkerIdentityDeps struct {
	ID       string
	JobTypes []string
}

// WorkerRuntimeDeps owns the queue, dispatch and wake-up ports.
type WorkerRuntimeDeps struct {
	Repo       job.Store
	Dispatcher *Dispatcher
	Notifier   QueueNotifier
}

// WorkerTimingDeps owns lease, polling and adaptive-backoff policy.
type WorkerTimingDeps struct {
	LeaseTTL  time.Duration
	PollEvery time.Duration
	Backoff   BackoffConfig
}

// WorkerDeps is the immutable constructor envelope for Worker.
type WorkerDeps struct {
	Identity WorkerIdentityDeps
	Runtime  WorkerRuntimeDeps
	Timing   WorkerTimingDeps
	Log      *zap.Logger
}

// NewWorkerFromDeps constructs a worker from capability-scoped dependencies.
// The legacy positional constructor remains temporarily while call sites migrate.
func NewWorkerFromDeps(deps WorkerDeps) *Worker {
	return NewWorker(
		deps.Identity.ID,
		deps.Runtime.Repo,
		deps.Runtime.Dispatcher,
		deps.Runtime.Notifier,
		deps.Log,
		deps.Timing.LeaseTTL,
		deps.Timing.PollEvery,
		deps.Timing.Backoff,
		deps.Identity.JobTypes,
	)
}
