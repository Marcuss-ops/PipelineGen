package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"os"
	"time"

	"go.uber.org/zap"

	job "github.com/Marcuss-ops/PipelineGen/internal/domain/job"
	sqljobs "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/jobs"
	metrics "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/observability"
	corid "github.com/Marcuss-ops/PipelineGen/pkg/corid"
)

// ── HC-1 (June 2026): typed Registry-based timeout lookup ──────────────────
//
// The pre-HC-1 worker.go carried a package-level global `var jobTimeoutRegistry`
// (a `map[JobType]time.Duration`) and exported `SetJobTimeout(t, d)` +
// a `jobTimeout(t)` helper protected by a `sync.RWMutex`. HC-1 removes the
// global in favour of a typed Registry on the Worker: composition root
// calls `WithRegistry(jobs.Compose())` (or any TimeoutResolver port) and
// the runJob path looks up `j.Type` in the snapshot `timeouts TimeoutMap`.
//
// Anti-reintro gate: Check 40 in scripts/ci-architectural-checks.sh
// fails CI on any new `var jobTimeoutRegistry = ` / `SetJobTimeout(`
// caller / `jobTimeout(` helper usage.

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

// BackoffConfig is the per-Worker polling backoff sub-struct
// (PR-Polling / ADR-0002 §D6.5, June 2026). All fields are config-driven
// from JobsConfig.PollMaxBackoff / PollJitterFraction /
// PollConsecutiveEmptyBeforeBackoff.
type BackoffConfig struct {
	// MaxBackoff caps the exponential-backoff curve. Worker poll
	// intervals grow up to this cap and stay there until a wake
	// arrives or a non-empty claim resets to BaseInterval. Default
	// 60s — matches the qdrant-stale-cleaner historical cadence
	// and bounds the worst-case latency between Wake → Claim.
	MaxBackoff time.Duration

	// JitterFraction is the FULL-JITTER factor (AWS pattern).
	// actualSleep = rand(0, currentBackoff) every iteration, which
	// spreads thundering-herd wake-ups across the worker pool when a
	// burst of Enqueues lands. 0.0 = no jitter (deterministic burn
	// of full backoff); 1.0 = uniform [0, currentBackoff] jitter.
	// Default 0.5.
	JitterFraction float64

	// ConsecutiveEmptyThreshold is the number of CONSECUTIVE empty
	// Claims Workers tolerate before escalating the backoff curve.
	// Below the threshold: stay at BaseInterval. Above: backoff
	// doubles every subsequent empty claim (capped at MaxBackoff).
	// 0 disables the escalation entirely (workers stay at
	// BaseInterval forever — the legacy behaviour, useful as an
	// emergency unblock toggle).
	ConsecutiveEmptyThreshold int
}

// Worker polls the domain Repository for queued jobs and dispatches
// them to registered handlers. It depends on the domain Repository
// interface, NOT on the concrete *jobs.Repository.
//
// Polling surface (PR-Polling / ADR §D6.5):
//   - BaseInterval is the canonical PollEvery (the first-claim cadence
//     + the post-successful-claim reset cadence). Set by the runner
//     via RunnerConfig.PollEvery.
//   - MaxBackoff / JitterFraction / ConsecutiveEmptyThreshold are the
//     backoff knobs (RunnerConfig.Backoff). Together they implement
//     the exponential-backoff state machine: idle Workers sleep for
//     exponentially-growing intervals (full-jitter spread, capped at
//     MaxBackoff) until a Wake-side Broadcast on the QueueNotifier
//     closes their sleep channel.
//   - notifier drive the wake-on-Enqueue (Subscribe() per iteration;
//     channel close = Broadcast on Enqueue / Retry / RequeueExpired).
//
// HC-1 (June 2026) additions:
//   - reg     *Registry      — the typed config-port for per-job-type
//                              execution timeouts. Set via WithRegistry()
//                              at composition time. If nil, the worker
//                              falls back to the canonical 10-minute
//                              default for every job type.
//   - timeouts TimeoutMap    — cached snapshot of reg.Compose() taken
//                              at WithRegistry() time. The worker's
//                              runJob path indexes this map by j.Type;
//                              a zero value falls through to the
//                              canonical default.
type Worker struct {
	id        string
	repo      job.Store
	dispatcher *Dispatcher
	log       *zap.Logger
	leaseTTL  time.Duration
	pollEvery time.Duration
	backoff   BackoffConfig
	types     []string
	notifier  QueueNotifier
	reg       *Registry
	timeouts  TimeoutMap
}

// NewWorker constructs a Worker.
//
// PR-Polling signature change vs the pre-PR-Poll shape (June 2026):
//   - `notifier QueueNotifier` is the new wake-on-Enqueue port.
//   - `pollEvery time.Duration` is the BASE interval (preserved from
//     the original signature).
//   - `backoff BackoffConfig` is the new arg carrying MaxBackoff /
//     JitterFraction / ConsecutiveEmptyThreshold (was a single
//     time.Duration; now a sub-struct to keep the function signature
//     flat, ≤8 args per PR-D cap).
//   - `types []string` is unchanged.
//
// HC-1 (June 2026): the per-job-type timeout lookup is NOT a constructor
// arg — callers must use `WithRegistry(reg *Registry) *Worker` to
// attach the typed config-port. This keeps NewWorker under the PR-D
// 8-arg cap. Composition root (internal/app/registry.go::WireRegistry)
// is required to call WithRegistry(jobs.Compose()) AFTER NewWorker.
//
// Callers MUST pass a non-nil notifier; the worker pulls a fresh
// channel each iteration. Today the production wiring passes the
// in-process *SQLiteStore (the compile-time assertion in
// notifier.go::var _ QueueNotifier = (*sqljobs.SQLiteStore)(nil)
// is the seam marker for a future adapter).
func NewWorker(id string, repo job.Store, dispatcher *Dispatcher, notifier QueueNotifier,
	log *zap.Logger, leaseTTL, pollEvery time.Duration, backoff BackoffConfig, types []string) *Worker {
	return &Worker{
		id:         id,
		repo:       repo,
		dispatcher: dispatcher,
		log:        log,
		leaseTTL:   leaseTTL,
		pollEvery:  pollEvery,
		backoff:    backoff,
		types:      types,
		notifier:   notifier,
	}
}

// WithRegistry attaches a typed Registry to the Worker for per-job-type
// execution timeouts (HC-1, June 2026). Replaces the pre-HC-1
// package-level `var jobTimeoutRegistry` global, which was process-
// global mutable state. The worker snapshots reg.Compose() at attach
// time; a frozen Registry means the snapshot is also frozen, so the
// per-job lookup is branch-free (`timeouts[j.Type]`).
//
// Composition root pattern:
//
//	w := jobs.NewWorker(...).WithRegistry(jobs.Compose())
//
// Nil-tolerant: if reg is nil, the worker falls back to the canonical
// 10-minute default for every job type. This preserves the legacy
// "no timeouts configured" behaviour that test fixtures used to rely
// on; production wiring ALWAYS supplies jobs.Compose().
//
// Returns the receiver to allow builder-style chaining at the
// composition site.
func (w *Worker) WithRegistry(reg *Registry) *Worker {
	w.reg = reg
	if reg != nil {
		w.timeouts = reg.Compose()
	} else {
		w.timeouts = nil
	}
	return w
}

// jobTimeoutFor returns the cached timeout for a job type, falling
// back to the canonical 10-minute default when (a) the worker has no
// attached registry, (b) the snapshot is nil, or (c) the job type is
// not registered. Mirrors the pre-HC-1 jobTimeout() helper semantics
// without the global mutex.
func (w *Worker) jobTimeoutFor(jobType string) time.Duration {
	if w.timeouts != nil {
		if d, ok := w.timeouts[jobType]; ok && d > 0 {
			return d
		}
	}
	return 10 * time.Minute
}

// Start runs the Worker poll loop until ctx is cancelled.
//
// State machine (PR-Polling / ADR §D6.5):
//  1. Initial sleep = pollEvery + jitter  (spreads Worker startup).
//  2. Loop:
//     a. ClaimNext; if err → sleep at BaseInterval (errors don't escalate).
//     b. (nil, nil) → empty; consecutiveEmpty++; if exceeds the
//     backoff threshold, double the backoff (capped at MaxBackoff)
//     with full-jitter sleep on the next iteration.
//     c. Non-nil lease → reset backoff to BaseInterval, dispatch.
//  3. Each sleep blocks on ctx.Done, notifier.Subscribe() wake, or
//     the jittered backoff timer — whichever fires first.
//
// Acceptance:
//   - After N consecutive empty claims (N = backoff.ConsecutiveEmptyThreshold),
//     the polling interval grows; capped at MaxBackoff. Idle CPU drops to
//     ~0% while sleeping.
//   - Enqueue / Retry / RequeueExpiredLeases trigger Broadcast on the
//     concrete notifier; the sleeping select wakes immediately and
//     Workers resume polling at the BaseInterval (backoff is reset on
//     the next successful claim).
func (w *Worker) Start(ctx context.Context) {
	w.log.Info("worker started",
		zap.String("worker_id", w.id),
		zap.Duration("base_poll_every", w.pollEvery),
		zap.Duration("max_backoff", w.backoff.MaxBackoff),
		zap.Int("consecutive_empty_threshold", w.backoff.ConsecutiveEmptyThreshold),
	)

	// Backoff state machine. Reset to base on successful claim;
	// grow on empty claims past the threshold.
	currentBackoff := w.pollEvery
	consecutiveEmpty := 0

	// Initial jitter to spread Worker-goroutine startup.
	jitterInitial := jitterDuration(w.pollEvery/4, 1.0)
	if !w.sleepBackoff(ctx, w.pollEvery+jitterInitial) {
		w.log.Info("worker stopped (ctx before first claim)",
			zap.String("worker_id", w.id))
		return
	}

	for {
		if ctx.Err() != nil {
			w.log.Info("worker stopped",
				zap.String("worker_id", w.id),
				zap.Duration("current_backoff", currentBackoff),
				zap.Int("consecutive_empty", consecutiveEmpty))
			return
		}

		j, err := w.repo.ClaimNext(ctx, w.id, w.leaseTTL, w.types)
		if err != nil {
			w.log.Error("failed to claim next job", zap.Error(err))
			// Errors do NOT escalate backoff — the broker is presumed
			// transient. Sleep at BaseInterval.
			if !w.sleepBackoff(ctx, w.effectiveSleep(w.pollEvery)) {
				w.log.Info("worker stopped", zap.String("worker_id", w.id))
				return
			}
			continue
		}

		if j == nil {
			consecutiveEmpty++
			metrics.WorkerIdleTicksTotal.Inc()

			// Escalate backoff ONLY when threshold exceeded AND
			// escalation is enabled (threshold > 0). 0 = disabled
			// (legacy behaviour: stay at BaseInterval forever).
			if w.backoff.ConsecutiveEmptyThreshold > 0 &&
				consecutiveEmpty > w.backoff.ConsecutiveEmptyThreshold {
				prev := currentBackoff
				next := prev * 2
				if next > w.backoff.MaxBackoff {
					next = w.backoff.MaxBackoff
				}
				if next > prev {
					metrics.WorkerBackoffEventsTotal.Inc()
					currentBackoff = next
					w.log.Debug("worker backoff escalated",
						zap.String("worker_id", w.id),
						zap.Int("consecutive_empty", consecutiveEmpty),
						zap.Duration("from", prev),
						zap.Duration("to", next))
				}
			}

			if !w.sleepBackoff(ctx, w.effectiveSleep(currentBackoff)) {
				w.log.Info("worker stopped", zap.String("worker_id", w.id))
				return
			}
			continue
		}

		// Successful claim — reset backoff state.
		if consecutiveEmpty > 0 || currentBackoff != w.pollEvery {
			w.log.Debug("worker backoff reset on successful claim",
				zap.String("worker_id", w.id),
				zap.Int("previous_consecutive_empty", consecutiveEmpty),
				zap.Duration("previous_backoff", currentBackoff))
		}
		consecutiveEmpty = 0
		currentBackoff = w.pollEvery

		w.runJob(ctx, j)
	}
}

// effectiveSleep applies the JitterFraction as full-jitter on the base
// sleep duration. 0.0 jitter ⇒ deterministic burn; 1.0 ⇒ uniform
// rand(0, base). Negative or >1 are clamped to keep the math safe.
func (w *Worker) effectiveSleep(base time.Duration) time.Duration {
	if base <= 0 {
		base = w.pollEvery
	}
	return jitterDuration(base, w.backoff.JitterFraction)
}

// sleepBackoff blocks for `d` OR wakes on the notifier's wake channel
// OR returns false on ctx cancellation. The notifier subscription is
// refreshed on every call so the post-Broadcast replacement channel is
// the one observed by the next sleep iteration (close-and-replace
// invariant).
func (w *Worker) sleepBackoff(ctx context.Context, d time.Duration) bool {
	if d <= 0 {
		d = w.pollEvery
	}
	wakeCh := w.notifier.Subscribe()
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-wakeCh:
		metrics.WorkerWakeOnEnqueueTotal.Inc()
		w.log.Debug("worker woke on enqueue broadcast",
			zap.String("worker_id", w.id))
		return true
	case <-timer.C:
		return true
	}
}

// jitterDuration adds full-jitter to a base duration: actual = rand(0, base).
// AWS-style full-jitter is the canonical exponential-backoff spread
// (https://aws.amazon.com/blogs/architecture/exponential-backoff-and-jitter/);
// on a saturated queue the spread prevents each Worker from burning the
// next full Backoff identical-interval sleeping in lockstep.
func jitterDuration(base time.Duration, jitter float64) time.Duration {
	if base <= 0 {
		return base
	}
	if jitter < 0 {
		jitter = 0
	} else if jitter > 1 {
		jitter = 1
	}
	delta := int64(float64(base) * jitter)
	if delta <= 0 {
		return base
	}
	return time.Duration(rand.Int63n(delta + 1))
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

	// HC-1 (June 2026): per-job-type timeout resolves through the
	// typed Registry attached via WithRegistry(). Replaces the
	// pre-HC-1 `context.WithTimeout(ctx, jobTimeout(j.Type))` call
	// which read from a package-level `var jobTimeoutRegistry` map.
	jobCtx, jobCancel := context.WithTimeout(ctx, w.jobTimeoutFor(j.Type))
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
