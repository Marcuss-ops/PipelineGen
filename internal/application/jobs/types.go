package jobs

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	capjobregistry "github.com/Marcuss-ops/PipelineGen/internal/capabilities/jobregistry"
	sqljobs "github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/jobs"
	jobs "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
	kernobs "github.com/Marcuss-ops/PipelineGen/internal/kernel/observability"
	"go.uber.org/zap"
)

// ── Store / command types ───────────────────────────────────────────────────
//
// Wave 5 PR 3 (June 2026): removed the three zero-copy forwarding type
// aliases formerly aliased here (Store, StartJob, RequeueResult). Callers
// must now import the canonical home directly:
//   • jobs.Store                 → domain/jobs.Store
//   • jobs.StartJob              → internal/infrastructure/database/sqlite/jobs.StartJob
//   • jobs.RequeueResult         → internal/infrastructure/database/sqlite/jobs.RequeueResult
// The single in-tree consumer that switched to direct imports is
// internal/infrastructure/jobs/local/broker.go. The application-layer
// Runner/NewRunner are now typed against the canonical jobs.Store interface.
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

// Canonical Handler / JobExecutionTools / Result types live in
// internal/domain/job/handler.go (P1 #13 SSOT, July 2026). The
// canonical placement is the domain layer — NOT this package —
// because worker.Registry.Register (a sub-package of jobs) needs
// the SAME Handler type, and putting it in jobs would force worker
// to import its parent package, creating a cycle on the test side
// (jobs/handler_signature_test.go imports worker).
//
// Domain has NO upstream imports, so both jobs AND worker can
// freely alias from it. The aliases below are sealed: renaming
// any of jobs.Handler / domainob.JobExecutionTools /
// domainob.Result triggers a compile failure at THIS site, the
// godlike/06 SSOT lock that forces future renames to be deliberate.
//
// The application-layer aliases preserve the 96 in-tree pre-P1-#13
// references that imported HandlerFunc / JobTools directly from
// jobs — they compile unchanged via Go type-alias semantics. New
// code MUST prefer to import internal/domain/job directly.
//
// godlike/07 EXPAND→BACKFILL→CUTOVER→CONTRACT migration:
//
//	EXPAND (today, P1 #13): canonical SSOT in domain/job +
//	                         sealed back-compat aliases here.
//	BACKFILL (forward-pointer, deadline 2026-09-15): per-job-type
//	                         handlers that still type-literal
//	                         HandlerFunc / map[string]any return
//	                         migrate to Handler / Result.
//	CUTOVER (forward-pointer, deadline 2026-10-01): aliases
//	                         HandlerFunc, JobTools retired.
//	CONTRACT (forward-pointer): physical git-rm; Check-N in
//	                         scripts/ci-architectural-checks.sh
//	                         bans the legacy literal shapes.
type (
	JobExecutionTools = jobs.JobExecutionTools
	JobTools          = jobs.JobExecutionTools // back-compat alias
	Result            = jobs.Result
	Handler           = jobs.Handler
	HandlerFunc       = jobs.Handler // back-compat alias
)

// Dispatcher routes jobs to registered handlers by job type (string).
// Safe for concurrent use after Freeze().
type Dispatcher struct {
	mu       sync.RWMutex
	handlers map[string]Handler
	frozen   bool

	// registry + enqueuer are the P0 Commit 4 (July 2026) additions
	// for Dispatcher.Enqueue. They live on Dispatcher (not a separate
	// EnqueueService type) so the typed gateway is co-located with the
	// handler dispatcher surface. Both fields are unexported; access
	// via the fluent builders (WithRegistry + SetEnqueuer) in dispatcher.go.
	//
	// registry holds the canonical frozen CompiledJobRegistry (P0 Commit 3)
	// for type→definition lookups. Enqueue reads Definition(jobType) to
	// find the canonical codec for payload encoding.
	//
	// enqueuer holds the typed EnqueuePort that Enqueue delegates to.
	// *Service satisfies EnqueuePort (Service.Enqueue is the canonical
	// row-create method). The composition root wires the enqueuer
	// AFTER constructing the *Service — see dispatcher.go for the
	// late-binding rationale (cycle-break between dispatcher↔service).
	registry jobs.CompiledJobRegistry
	enqueuer EnqueuePort
}

func NewDispatcher() *Dispatcher {
	return &Dispatcher{handlers: make(map[string]Handler)}
}

func (d *Dispatcher) Register(jobType string, handler Handler) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.frozen {
		return fmt.Errorf("dispatcher is frozen: cannot register handler for %s", jobType)
	}
	// Idempotent: silently overwrite on duplicate Register — the v2
	// critical-handler validator (PR-VALIDATOR-LITERAL-REGISTER, July 2026)
	// re-invokes Register for handlers already bound by the late-bindings
	// block. The dispatcher MUST accept duplicates without error so the
	// validator's fail-closed posture (abort on any non-nil Bind result)
	// is compatible with the double-Register pattern.
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
func (d *Dispatcher) AllHandlers() map[string]Handler {
	d.mu.RLock()
	defer d.mu.RUnlock()
	out := make(map[string]Handler, len(d.handlers))
	for k, v := range d.handlers {
		out[k] = v
	}
	return out
}

func (d *Dispatcher) Dispatch(ctx context.Context, j *jobs.Job, tools *JobExecutionTools) (result Result, err error) {
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
//
// Issue 2 / P0 (June 2026): Runner now carries a typed config-port
// for per-job-type timeouts + retries, sourced from *Registry. The
// Registry is attached via the WithRegistry builder (mirrors
// Worker.WithRegistry, HC-1 June 2026 plumbing) and propagated to
// every Worker constructed by buildWorkers/Start. Without it, each
// Worker would fall back to the pre-HC-1 literal 10-minute timeout
// and literal 3-retry defaults — bypassing the typed-port contract
// declared in jobs.Compose().
//
// Composition root pattern (canonical):
//
//	r := jobs.NewRunner(repo, dispatcher, log, cfg).
//	    WithRegistry(jobs.Compose())
//	r.Start(ctx)
type Runner struct {
	repo       jobs.Store
	dispatcher *Dispatcher
	log        *zap.Logger
	config     RunnerConfig
	registry   *Registry
	workers    []*Worker
	broker     CompletionPort
	jobLedger  capjobregistry.Registry

	// observer is the kernel observability entry point propagated to
	// every Worker built by buildWorkers (FASE 2, August 2026). nil =
	// legacy un-instrumented workers (test fixtures keep working).
	observer *kernobs.RunObserver
}

func NewRunner(repo jobs.Store, dispatcher *Dispatcher, log *zap.Logger, config RunnerConfig) *Runner {
	return &Runner{
		repo:       repo,
		dispatcher: dispatcher,
		log:        log,
		config:     config,
	}
}

// WithRegistry attaches a typed Registry to the Runner. The Registry
// is propagated to every Worker constructed in Start so each Worker
// honors the per-job-type Timeout / DefaultMaxRetries values declared
// in jobs.Compose().
//
// Nil-tolerant: a nil reg means the workers fall back to the legacy
// literal defaults (10-min timeout, 3 retries), preserving test
// fixtures that don't build a registry. Mirrors Worker.WithRegistry.
//
// Returns the receiver for builder-style chaining.
func (r *Runner) WithRegistry(reg *Registry) *Runner {
	r.registry = reg
	return r
}

// WithBroker attaches a CompletionPort narrow port (like the local Broker)
// to the Runner so that local workers can complete artifact-producing jobs.
func (r *Runner) WithBroker(cp CompletionPort) *Runner {
	r.broker = cp
	return r
}

// WithJobRegistry attaches the durable execution ledger to every worker.
func (r *Runner) WithJobRegistry(reg capjobregistry.Registry) *Runner { r.jobLedger = reg; return r }

// WithObserver attaches the kernel observability RunObserver to the
// Runner (FASE 2, August 2026). The observer is propagated onto every
// Worker constructed by buildWorkers so each claimed job produces a
// Run (queue_wait, wall_time, status, attempts).
//
// Nil-tolerant: a nil observer means the workers skip instrumentation
// entirely, preserving legacy test fixtures. Mirrors WithRegistry /
// WithBroker.
//
// Returns the receiver for builder-style chaining.
func (r *Runner) WithObserver(observer *kernobs.RunObserver) *Runner {
	r.observer = observer
	return r
}

// buildWorkers constructs the worker pool with the attached Registry
// wired onto each Worker (via Worker.WithRegistry). Called by Start;
// kept package-private so tests can assert the binding without
// spinning up the poll loop.
//
// Issue 2 / P0 (June 2026): the WithRegistry chain here is the fix
// surface. Pre-fix Start called NewWorker(...) directly and the
// workers silently regressed to the HC-0 literal defaults.
func (r *Runner) buildWorkers() []*Worker {
	workers := make([]*Worker, 0, r.config.Workers)
	for i := 0; i < r.config.Workers; i++ {
		workerID := fmt.Sprintf("%s_worker-%d", workerIDPrefix, i+1)
		w := NewWorker(WorkerDeps{
			ID:         workerID,
			Repo:       r.repo,
			Dispatcher: r.dispatcher,
			Notifier:   r.config.Notifier,
			Log:        r.log,
			LeaseTTL:   r.config.LeaseTTL,
			PollEvery:  r.config.PollEvery,
			Backoff:    r.config.Backoff,
			Types:      r.config.JobTypes,
		})
		w.WithRegistry(r.registry)
		if r.broker != nil {
			w.WithBroker(r.broker)
		}
		if r.jobLedger != nil {
			w.WithJobRegistry(r.jobLedger)
		}
		if r.observer != nil {
			w.WithObserver(r.observer)
		}
		workers = append(workers, w)
	}
	return workers
}

func (r *Runner) Start(ctx context.Context) {
	r.log.Info("starting job runner", zap.Int("workers", r.config.Workers))

	r.workers = r.buildWorkers()
	for _, w := range r.workers {
		go w.Start(ctx)
	}

	r.log.Info("job runner started", zap.Int("worker_count", len(r.workers)))
}
