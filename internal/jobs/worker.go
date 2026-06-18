package jobs

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"os"
	"sync"
	"time"

	"go.uber.org/zap"
	"github.com/Marcuss-ops/PipelineGen/internal/media/models"
	jobsrepo "github.com/Marcuss-ops/PipelineGen/internal/repository/jobs"
	"github.com/Marcuss-ops/PipelineGen/pkg/corid"
	"github.com/Marcuss-ops/PipelineGen/pkg/hashutil"
)

// jobContext holds the running job's lease and revision state.
// The renewLeaseLoop updates revision via the mutex; the main
// job goroutine reads it for the final Complete/Fail call.
type jobContext struct {
	job      *models.Job
	leaseID  string
	revision int64
	mu       sync.Mutex
}

func (jc *jobContext) getRevision() int64 {
	jc.mu.Lock()
	defer jc.mu.Unlock()
	return jc.revision
}

func (jc *jobContext) setRevision(r int64) {
	jc.mu.Lock()
	defer jc.mu.Unlock()
	jc.revision = r
}

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
	repo       *jobsrepo.Repository
	dispatcher *Dispatcher
	log        *zap.Logger
	leaseTTL   time.Duration
	pollEvery  time.Duration
	types      []models.JobType
}

func NewWorker(id string, repo *jobsrepo.Repository, dispatcher *Dispatcher, log *zap.Logger,
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

		leas, err := w.claimAndStart(ctx)
		if err != nil {
			w.log.Error("failed to claim next job", zap.Error(err))
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
			continue
		}

		if leas == nil {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
			continue
		}

		w.runJob(ctx, leas)
	}
}

// claimAndStart claims the next PENDING job (PENDING→LEASED) then starts
// it (LEASED→RUNNING). Returns the job context with lease tracking, or
// nil if no jobs are available.
func (w *Worker) claimAndStart(ctx context.Context) (*jobContext, error) {
	leaseID := fmt.Sprintf("lease_%d_%s", time.Now().UnixNano(), hashutil.RandomString(8))

	leas, err := w.repo.ClaimNext(ctx, jobsrepo.ClaimNext{
		WorkerID: w.id,
		LeaseID:  leaseID,
		LeaseTTL: w.leaseTTL,
		Types:    w.types,
	})
	if err != nil {
		if err == jobsrepo.ErrAlreadyClaimed {
			return nil, nil // another worker raced us — retry next poll
		}
		return nil, err
	}
	if leas == nil || leas.Job == nil {
		return nil, nil
	}

	// Start: LEASED → RUNNING (sets started_at, extends lease)
	started, err := w.repo.Start(ctx, jobsrepo.StartJob{
		JobID:    leas.Job.ID,
		WorkerID: w.id,
		LeaseID:  leas.LeaseID,
		LeaseTTL: w.leaseTTL,
		Revision: int64(leas.Job.Revision),
	})
	if err != nil {
		return nil, fmt.Errorf("start job %s: %w", leas.Job.ID, err)
	}

	return &jobContext{
		job:      started,
		leaseID:  leas.LeaseID,
		revision: int64(started.Revision),
	}, nil
}

func (w *Worker) runJob(parent context.Context, jc *jobContext) {
	ctx, cancel := context.WithCancel(parent)
	defer cancel()

	// Re-inject the correlation id stored on the job into the per-job
	// context, so any handler (and anything it calls: Ollama, Python
	// subprocess, repository) can pull it out via corid.FromContext and
	// stamp it on log lines. A single grep on the id will then surface
	// the full trace across processes.
	if jc.job.CorrelationID != "" {
		ctx = corid.WithCorrelationID(ctx, jc.job.CorrelationID)
	}

	w.log.Info("running job",
		zap.String("job_id", jc.job.ID),
		zap.String("type", string(jc.job.Type)),
		zap.String("correlation_id", jc.job.CorrelationID),
	)

	// Per-job deadline derived from jobTimeoutRegistry (punto 27).
	jobCtx, jobCancel := context.WithTimeout(ctx, jobTimeout(jc.job.Type))
	defer jobCancel()

	// Start lease renewal goroutine (with done channel to synchronize
	// revision read after the goroutine fully exits).
	stopLease := make(chan struct{})
	leaseDone := make(chan struct{})
	go w.renewLeaseLoop(jobCtx, jc, stopLease, leaseDone)

	tools := &JobTools{
		Progress: func(progress int, message string) {
			if err := w.repo.SetProgress(jobCtx, jc.job.ID, progress, message); err != nil {
				w.log.Warn("failed to report progress",
					zap.String("job_id", jc.job.ID),
					zap.Int("progress", progress),
					zap.Error(err),
				)
			}
		},
		Event: func(eventType string, message string, data map[string]any) {
			if err := w.repo.AddEvent(jobCtx, jc.job.ID, eventType, message, data); err != nil {
				w.log.Warn("failed to record event",
					zap.String("job_id", jc.job.ID),
					zap.String("event_type", eventType),
					zap.Error(err),
				)
			}
		},
		IsCancelled: func() bool {
			j, err := w.repo.Get(jobCtx, jc.job.ID)
			if err != nil {
				return false
			}
			return j.Status == models.StatusCancelled
		},
	}

	result, err := w.dispatcher.Dispatch(jobCtx, jc.job, tools)

	// Stop lease renewal and wait for goroutine to fully exit.
	// The done channel guarantees that all in-flight RenewLease
	// calls have completed before we read the final revision.
	close(stopLease)
	<-leaseDone

	// finalizationCtx is created fresh here (after Dispatch returns) so that
	// long-running jobs still get a full 30s window for the DB write.
	// Previously it was created at the top of runJob and expired before
	// jobs that ran longer than 30s could be finalised.
	finalizationCtx, finalCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer finalCancel()

	rev := jc.getRevision()

	if err != nil {
		w.log.Error("job failed",
			zap.String("job_id", jc.job.ID),
			zap.Error(err),
		)

		if jc.job.RetryCount < jc.job.MaxRetries {
			// Exponential backoff: 2s, 4s, 8s, ... capped at 30s.
			backoff := time.Duration(1<<jc.job.RetryCount) * 2 * time.Second
			if backoff > 30*time.Second {
				backoff = 30 * time.Second
			}
			w.log.Info("marking job for retry",
				zap.String("job_id", jc.job.ID),
				zap.Duration("backoff", backoff),
			)
			// Context-aware wait: respect cancellation/shutdown instead of
			// blocking the worker for up to 30s.
			select {
			case <-ctx.Done():
				w.log.Info("retry backoff interrupted by cancellation",
					zap.String("job_id", jc.job.ID),
					zap.Error(ctx.Err()),
				)
				if _, failErr := w.repo.Fail(finalizationCtx, jobsrepo.FailJob{
					JobID:    jc.job.ID,
					WorkerID: w.id,
					LeaseID:  jc.leaseID,
					Revision: rev,
					Error:    "backoff cancelled: " + err.Error(),
				}); failErr != nil {
					w.log.Error("failed to mark job as failed after backoff cancelled",
						zap.String("job_id", jc.job.ID),
						zap.Error(failErr),
					)
				}
				return
			case <-time.After(backoff):
			}
			if _, failErr := w.repo.Fail(finalizationCtx, jobsrepo.FailJob{
				JobID:    jc.job.ID,
				WorkerID: w.id,
				LeaseID:  jc.leaseID,
				Revision: rev,
				Error:    err.Error(),
			}); failErr != nil {
				w.log.Error("failed to mark job as failed before retry",
					zap.String("job_id", jc.job.ID),
					zap.Error(failErr),
				)
			}
			if _, retryErr := w.repo.ScheduleRetry(finalizationCtx, jobsrepo.ScheduleRetry{
				JobID:    jc.job.ID,
				WorkerID: w.id,
				LeaseID:  jc.leaseID,
				Revision: rev,
			}); retryErr != nil {
				w.log.Warn("failed to retry job", zap.String("job_id", jc.job.ID), zap.Error(retryErr))
			}
			return
		}

		// Exhausted retries: dead-letter the job for manual inspection.
		if _, failErr := w.repo.Fail(finalizationCtx, jobsrepo.FailJob{
			JobID:    jc.job.ID,
			WorkerID: w.id,
			LeaseID:  jc.leaseID,
			Revision: rev,
			Error:    err.Error(),
		}); failErr != nil {
			w.log.Error("failed to mark job as failed after retries exhausted",
				zap.String("job_id", jc.job.ID),
				zap.Error(failErr),
			)
		}
		if dlqErr := w.repo.DeadLetter(finalizationCtx, jc.job.ID, err.Error()); dlqErr != nil {
			w.log.Warn("failed to dead-letter job", zap.String("job_id", jc.job.ID), zap.Error(dlqErr))
		} else {
			w.log.Warn("job moved to dead letter queue",
				zap.String("job_id", jc.job.ID),
				zap.Int("retry_count", jc.job.RetryCount),
				zap.Error(err),
			)
		}
		return
	}

	if _, completeErr := w.repo.Complete(finalizationCtx, jobsrepo.CompleteJob{
		JobID:    jc.job.ID,
		WorkerID: w.id,
		LeaseID:  jc.leaseID,
		Revision: rev,
		ResultJSON: marshalResult(result),
	}); completeErr != nil {
		w.log.Error("failed to mark job as completed",
			zap.String("job_id", jc.job.ID),
			zap.Error(completeErr),
		)
	} else {
		w.log.Info("job completed", zap.String("job_id", jc.job.ID))
	}
}

func (w *Worker) renewLeaseLoop(ctx context.Context, jc *jobContext, stop <-chan struct{}, done chan<- struct{}) {
	defer close(done)
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
			renewed, err := w.repo.RenewLease(ctx, jobsrepo.RenewLease{
				JobID:         jc.job.ID,
				WorkerID:      w.id,
				LeaseID:       jc.leaseID,
				Revision:      jc.getRevision(),
				NewExpiration: time.Now().Add(w.leaseTTL),
			})
			if err != nil {
				w.log.Warn("failed to renew lease", zap.String("job_id", jc.job.ID), zap.Error(err))
			} else {
				jc.setRevision(int64(renewed.Revision))
			}
		}
	}
}

// marshalResult converts a result map to json.RawMessage.
func marshalResult(result map[string]any) []byte {
	if result == nil {
		return nil
	}
	b, _ := json.Marshal(result)
	return b
}
