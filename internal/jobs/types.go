package jobs

import (
	"context"
	"fmt"
	"time"

	"go.uber.org/zap"
	"velox/go-master/internal/repository/jobs"
	"velox/go-master/internal/media/models"
)

type EnqueueRequest struct {
	Type       models.JobType `json:"type"`
	Project    string         `json:"project,omitempty"`
	VideoName  string         `json:"video_name,omitempty"`
	Payload    map[string]any `json:"payload"`
	Priority   int            `json:"priority,omitempty"`
	MaxRetries int            `json:"max_retries,omitempty"`
	ActiveKey  string         `json:"active_key,omitempty"`
}

type JobTools struct {
	Progress    func(progress int, message string)
	Event       func(eventType string, message string, data map[string]any)
	IsCancelled func() bool
}

type HandlerFunc func(ctx context.Context, job *models.Job, tools *JobTools) (map[string]any, error)

type Dispatcher struct {
	handlers map[models.JobType]HandlerFunc
}

func NewDispatcher() *Dispatcher {
	return &Dispatcher{handlers: make(map[models.JobType]HandlerFunc)}
}

func (d *Dispatcher) Register(jobType models.JobType, handler HandlerFunc) {
	d.handlers[jobType] = handler
}

func (d *Dispatcher) Dispatch(ctx context.Context, job *models.Job, tools *JobTools) (map[string]any, error) {
	handler, ok := d.handlers[job.Type]
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
		workerID := fmt.Sprintf("worker-%d", i+1)
		worker := NewWorker(workerID, r.repo, r.dispatcher, r.log, r.config.LeaseTTL, r.config.PollEvery, r.config.JobTypes)
		r.workers = append(r.workers, worker)
		go worker.Start(ctx)
	}

	r.log.Info("job runner started", zap.Int("worker_count", len(r.workers)))
}
