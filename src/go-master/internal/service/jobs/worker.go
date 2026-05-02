package jobs

import (
	"context"
	"time"

	"go.uber.org/zap"
	"velox/go-master/internal/repository/jobs"
	"velox/go-master/pkg/models"
)

type Worker struct {
	id         string
	repo       *jobs.Repository
	dispatcher *Dispatcher
	log        *zap.Logger
	leaseTTL   time.Duration
	pollEvery  time.Duration
	types      []models.JobType
}

func NewWorker(id string, repo *jobs.Repository, dispatcher *Dispatcher, log *zap.Logger,
	leaseTTL, pollEvery time.Duration, types []models.JobType) *Worker {
	return &Worker{
		id:         id,
		repo:       repo,
		dispatcher: dispatcher,
		log:        log,
		leaseTTL:   leaseTTL,
		pollEvery:  pollEvery,
		types:      types,
	}
}

func (w *Worker) Start(ctx context.Context) {
	w.log.Info("worker started", zap.String("worker_id", w.id))

	ticker := time.NewTicker(w.pollEvery)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			w.log.Info("worker stopped", zap.String("worker_id", w.id))
			return
		default:
		}

		job, err := w.repo.ClaimNext(ctx, w.id, w.leaseTTL, w.types)
		if err != nil {
			w.log.Error("failed to claim next job", zap.Error(err))
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
			continue
		}

		if job == nil {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
			continue
		}

		w.runJob(ctx, job)
	}
}

func (w *Worker) runJob(parent context.Context, job *models.Job) {
	ctx, cancel := context.WithCancel(parent)
	defer cancel()

	w.log.Info("running job", zap.String("job_id", job.ID), zap.String("type", string(job.Type)))

	// Start lease renewal goroutine
	stopLease := make(chan struct{})
	go w.renewLeaseLoop(ctx, job.ID, stopLease)

	tools := &JobTools{
		Progress: func(progress int, message string) {
			_ = w.repo.SetProgress(ctx, job.ID, progress, message)
		},
		Event: func(eventType string, message string, data map[string]any) {
			_ = w.repo.AddEvent(ctx, job.ID, eventType, message, data)
		},
		IsCancelled: func() bool {
			j, err := w.repo.Get(ctx, job.ID)
			if err != nil {
				return false
			}
			return j.Status == models.StatusCancelled
		},
	}

	result, err := w.dispatcher.Dispatch(ctx, job, tools)

	// Stop lease renewal
	close(stopLease)
	if err != nil {
		w.log.Error("job failed", zap.String("job_id", job.ID), zap.Error(err))

		if job.RetryCount < job.MaxRetries {
			w.log.Info("marking job for retry", zap.String("job_id", job.ID))
			_ = w.repo.Fail(ctx, job.ID, err.Error())
			_, retryErr := w.repo.Retry(ctx, job.ID)
			if retryErr != nil {
				w.log.Warn("failed to retry job", zap.String("job_id", job.ID), zap.Error(retryErr))
			}
			return
		}

		_ = w.repo.Fail(ctx, job.ID, err.Error())
		return
	}

	_ = w.repo.Complete(ctx, job.ID, result)
	w.log.Info("job completed", zap.String("job_id", job.ID))
}

func (w *Worker) renewLeaseLoop(ctx context.Context, jobID string, stop <-chan struct{}) {
	ticker := time.NewTicker(60 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-stop:
			return
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := w.repo.RenewLease(ctx, jobID, w.id, w.leaseTTL); err != nil {
				w.log.Warn("failed to renew lease", zap.String("job_id", jobID), zap.Error(err))
			}
		}
	}
}
