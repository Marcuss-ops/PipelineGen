package jobs

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	job "github.com/Marcuss-ops/PipelineGen/internal/domain/job"
	sqljobs "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/jobs"
	"go.uber.org/zap"
)

// ── Store / command types ───────────────────────────────────────────────────
//
// Wave 5 PR 3 (June 2026): removed the three zero-copy forwarding type
// aliases formerly aliased here (Store, StartJob, RequeueResult). Callers
// must now import the canonical home directly:
//   • jobs.Store                 → domain/job.Store
//   • jobs.StartJob              → internal/infrastructure/database/sqlite/jobs.StartJob
//   • jobs.RequeueResult         → internal/infrastructure/database/sqlite/jobs.RequeueResult
// The single in-tree consumer that switched to direct imports is
// internal/infrastructure/jobs/local/broker.go. The application-layer
// Runner/NewRunner are now typed against the canonical job.Store interface.
// PR4.A2 (June 2026): removed the SQLiteStore/JobStats/ErrLeaseLost type
// aliases (formerly this package's store.go). Callers now import
// internal/infrastructure/database/sqlite/jobs directly as `sqljobs`.

// Sentinel errors raised by Broker implementations and the in-process runner.
// Workers use ErrNoWorkerCapabilities to fail closed when their advertised
// job type list is empty — the W1 spec requires empty ≠ "all".
var (
	ErrNoWorkerCapabilities = errors.New("worker has no advertised capabilities")
)

// ── HTTP-layer DTOs ─────────────────────────────────────────────────────

// EnqueueRequest is the HTTP-layer DTO for enqueueing a job.
// Type uses string for domain compatibility.
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

// JobTools provides callbacks that handlers use to report progress,
// record events, and check for cancellation.
type JobTools struct {
	Progress    func(progress int, message string)
	Event       func(eventType string, message string, data map[string]any)
	IsCancelled func() bool
}

// HandlerFunc is the type for job handlers. Accepts the canonical domain
// *job.Job type. All handlers were migrated in Passaggio 6.
type HandlerFunc func(ctx context.Context, j *job.Job, tools *JobTools) (map[string]any, error)

// Dispatcher routes jobs to registered handlers by job type (string).
// Safe for concurrent use after Freeze().
type Dispatcher struct {
	mu       sync.RWMutex
	handlers map[string]HandlerFunc
	frozen   bool
}

func NewDispatcher() *Dispatcher {
	return &Dispatcher{handlers: make(map[string]HandlerFunc)}
}

func (d *Dispatcher) Register(jobType string, handler HandlerFunc) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.frozen {
		return fmt.Errorf("dispatcher is frozen: cannot register handler for %s", jobType)
	}
	if _, exists := d.handlers[jobType]; exists {
		return fmt.Errorf("handler for job type %s already registered", jobType)
	}
	d.handlers[jobType] = handler
	return nil
}

func (d *Dispatcher) Freeze() {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.frozen = true
}

// AllHandlers returns a shallow copy of all registered handlers.
// Safe for read-only iteration; used by the remote worker builder.
func (d *Dispatcher) AllHandlers() map[string]HandlerFunc {
	d.mu.RLock()
	defer d.mu.RUnlock()
	out := make(map[string]HandlerFunc, len(d.handlers))
	for k, v := range d.handlers {
		out[k] = v
	}
	return out
}

func (d *Dispatcher) Dispatch(ctx context.Context, j *job.Job, tools *JobTools) (result map[string]any, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("panic in handler for job type %s: %v", j.Type, r)
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

type RunnerConfig struct {
	Workers   int
	PollEvery time.Duration
	LeaseTTL  time.Duration
	JobTypes  []string

	// PR-Polling / ADR-0002 §D6.5 (June 2026): exponential-backoff
	// subsumed into the config; null-valued here means the Worker
	// uses the legacy fixed-poll behaviour (no backoff escalation).
	// Composition root (lifecycle_job_runner.go) populates this
	// from JobsConfig.{PollMaxBackoff, PollJitterFraction,
	// PollConsecutiveEmptyBeforeBackoff}.
	Backoff BackoffConfig

	// Notifier is the wake-on-Enqueue port. Required by the Worker
	// signature (compile-time seam marker at internal/application/jobs/
	// notifier.go::var _ QueueNotifier = (*sqljobs.SQLiteStore)(nil)).
	// Composition root wires the in-process *SQLiteStore today; a
	// future postgres adapter (LISTEN/NOTIFY) plugs in here via Deps.
	Notifier sqljobs.QueueNotifier
}

// Runner manages a pool of Workers. Depends on the domain Repository
// interface — NOT on the concrete *jobs.Repository.
type Runner struct {
	repo       job.Store
	dispatcher *Dispatcher
	log        *zap.Logger
	config     RunnerConfig
	workers    []*Worker
}

func NewRunner(repo job.Store, dispatcher *Dispatcher, log *zap.Logger, config RunnerConfig) *Runner {
	return &Runner{
		repo:       repo,
		dispatcher: dispatcher,
		log:        log,
		config:     config,
	}
}

func (r *Runner) Start(ctx context.Context) {
	r.log.Info("starting job runner", zap.Int("workers", r.config.Workers))

	for i := 0; i < r.config.Workers; i++ {
		workerID := fmt.Sprintf("%s_worker-%d", workerIDPrefix, i+1)
		worker := NewWorker(workerID, r.repo, r.dispatcher, r.config.Notifier,
			r.log, r.config.LeaseTTL, r.config.PollEvery, r.config.Backoff, r.config.JobTypes)
		r.workers = append(r.workers, worker)
		go worker.Start(ctx)
	}

	r.log.Info("job runner started", zap.Int("worker_count", len(r.workers)))
}
