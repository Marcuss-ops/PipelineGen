package jobs

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/core/domain/job"
	"github.com/Marcuss-ops/PipelineGen/internal/media/models"
)

// EnqueueRequest is the HTTP-layer DTO for enqueueing a job. Still uses
// string types for backward compatibility with 95+ call sites; migrated
// to domain types in Passaggio 6.
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

// JobTools provides callbacks that handlers use to report progress,
// record events, and check for cancellation.
type JobTools struct {
	Progress    func(progress int, message string)
	Event       func(eventType string, message string, data map[string]any)
	IsCancelled func() bool
}

// HandlerFunc is the type for job handlers. Temporarily accepts *models.Job
// for backward compatibility with ~30 existing handler implementations.
// Passaggio 6 will migrate all handlers to accept domain *job.Job.
type HandlerFunc func(ctx context.Context, job *models.Job, tools *JobTools) (map[string]any, error)

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
	// Bridge: convert domain *job.Job → legacy *models.Job for handler
	// compatibility. Removed in Passaggio 6 when handlers migrate.
	return handler(ctx, domainToModelsLegacy(j), tools)
}

// domainToModelsLegacy converts *job.Job → *models.Job for the dispatcher
// bridge. Removed in Passaggio 6.
func domainToModelsLegacy(j *job.Job) *models.Job {
	if j == nil {
		return nil
	}
	status := models.StatusPending
	switch j.Status {
	case job.StatusRunning:
		status = models.StatusRunning
	case job.StatusCompleted:
		status = models.StatusSucceeded
	case job.StatusFailed:
		status = models.StatusFailed
	case job.StatusCancelled:
		status = models.StatusCancelled
	}
	return &models.Job{
		ID:            j.ID,
		Type:          models.JobType(j.Type),
		Status:        status,
		Priority:      j.Priority,
		Project:       j.Project,
		Payload:       j.Payload,
		Result:        modelResult(j.Result),
		Error:         j.Error,
		Progress:      j.Progress,
		RetryCount:    j.RetryCount,
		MaxRetries:    j.MaxRetries,
		WorkerID:      j.WorkerID,
		LeaseID:       j.LeaseID,
		LeaseExpiry:   j.LeaseExpiry,
		Revision:      j.Revision,
		CorrelationID: j.CorrelationID,
		CreatedAt:     j.CreatedAt,
		UpdatedAt:     j.UpdatedAt,
		StartedAt:     j.StartedAt,
		CompletedAt:   j.CompletedAt,
	}
}

func modelResult(r json.RawMessage) map[string]any {
	var m map[string]any
	if len(r) > 0 && string(r) != "null" {
		_ = json.Unmarshal(r, &m)
	}
	return m
}

type RunnerConfig struct {
	Workers   int
	PollEvery time.Duration
	LeaseTTL  time.Duration
	JobTypes  []string
}

// Runner manages a pool of Workers. Depends on the domain job.Repository
// interface — NOT on the concrete *jobs.Repository.
type Runner struct {
	repo       job.Repository
	dispatcher *Dispatcher
	log        *zap.Logger
	config     RunnerConfig
	workers    []*Worker
}

func NewRunner(repo job.Repository, dispatcher *Dispatcher, log *zap.Logger, config RunnerConfig) *Runner {
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
		worker := NewWorker(workerID, r.repo, r.dispatcher, r.log, r.config.LeaseTTL, r.config.PollEvery, r.config.JobTypes)
		r.workers = append(r.workers, worker)
		go worker.Start(ctx)
	}

	r.log.Info("job runner started", zap.Int("worker_count", len(r.workers)))
}
