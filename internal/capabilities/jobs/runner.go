package jobs

import (
	"context"
	"fmt"

	capjobregistry "github.com/Marcuss-ops/PipelineGen/internal/capabilities/jobregistry"
	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
	kernobs "github.com/Marcuss-ops/PipelineGen/internal/kernel/observability"
	"github.com/Marcuss-ops/PipelineGen/pkg/concurrent"
	"go.uber.org/zap"
)

type Runner struct {
	repo       job.Store
	dispatcher *Dispatcher
	log        *zap.Logger
	cfg        RunnerConfig
	reg        *Registry
	broker     CompletionPort
	jobLedger  capjobregistry.Registry
	observer   *kernobs.RunObserver

	// resourceSampler is the run-scoped resource telemetry port (August
	// 2026), propagated to every Worker by buildWorkers. nil = workers
	// run un-instrumented (fixtures keep working).
	resourceSampler kernobs.RunResourceSampler
	// host is the hostname stamped on every resource observation.
	host string
}

func NewRunner(repo job.Store, dispatcher *Dispatcher, log *zap.Logger, cfg RunnerConfig) *Runner {
	if cfg.Workers <= 0 {
		cfg.Workers = 1
	}
	return &Runner{
		repo:       repo,
		dispatcher: dispatcher,
		log:        log,
		cfg:        cfg,
	}
}

func (r *Runner) WithRegistry(reg *Registry) *Runner {
	r.reg = reg
	return r
}

func (r *Runner) WithBroker(broker CompletionPort) *Runner {
	r.broker = broker
	return r
}

func (r *Runner) WithJobRegistry(reg capjobregistry.Registry) *Runner {
	r.jobLedger = reg
	return r
}

func (r *Runner) WithObserver(obs *kernobs.RunObserver) *Runner {
	r.observer = obs
	return r
}

// WithResourceSampler attaches the run-scoped resource telemetry port
// (August 2026) and the hostname stamped on every observation. Mirrors the
// WithObserver fluent-setter precedent; buildWorkers propagates both to
// every Worker. Nil-tolerant: a nil sampler keeps the legacy
// un-instrumented worker path.
func (r *Runner) WithResourceSampler(s kernobs.RunResourceSampler, host string) *Runner {
	r.resourceSampler = s
	r.host = host
	return r
}

// buildWorkers constructs the worker pool and attaches every wired
// dependency (registry, broker, job ledger, observer) to each Worker.
// Start() runs these workers; the helper exists so tests can assert
// the attachment contract deterministically without goroutines.
func (r *Runner) buildWorkers() []*Worker {
	workers := make([]*Worker, 0, r.cfg.Workers)
	for i := 0; i < r.cfg.Workers; i++ {
		workerID := fmt.Sprintf("%s_%d", workerIDPrefix, i+1)
		w := NewWorker(WorkerDeps{
			ID:         workerID,
			Repo:       r.repo,
			Dispatcher: r.dispatcher,
			Notifier:   r.cfg.Notifier,
			Log:        r.log,
			LeaseTTL:   r.cfg.LeaseTTL,
			PollEvery:  r.cfg.PollEvery,
			Backoff:    r.cfg.Backoff,
			Types:      r.cfg.JobTypes,
		})
		if r.reg != nil {
			w.WithRegistry(r.reg)
		}
		if r.broker != nil {
			w.WithBroker(r.broker)
		}
		if r.jobLedger != nil {
			w.WithJobRegistry(r.jobLedger)
		}
		if r.observer != nil {
			w.WithObserver(r.observer)
		}
		if r.resourceSampler != nil {
			w.WithResourceSampler(r.resourceSampler, r.host)
		}
		workers = append(workers, w)
	}
	return workers
}

func (r *Runner) Start(ctx context.Context) {
	for _, w := range r.buildWorkers() {
		concurrent.SafeGo(fmt.Sprintf("worker-%s", w.id), func() {
			w.Start(ctx)
		})
	}
	<-ctx.Done()
}
