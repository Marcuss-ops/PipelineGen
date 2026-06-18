package jobs

import (
	"context"
	"fmt"
	"math/rand"
	"os"
	"sync"
	"time"

	"go.uber.org/zap"
	"github.com/Marcuss-ops/PipelineGen/internal/media/models"
	"github.com/Marcuss-ops/PipelineGen/internal/repository/jobs"
	"github.com/Marcuss-ops/PipelineGen/pkg/corid"
)

// jobTimeoutRegistry maps job types to their per-type timeout (punto 27).
// Protected by mu for concurrent safety — workers read via jobTimeout()
// while handlers may call SetJobTimeout() at init or runtime.
var (
	jobTimeoutRegistry = map[models.JobType]time.Duration{
		models.JobTypeCatalogSync:            2 * time.Minute,
		models.JobTypeSystemCleanup:          2 * time.Minute,
		models.JobTypeMediaReindex:           2 * time.Minute,
		models.JobTypeDriveFolderSync:        30 * time.Minute,
		models.JobTypeBatchScriptGenerate:    60 * time.Minute,
		models.JobTypeClipScriptGenerate:     60 * time.Minute,
		models.JobTypeCatalogScriptGenerate:  60 * time.Minute,
		models.JobTypeMediaStock:             60 * time.Minute,
		models.JobTypeYouTubeClipExtract:     60 * time.Minute,
		models.JobTypeBulkUploadYouTubeClips: 120 * time.Minute,
	}
	mu sync.RWMutex
)

// SetJobTimeout overrides the timeout for the given job type.
// Safe to call at any time — the map is protected by a mutex.
func SetJobTimeout(t models.JobType, d time.Duration) {
	mu.Lock()
	jobTimeoutRegistry[t] = d
	mu.Unlock()
}

func jobTimeout(t models.JobType) time.Duration {
	mu.RLock()
	d, ok := jobTimeoutRegistry[t]
	mu.RUnlock()
	if ok {
		return d
	}
	return 10 * time.Minute // default for unknown types
}

// workerID is computed once at startup and includes hostname + PID so
// that worker identifiers are globally unique across processes and
// machines (punto 25).
var workerIDPrefix string

func init() {
	host, err := os.Hostname()
	if err != nil {
		host = "unknown"
	}
	workerIDPrefix = fmt.Sprintf("%s_%d", host, os.Getpid())
}

// StartMetricsRefresher periodically recomputes queue depth and oldest-pending
// seconds gauges. Call once at startup alongside the job runner.
//
// Without this, metrics.JobQueueDepth and metrics.JobOldestPendingSeconds
// stay at zero forever — Prometheus dashboards would silently mislead.
func StartMetricsRefresher(ctx context.Context, repo MetricRefresher, interval time.Duration, log *zap.Logger) {
	if interval <= 0 {
		interval = 30 * time.Second
	}
	go func() {
		// Tick once immediately so the first scrape has data.
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

// MetricRefresher is satisfied by *jobs.Repository.
type MetricRefresher interface {
	RefreshMetrics(ctx context.Context) error
}

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

	// polling with jitter: base interval + random offset up to 25%
	// to avoid thundering-herd on the DB (punto 26).
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

	// Re-inject the correlation id stored on the job into the per-job
	// context, so any handler (and anything it calls: Ollama, Python
	// subprocess, repository) can pull it out via corid.FromContext and
	// stamp it on log lines. A single grep on the id will then surface
	// the full trace across processes.
	if job.CorrelationID != "" {
		ctx = corid.WithCorrelationID(ctx, job.CorrelationID)
	}

	w.log.Info("running job",
		zap.String("job_id", job.ID),
		zap.String("type", string(job.Type)),
		zap.String("correlation_id", job.CorrelationID),
	)

	// Per-job deadline derived from jobTimeoutRegistry (punto 27).
	jobCtx, jobCancel := context.WithTimeout(ctx, jobTimeout(job.Type))
	defer jobCancel()

	// Start lease renewal goroutine
	stopLease := make(chan struct{})
	go w.renewLeaseLoop(jobCtx, job.ID, stopLease)

	tools := &JobTools{
		Progress: func(progress int, message string) {
			if err := w.repo.SetProgress(jobCtx, job.ID, progress, message); err != nil {
				w.log.Warn("failed to report progress",
					zap.String("job_id", job.ID),
					zap.Int("progress", progress),
					zap.Error(err),
				)
			}
		},
		Event: func(eventType string, message string, data map[string]any) {
			if err := w.repo.AddEvent(jobCtx, job.ID, eventType, message, data); err != nil {
				w.log.Warn("failed to record event",
					zap.String("job_id", job.ID),
					zap.String("event_type", eventType),
					zap.Error(err),
				)
			}
		},
		IsCancelled: func() bool {
			j, err := w.repo.Get(jobCtx, job.ID)
			if err != nil {
				return false
			}
			return j.Status == models.StatusCancelled
		},
	}

	result, err := w.dispatcher.Dispatch(jobCtx, job, tools)

	// Stop lease renewal
	close(stopLease)

	// finalizationCtx is created fresh here (after Dispatch returns) so that
	// long-running jobs still get a full 30s window for the DB write.
	// Previously it was created at the top of runJob and expired before
	// jobs that ran longer than 30s could be finalised.
	finalizationCtx, finalCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer finalCancel()

	if err != nil {
		w.log.Error("job failed",
			zap.String("job_id", job.ID),
			zap.Error(err),
		)

		if job.RetryCount < job.MaxRetries {
			// Exponential backoff: 2s, 4s, 8s, ... capped at 30s.
			backoff := time.Duration(1<<job.RetryCount) * 2 * time.Second
			if backoff > 30*time.Second {
				backoff = 30 * time.Second
			}
			w.log.Info("marking job for retry",
				zap.String("job_id", job.ID),
				zap.Duration("backoff", backoff),
			)
			// Context-aware wait: respect cancellation/shutdown instead of
			// blocking the worker for up to 30s.
			select {
			case <-ctx.Done():
				w.log.Info("retry backoff interrupted by cancellation",
					zap.String("job_id", job.ID),
					zap.Error(ctx.Err()),
				)
				if failErr := w.repo.Fail(finalizationCtx, job.ID, "backoff cancelled: "+err.Error()); failErr != nil {
					w.log.Error("failed to mark job as failed after backoff cancelled",
						zap.String("job_id", job.ID),
						zap.Error(failErr),
					)
				}
				return
			case <-time.After(backoff):
			}
			if failErr := w.repo.Fail(finalizationCtx, job.ID, err.Error()); failErr != nil {
				w.log.Error("failed to mark job as failed before retry",
					zap.String("job_id", job.ID),
					zap.Error(failErr),
				)
			}
			_, retryErr := w.repo.Retry(finalizationCtx, job.ID)
			if retryErr != nil {
				w.log.Warn("failed to retry job", zap.String("job_id", job.ID), zap.Error(retryErr))
			}
			return
		}

		// Exhausted retries: dead-letter the job for manual inspection.
		if failErr := w.repo.Fail(finalizationCtx, job.ID, err.Error()); failErr != nil {
			w.log.Error("failed to mark job as failed after retries exhausted",
				zap.String("job_id", job.ID),
				zap.Error(failErr),
			)
		}
		if dlqErr := w.repo.DeadLetter(finalizationCtx, job.ID, err.Error()); dlqErr != nil {
			w.log.Warn("failed to dead-letter job", zap.String("job_id", job.ID), zap.Error(dlqErr))
		} else {
			w.log.Warn("job moved to dead letter queue",
				zap.String("job_id", job.ID),
				zap.Int("retry_count", job.RetryCount),
				zap.Error(err),
			)
		}
		return
	}

	if completeErr := w.repo.Complete(finalizationCtx, job.ID, result); completeErr != nil {
		w.log.Error("failed to mark job as completed",
			zap.String("job_id", job.ID),
			zap.Error(completeErr),
		)
	} else {
		w.log.Info("job completed", zap.String("job_id", job.ID))
	}
}

func (w *Worker) renewLeaseLoop(ctx context.Context, jobID string, stop <-chan struct{}) {
	// Derive renewal interval from leaseTTL so that short TTLs get
	// frequent renewals and long TTLs don't hammer the DB (punto 24).
	// A worker that owns a 5-minute lease will attempt renewal every
	// 100 seconds; a 30-second lease every 10 seconds.
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
