package jobs

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	jobs "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
	sqljobs "github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/jobs"
)

// ── Store / command types ───────────────────────────────────────────────────
//
// Wave 5 PR 3 (June 2026): removed the three zero-copy forwarding type
// aliases formerly aliased here (Store, StartJob, RequeueResult). Callers
// must now import the canonical home directly:
//   • jobs.Store                 → domain/jobs.Store
//   • jobs.StartJob              → internal/platform/sqlite/jobs.StartJob
//   • jobs.RequeueResult         → internal/platform/sqlite/jobs.RequeueResult
// The single in-tree consumer that switched to direct imports is
// internal/infrastructure/jobs/local/broker.go. The application-layer
// Runner/NewRunner are now typed against the canonical jobs.Store interface.
// PR4.A2 (June 2026): removed the SQLiteStore/JobStats/ErrLeaseLost type
// aliases (formerly this package's store.go). Callers now import
// internal/platform/sqlite/jobs directly as `sqljobs`.

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
	// signature (compile-time seam marker at internal/capabilities/jobs/queue/
	// notifier.go::var _ QueueNotifier = (*sqljobs.SQLiteStore)(nil)).
	// Composition root wires the in-process *SQLiteStore today; a
	// future postgres adapter (LISTEN/NOTIFY) plugs in here via Deps.
	Notifier sqljobs.QueueNotifier
}
