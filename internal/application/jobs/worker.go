package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"os"
	"sync"
	"time"

	"go.uber.org/zap"

	job "github.com/Marcuss-ops/PipelineGen/internal/domain/job"
	sqljobs "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/jobs"
	corid "github.com/Marcuss-ops/PipelineGen/pkg/corid"
)

// JobType is a string alias for the timeout registry. Passaggio 6 will
// migrate this to the canonical domain types.
type JobType = string

// jobTimeoutRegistry maps job types to their per-type timeout (punto 27).
var (
	jobTimeoutRegistry = map[JobType]time.Duration{
		"catalog.sync":                    2 * time.Minute,
		"system.cleanup":                  2 * time.Minute,
		"media.reindex":                   2 * time.Minute,
		"drive.folder.sync":               30 * time.Minute,
		"script.generate_batch":           60 * time.Minute,
		"script.generate_from_clips":      60 * time.Minute,
		"script.generate_from_catalog":    60 * time.Minute,
		"media.stock":                     60 * time.Minute,
		"youtube_clip.extract":            60 * time.Minute,
		"media.bulk_upload_youtube_clips": 120 * time.Minute,
	}
	mu sync.RWMutex
)

func SetJobTimeout(t string, d time.Duration) {
	mu.Lock()
	jobTimeoutRegistry[t] = d
	mu.Unlock()
}

func jobTimeout(t string) time.Duration {
	mu.RLock()
	d, ok := jobTimeoutRegistry[t]
	mu.RUnlock()
	if ok {
		return d
	}
	return 10 * time.Minute
}

var workerIDPrefix string

func init() {
	host, err := os.Hostname()
	if err != nil {
		host = "unknown"
	}
	workerIDPrefix = fmt.Sprintf("%s_%d", host, os.Getpid())
}

// MetricRefresher is satisfied by the concrete jobs.Repository.
type MetricRefresher interface {
	RefreshMetrics(ctx context.Context) error
}

func StartMetricsRefresher(ctx context.Context, repo MetricRefresher, interval time.Duration, log *zap.Logger) {
	if interval <= 0 {
		interval = 30 * time.Second
	}
	go func() {
		if err := repo.RefreshMetrics(ctx); err != nil {
			log.Warn("metrics refresh failed (immediate tick)", zap.Error(err))
		}
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if err := repo.RefreshMetrics(ctx); err != nil {
					log.Warn("metrics refresh failed", zap.Error(err))
				}
			}
		}
	}()
}

// Worker polls the domain Repository for queued jobs and dispatches
// them to registered handlers. It depends on the domain Repository
// interface, NOT on the concrete *jobs.Repository.
type Worker struct {
	id         string
	repo       job.Store
	dispatcher *Dispatcher
	log        *zap.Logger
	leaseTTL   time.Duration
	pollEvery  time.Duration
	types      []string
}

func NewWorker(id string, repo job.Store, dispatcher *Dispatcher, log *zap.Logger,
	leaseTTL, pollEvery time.Duration, types []string) *Worker {
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

	jitterDuration := time.Duration(rand.Int63n(int64(w.pollEvery) / 4))
	ticker := time.NewTicker(w.pollEvery + jitterDuration)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			w.log.Info("worker stopped", zap.String("worker_id", w.id))
			return
		default:
		}

		j, err := w.repo.ClaimNext(ctx, w.id, w.leaseTTL, w.types)
		if err != nil {
			w.log.Error("failed to claim next job", zap.Error(err))
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
			continue
		}

		if j == nil {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
			continue
		}

		w.runJob(ctx, j)
	}
}

func (w *Worker) runJob(parent context.Context, j *job.Job) {
	ctx, cancel := context.WithCancel(parent)
	defer cancel()

	if j.CorrelationID != "" {
		ctx = corid.WithCorrelationID(ctx, j.CorrelationID)
	}

	w.log.Info("running job",
		zap.String("job_id", j.ID),
		zap.String("type", j.Type),
		zap.String("correlation_id", j.CorrelationID),
		zap.String("lease_id", j.LeaseID),
		zap.Int("revision", j.Revision),
	)

	jobCtx, jobCancel := context.WithTimeout(ctx, jobTimeout(j.Type))
	defer jobCancel()

	// Lease renewal.
	stopLease := make(chan struct{})
	leaseDone := make(chan struct{})
	go w.renewLeaseLoop(jobCtx, j.ID, stopLease, leaseDone)

	// Snapshot lease tokens for finalisation.
	workerID := w.id
	leaseID := j.LeaseID
	revision := j.Revision

	tools := &JobTools{
		Progress: func(progress int, message string) {
			if err := w.repo.SetProgress(jobCtx, j.ID, progress, message); err != nil {
				w.log.Warn("failed to report progress",
					zap.String("job_id", j.ID),
					zap.Int("progress", progress),
					zap.Error(err))
			}
		},
		Event: func(eventType string, message string, data map[string]any) {
			if err := w.repo.AddEvent(jobCtx, j.ID, eventType, message, data); err != nil {
				w.log.Warn("failed to record event",
					zap.String("job_id", j.ID),
					zap.String("event_type", eventType),
					zap.Error(err))
			}
		},
		IsCancelled: func() bool {
			domJob, err := w.repo.Get(jobCtx, j.ID)
			if err != nil {
				return false
			}
			return domJob != nil && domJob.Status == job.StatusCancelled
		},
	}

	result, dispatchErr := w.dispatcher.Dispatch(jobCtx, j, tools)

	close(stopLease)
	<-leaseDone

	finalizationCtx, finalCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer finalCancel()

	if dispatchErr != nil {
		w.log.Error("job failed",
			zap.String("job_id", j.ID),
			zap.Error(dispatchErr))

		if j.RetryCount < j.MaxRetries {
			backoff := time.Duration(1<<j.RetryCount) * 2 * time.Second
			if backoff > 30*time.Second {
				backoff = 30 * time.Second
			}
			w.log.Info("scheduling job for retry",
				zap.String("job_id", j.ID),
				zap.Duration("backoff", backoff))

			// ScheduleRetry does running→queued atomically with
			// server-side backoff via available_at. No intermediate
			// "failed" state — avoids false alerting.
			if retryErr := w.repo.ScheduleRetry(finalizationCtx, j.ID, workerID, leaseID, revision, backoff); retryErr != nil {
				if errors.Is(retryErr, sqljobs.ErrLeaseLost) {
					w.log.Warn("lease lost during ScheduleRetry — another worker claimed this job",
						zap.String("job_id", j.ID))
				} else {
					w.log.Error("failed to schedule retry",
						zap.String("job_id", j.ID),
						zap.Error(retryErr))
				}
			}
			return
		}

		if failErr := w.repo.Fail(finalizationCtx, j.ID, workerID, leaseID, revision, dispatchErr.Error()); failErr != nil {
			if errors.Is(failErr, sqljobs.ErrLeaseLost) {
				w.log.Warn("lease lost during fail (exhausted retries)",
					zap.String("job_id", j.ID))
			} else {
				w.log.Error("failed to mark job as failed",
					zap.String("job_id", j.ID),
					zap.Error(failErr))
			}
		}
		if dlqErr := w.repo.DeadLetter(finalizationCtx, j.ID, dispatchErr.Error()); dlqErr != nil {
			w.log.Warn("failed to dead-letter job", zap.String("job_id", j.ID), zap.Error(dlqErr))
		} else {
			w.log.Warn("job moved to dead letter queue",
				zap.String("job_id", j.ID),
				zap.Int("retry_count", j.RetryCount),
				zap.Error(dispatchErr))
		}
		return
	}

	if completeErr := w.repo.Complete(finalizationCtx, j.ID, workerID, leaseID, revision, mapToRawMessage(result)); completeErr != nil {
		if errors.Is(completeErr, sqljobs.ErrLeaseLost) {
			w.log.Warn("lease lost during complete — another worker claimed this job",
				zap.String("job_id", j.ID))
		} else {
			w.log.Error("failed to mark job as completed",
				zap.String("job_id", j.ID),
				zap.Error(completeErr))
		}
	} else {
		w.log.Info("job completed", zap.String("job_id", j.ID))
	}
}

func (w *Worker) renewLeaseLoop(ctx context.Context, jobID string, stop <-chan struct{}, done chan<- struct{}) {
	defer close(done)
	interval := w.leaseTTL / 3
	if interval < 5*time.Second {
		interval = 5 * time.Second
	}
	ticker := time.NewTicker(interval)
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

func mapToRawMessage(m map[string]any) json.RawMessage {
	if m == nil {
		return json.RawMessage("{}")
	}
	b, err := json.Marshal(m)
	if err != nil {
		return json.RawMessage("{}")
	}
	return b
}
