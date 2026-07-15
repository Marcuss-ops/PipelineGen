package jobs

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	sqljobs "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/jobs"
	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
	"go.uber.org/zap"
)

var ErrNoWorkerCapabilities = errors.New("worker has no advertised capabilities")

// EnqueueRequest is the HTTP-layer DTO for enqueueing a job.
type EnqueueRequest struct {
	Type          string `json:"type"`
	Project       string `json:"project,omitempty"`
	VideoName     string `json:"video_name,omitempty"`
	Payload       any    `json:"payload"`
	Priority      int    `json:"priority,omitempty"`
	MaxRetries    int    `json:"max_retries,omitempty"`
	ActiveKey     string `json:"active_key,omitempty"`
	CorrelationID string `json:"correlation_id,omitempty"`
}

// Canonical execution types live in internal/kernel/job. These aliases retain
// the application package's existing public surface during migration.
type (
	JobExecutionTools = job.JobExecutionTools
	JobTools          = job.JobExecutionTools
	Result            = job.Result
	Handler           = job.Handler
	HandlerFunc       = job.Handler
)

// Dispatcher routes jobs to registered handlers and becomes immutable after
// Freeze. Registry and enqueuer are attached by the canonical composition path.
type Dispatcher struct {
	mu       sync.RWMutex
	handlers map[string]Handler
	frozen   bool
	registry job.CompiledJobRegistry
	enqueuer EnqueuePort
}

func NewDispatcher() *Dispatcher {
	return &Dispatcher{handlers: make(map[string]Handler)}
}

func (d *Dispatcher) Register(jobType string, handler Handler) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.frozen {
		return fmt.Errorf("dispatcher is frozen: cannot register handler for %s", jobType)
	}
	d.handlers[jobType] = handler
	return nil
}

func (d *Dispatcher) Freeze() {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.frozen = true
}

func (d *Dispatcher) AllHandlers() map[string]Handler {
	d.mu.RLock()
	defer d.mu.RUnlock()
	out := make(map[string]Handler, len(d.handlers))
	for key, handler := range d.handlers {
		out[key] = handler
	}
	return out
}

func (d *Dispatcher) Dispatch(ctx context.Context, j *job.Job, tools *JobExecutionTools) (result Result, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("panic in handler for job type %s: %v", j.Type, recovered)
		}
	}()
	d.mu.RLock()
	handler, ok := d.handlers[j.Type]
	d.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("no handler registered for job type %s", j.Type)
	}
	return handler(ctx, j, tools)
}

// RunnerConfig owns worker count, lease/polling policy and advertised types.
type RunnerConfig struct {
	Workers   int
	PollEvery time.Duration
	LeaseTTL  time.Duration
	JobTypes  []string
	Backoff   BackoffConfig
	Notifier  sqljobs.QueueNotifier
}

// Runner manages a pool of workers over the canonical job.Store port.
type Runner struct {
	repo       job.Store
	dispatcher *Dispatcher
	log        *zap.Logger
	config     RunnerConfig
	registry   *Registry
	workers    []*Worker
	broker     CompletionPort
}

func NewRunner(repo job.Store, dispatcher *Dispatcher, log *zap.Logger, config RunnerConfig) *Runner {
	return &Runner{
		repo:       repo,
		dispatcher: dispatcher,
		log:        log,
		config:     config,
	}
}

func (r *Runner) WithRegistry(registry *Registry) *Runner {
	r.registry = registry
	return r
}

func (r *Runner) WithBroker(broker CompletionPort) *Runner {
	r.broker = broker
	return r
}

func (r *Runner) buildWorkers() []*Worker {
	workers := make([]*Worker, 0, r.config.Workers)
	for i := 0; i < r.config.Workers; i++ {
		workerID := fmt.Sprintf("%s_worker-%d", workerIDPrefix, i+1)
		worker := NewWorkerFromDeps(WorkerDeps{
			Identity: WorkerIdentityDeps{
				ID:       workerID,
				JobTypes: r.config.JobTypes,
			},
			Runtime: WorkerRuntimeDeps{
				Repo:       r.repo,
				Dispatcher: r.dispatcher,
				Notifier:   r.config.Notifier,
			},
			Timing: WorkerTimingDeps{
				LeaseTTL:  r.config.LeaseTTL,
				PollEvery: r.config.PollEvery,
				Backoff:   r.config.Backoff,
			},
			Log: r.log,
		}).WithRegistry(r.registry)
		if r.broker != nil {
			worker.WithBroker(r.broker)
		}
		workers = append(workers, worker)
	}
	return workers
}

func (r *Runner) Start(ctx context.Context) {
	r.log.Info("starting job runner", zap.Int("workers", r.config.Workers))
	r.workers = r.buildWorkers()
	for _, worker := range r.workers {
		go worker.Start(ctx)
	}
	r.log.Info("job runner started", zap.Int("worker_count", len(r.workers)))
}
