// Package jobs — worker.go (PR7 SLIM ORCHESTRATOR, June 2026).
//
// Per-Worker constructor + lifecycle entrypoint. Owns:
//
//   - package init + workerIDPrefix (host/pid bootstrap)
//   - type Worker struct (the single-instance Worker state)
//   - func NewWorker (constructor)
//   - func (w *Worker) WithRegistry (HC-1 typed Registry attach)
//   - func (w *Worker) jobTimeoutFor (private helper reading the
//     HC-1 snapshot)
//   - func (w *Worker) Start (the outer poll loop with the backoff
//     state machine inline)
//   - func mapToRawMessage (free helper used by runJob on success
//     path; tiny enough to live in the bootstrap file as a stable
//     utility home)
//
// PR7 split relocated 5 categories of detail into same-package
// helpers (per the user's 6-file spec):
//
//   - worker_metrics.go        → MetricRefresher interface + StartMetricsRefresher free func
//   - worker_backoff.go        → BackoffConfig struct + effectiveSleep + jitterDuration
//   - worker_polling.go        → sleepBackoff (timer + notifier Subscribe + ticker block)
//   - worker_execution.go      → runJob (per-job dispatch + finalisation with
//     finalizationCtx = context.Background() + finalizationTimeout invariant)
//   - worker_lease.go          → renewLeaseLoop (lease renewal ticker)
//
// VINCOLI (PR7 rigid):
//  1. Stesso *Worker receiver — no new types/interfaces beyond what
//     the source already exports.
//  2. finalizationCtx invariant (worker_execution.go) MUST stay
//     `context.WithTimeout(context.Background(), finalizationTimeout)` —
//     AGENTS.md §context-util-table allowlist, allows the DB
//     final-state write AND the artifact-publication spine to survive
//     jobCtx cancellation.
//  3. No new abstraction helpers created (e.g. extracted
//     nextBackoff() / xmlLogEntry() etc. would violate the spec).
//
// ── HC-1 (June 2026): typed Registry-based timeout lookup ────────
//
// The pre-HC-1 worker.go carried a package-level global
// `var jobTimeoutRegistry` (a `map[JobType]time.Duration`) and
// exported `SetJobTimeout(t, d)` + a `jobTimeout(t)` helper
// protected by a `sync.RWMutex`. HC-1 removes the global in favour
// of a typed Registry on the Worker: composition root calls
// `WithRegistry(jobs.Compose())` (or any TimeoutResolver port) and
// the runJob path looks up `j.Type` in the snapshot `timeouts
// TimeoutMap`.
//
// Anti-reintro gate: Check 40 in scripts/ci-architectural-checks.sh
// fails CI on any new `var jobTimeoutRegistry = ` /
// `SetJobTimeout(` caller / `jobTimeout(` helper usage.
package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"

	"go.uber.org/zap"

	capjobregistry "github.com/Marcuss-ops/PipelineGen/internal/capabilities/jobregistry"
	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
	kernobs "github.com/Marcuss-ops/PipelineGen/internal/kernel/observability"
	metrics "github.com/Marcuss-ops/PipelineGen/internal/platform/observability"
	"github.com/Marcuss-ops/PipelineGen/pkg/retry"
)

var workerIDPrefix string

func init() {
	host, err := os.Hostname()
	if err != nil {
		host = "unknown"
	}
	workerIDPrefix = fmt.Sprintf("%s_%d", host, os.Getpid())
}

// Worker polls the domain Repository for queued jobs and dispatches
// them to registered handlers. It depends on the domain Repository
// interface, NOT on the concrete *jobs.Repository.
//
// Polling surface (PR-Polling / ADR §D6.5):
//   - BaseInterval is the canonical PollEvery (the first-claim cadence
//   - the post-successful-claim reset cadence). Set by the runner
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
//     execution timeouts. Set via WithRegistry()
//     at composition time. If nil, the worker
//     falls back to the canonical 10-minute
//     default for every job type.
//   - timeouts TimeoutMap    — cached snapshot of reg.Compose() taken
//     at WithRegistry() time. The worker's
//     runJob path indexes this map by j.Type;
//     a zero value falls through to the
//     canonical default.
type Worker struct {
	id         string
	repo       job.Store
	dispatcher *Dispatcher
	log        *zap.Logger
	leaseTTL   time.Duration
	pollEvery  time.Duration
	backoff    BackoffConfig
	types      []string
	notifier   QueueNotifier
	reg        *Registry
	timeouts   TimeoutMap

	// broker is the typed narrow port (CompletionPort) consumed by
	// the Worker for artifact-producing job finalization. nil = legacy
	// w.repo.Complete path; non-nil = route ProducesArtifacts=true
	// jobs through broker.CompleteWithArtifacts per
	// PR-WORKER-RUNNER-INPROCESS-MIGRATION (July 2026). See
	// WithBroker() below + CompletionPort interface declaration at
	// internal/capabilities/jobs/queue/broker.go:50 for the contract.
	broker    CompletionPort
	jobLedger capjobregistry.Registry

	// observer is the kernel observability entry point (FASE 2, August
	// 2026). When non-nil, every claimed job in runJob gets a Run:
	// queue_wait_ms (created_at → started_at), wall_time_ms, status and
	// attempts. nil = legacy un-instrumented behaviour (test fixtures
	// that don't wire an observer keep working). See WithObserver().
	observer *kernobs.RunObserver
}

// WorkerDeps carries the dependencies for NewWorker. Grouping them
// keeps the constructor under the archcheck 8-parameter cap while
// making the call sites self-documenting.
type WorkerDeps struct {
	ID         string
	Repo       job.Store
	Dispatcher *Dispatcher
	Notifier   QueueNotifier
	Log        *zap.Logger
	LeaseTTL   time.Duration
	PollEvery  time.Duration
	Backoff    BackoffConfig
	Types      []string
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
func NewWorker(deps WorkerDeps) *Worker {
	return &Worker{
		id:         deps.ID,
		repo:       deps.Repo,
		dispatcher: deps.Dispatcher,
		log:        deps.Log,
		leaseTTL:   deps.LeaseTTL,
		pollEvery:  deps.PollEvery,
		backoff:    deps.Backoff,
		types:      deps.Types,
		notifier:   deps.Notifier,
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

// WithBroker attaches the canonical CompletionPort narrow port to
// the Worker for artifact-producing job finalization (PR-WORKER-
// RUNNER-INPROCESS-MIGRATION, July 2026).
//
// godlike/06 SSOT one-canonical-owner-per-fact: the port contract
// (CompletionPort.CompleteWithArtifacts) is declared EXACTLY ONCE
// in internal/capabilities/jobs/queue/broker.go:50 and consumed here
// through the typed narrow interface. The Worker does NOT depend on
// the broader 9-method Broker surface (RegisterWorker / Heartbeat /
// Claim / Renew / Progress / Complete / Fail / IsCancelled) because
// the Worker's finalization block in worker_execution.go calls only
// `cp.CompleteWithArtifacts` — depending on the narrower port per
// godlike/07 minimum-bleed.
//
// Mirrors the WithRegistry fluent-setter precedent (HC-1 June 2026)
// so the composition root uses an idiomatic builder-style chain:
//
//	jobs.NewWorker(...).WithRegistry(reg).WithBroker(cp)
//
// Nil-tolerant: a nil broker means the worker falls through to the
// legacy w.repo.Complete path inside runJob (worker_execution.go).
// This preserves the canonical "no broker configured" behaviour that
// legacy fixtures relied on; production wiring ALWAYS supplies a
// non-nil broker via the composition root.
//
// Returns the receiver to allow builder-style chaining at the
// composition site.
//
// Concrete compatibility: the in-process *local.Broker at
// internal/infrastructure/jobs/local/broker.go satisfies CompletionPort
// structurally via its
// (ctx context.Context, cmd CompleteWithArtifactsCommand) ([]string, error)
// signature. No compile-time pin needed — Go's structural interface
// satisfaction handles this at the composition root in
// `internal/app/build_bundles_workers.go`.
func (w *Worker) WithBroker(cp CompletionPort) *Worker {
	w.broker = cp
	return w
}

// WithJobRegistry attaches the durable Job Registry projection to this worker.
func (w *Worker) WithJobRegistry(reg capjobregistry.Registry) *Worker {
	w.jobLedger = reg
	return w
}

// WithObserver attaches the kernel observability RunObserver to the
// Worker (FASE 2, August 2026). Mirrors the WithRegistry/WithBroker
// fluent-setter precedent so the composition root uses builder-style
// chaining:
//
//	jobs.NewWorker(...).WithRegistry(reg).WithBroker(cp).WithObserver(obs)
//
// Nil-tolerant: a nil observer means runJob skips instrumentation
// entirely (legacy behaviour preserved for fixtures that don't build
// an observer). Production wiring ALWAYS supplies a non-nil observer
// via the Runner (Runner.WithObserver → buildWorkers propagation).
//
// Returns the receiver to allow builder-style chaining at the
// composition site.
func (w *Worker) WithObserver(observer *kernobs.RunObserver) *Worker {
	w.observer = observer
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

// maxRetriesFor returns the default max-retry count for a job type,
// sourced from the attached Registry. Falls back to the canonical
// 3-retry default when the worker has no attached Registry or the
// job type is not registered. Mirrors the timeout lookup pattern
// (jobTimeoutFor).
//
// Issue 2 / P0 (June 2026): locks the Worker-side retry lookup so
// the future Issue 4 (P1, Enqueue path) integration into runJob is
// a one-line swap — pass effectiveRetries := w.maxRetriesFor(j.Type)
// when j.MaxRetries == 0. The companion regression test
// TestWorker_HonorsRegistryRetries (in registry_wiring_test.go)
// pins this contract today so Issue 4 cannot accidentally regress
// the lookup surface.
func (w *Worker) maxRetriesFor(jobType string) int {
	if w.reg != nil {
		return w.reg.DefaultMaxRetries(jobType)
	}
	return 3
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

		w.requeueDueRetries(ctx)

		j, err := w.repo.ClaimNext(ctx, w.id, w.leaseTTL, w.types)
		if err != nil {
			if errors.Is(err, job.ErrTransitionConflict) {
				// Expected under concurrent polling: another worker won the
				// CAS race and claimed the job first. Treat it as a normal
				// empty poll rather than a server-side error.
				if !w.sleepBackoff(ctx, w.effectiveSleep(w.pollEvery)) {
					w.log.Info("worker stopped", zap.String("worker_id", w.id))
					return
				}
				continue
			}
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

// requeueDueRetries closes the retry lifecycle: ScheduleRetry records a
// RETRY_WAIT row, while ClaimNext only accepts QUEUED rows. The persisted
// UpdatedAt and RetryCount provide a deterministic backoff without adding a
// second scheduler or making SQLite state non-canonical.
func (w *Worker) requeueDueRetries(ctx context.Context) {
	status := job.StatusRetryWait
	waiting, err := w.repo.List(ctx, job.Filter{Status: &status, Limit: 100})
	if err != nil {
		w.log.Warn("failed to list retry-wait jobs", zap.Error(err))
		return
	}
	now := time.Now().UTC()
	for i := range waiting {
		j := &waiting[i]
		if j.RetryCount >= j.MaxRetries {
			continue
		}
		backoff := retry.BackoffFor(j.RetryCount-1, retry.Options{
			InitialBackoff: 2 * time.Second,
			BackoffFactor:  2.0,
			MaxBackoff:     30 * time.Second,
		})
		if now.Sub(j.UpdatedAt) < backoff {
			continue
		}
		if _, err := w.repo.Retry(ctx, j.ID); err != nil && !errors.Is(err, job.ErrTransitionConflict) {
			w.log.Warn("failed to requeue retry-wait job", zap.String("job_id", j.ID), zap.Error(err))
		}
	}
}

// mapToRawMessage marshals a map to json.RawMessage, returning "{}"
// on nil/empty/error cases (the canonical "Complete a job with an
// empty result body" sentinel). Free func — no *Worker receiver
// needed — kept in the bootstrap file as a stable utility home.
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
