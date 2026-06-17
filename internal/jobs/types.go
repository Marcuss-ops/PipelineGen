package jobs

import (
	"context"
	"fmt"
	"sync"
	"time"

	"go.uber.org/zap"
	"velox/go-master/internal/media/models"
	"velox/go-master/internal/repository/jobs"
)

type EnqueueRequest struct {
	Type          models.JobType `json:"type"`
	Project       string         `json:"project,omitempty"`
	VideoName     string         `json:"video_name,omitempty"`
	Payload       map[string]any `json:"payload"`
	Priority      int            `json:"priority,omitempty"`
	MaxRetries    int            `json:"max_retries,omitempty"`
	ActiveKey     string         `json:"active_key,omitempty"`
	CorrelationID string         `json:"correlation_id,omitempty"`
}

type JobTools struct {
	Progress    func(progress int, message string)
	Event       func(eventType string, message string, data map[string]any)
	IsCancelled func() bool
}

type HandlerFunc func(ctx context.Context, job *models.Job, tools *JobTools) (map[string]any, error)

// Dispatcher routes jobs to registered handlers. It is safe for concurrent
// use after Freeze() has been called.
type Dispatcher struct {
	mu       sync.RWMutex
	handlers map[models.JobType]HandlerFunc
	frozen   bool
}

func NewDispatcher() *Dispatcher {
	return &Dispatcher{handlers: make(map[models.JobType]HandlerFunc)}
}

// Register adds a handler for the given job type. Returns an error if a
// handler for the same type is already registered, or if the dispatcher
// has been frozen.
func (d *Dispatcher) Register(jobType models.JobType, handler HandlerFunc) error {
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

// Freeze prevents any further handler registration. Call this after all
// handlers have been registered and before starting workers.
func (d *Dispatcher) Freeze() {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.frozen = true
}

// Dispatch looks up the handler for the job type and executes it.
// Panics in the handler are recovered and returned as errors.
func (d *Dispatcher) Dispatch(ctx context.Context, job *models.Job, tools *JobTools) (result map[string]any, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("panic in handler for job type %s: %v", job.Type, r)
		}
	}()

	d.mu.RLock()
	handler, ok := d.handlers[job.Type]
	d.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("no handler registered for job type %s", job.Type)
	}
	return handler(ctx, job, tools)
}

type RunnerConfig struct {
	Workers   int
	PollEvery time.Duration
	LeaseTTL  time.Duration
	JobTypes  []models.JobType
}

type Runner struct {
	repo       *jobs.Repository
	dispatcher *Dispatcher
	log        *zap.Logger
	config     RunnerConfig
	workers    []*Worker
}

func NewRunner(repo *jobs.Repository, dispatcher *Dispatcher, log *zap.Logger, config RunnerConfig) *Runner {
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
		// Globally unique worker ID including hostname + PID (punto 25).
		workerID := fmt.Sprintf("%s_worker-%d", workerIDPrefix, i+1)
		worker := NewWorker(workerID, r.repo, r.dispatcher, r.log, r.config.LeaseTTL, r.config.PollEvery, r.config.JobTypes)
		r.workers = append(r.workers, worker)
		go worker.Start(ctx)
	}

	r.log.Info("job runner started", zap.Int("worker_count", len(r.workers)))
}
