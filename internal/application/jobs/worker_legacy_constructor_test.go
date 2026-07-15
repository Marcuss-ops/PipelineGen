package jobs

import (
	"time"

	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
	"go.uber.org/zap"
)

// NewWorker is retained only in the package's test build while the remaining
// cancellation fixtures migrate away from the historical positional API.
func NewWorker(
	id string,
	repo job.Store,
	dispatcher *Dispatcher,
	notifier QueueNotifier,
	log *zap.Logger,
	leaseTTL time.Duration,
	pollEvery time.Duration,
	backoff BackoffConfig,
	jobTypes []string,
) *Worker {
	return NewWorkerFromDeps(WorkerDeps{
		Identity: WorkerIdentityDeps{ID: id, JobTypes: jobTypes},
		Runtime: WorkerRuntimeDeps{
			Repo:       repo,
			Dispatcher: dispatcher,
			Notifier:   notifier,
		},
		Timing: WorkerTimingDeps{
			LeaseTTL:  leaseTTL,
			PollEvery: pollEvery,
			Backoff:   backoff,
		},
		Log: log,
	})
}
