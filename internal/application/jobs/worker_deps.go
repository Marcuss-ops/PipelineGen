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
func NewWorkerFromDeps(deps WorkerDeps) *Worker {
	return &Worker{
		id:         deps.Identity.ID,
		repo:       deps.Runtime.Repo,
		dispatcher: deps.Runtime.Dispatcher,
		log:        deps.Log,
		leaseTTL:   deps.Timing.LeaseTTL,
		pollEvery:  deps.Timing.PollEvery,
		backoff:    deps.Timing.Backoff,
		types:      deps.Identity.JobTypes,
		notifier:   deps.Runtime.Notifier,
	}
}
