package worker

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"

	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/observability"
)

// Sentinel errors for registry operations.
var (
	ErrHandlerNotRegistered = errors.New("worker handler not registered")
	ErrEmptyJobType         = errors.New("job type must not be empty or whitespace")
	ErrNilHandler           = errors.New("handler must not be nil")
	ErrDuplicateHandler     = errors.New("handler already registered")
	ErrRegistryFrozen       = errors.New("registry is frozen: cannot register after startup")
	ErrNoHandlers           = errors.New("worker has no registered handlers")
	ErrUnsupportedJobType   = errors.New("unsupported job type")
)

// Handler is the canonical job-handler signature used by BOTH
// jobs.Dispatcher.Register AND worker.Registry.Register (P1 #13
// unification, July 2026). It is a Go type alias to
// job.Handler (the canonical SSOT in internal/domain/job/
// handler.go). Putting the alias on domainjob (NOT jobs.Handler)
// breaks what would otherwise be a cycle — worker no longer
// imports its parent package; the canonical SSOT in domain is
// below both, and both packages can freely alias from it. Compile
// drift in job.Handler is a build failure at THIS site AND
// at the jobs.Handler alias site (godlike/06 SSOT lock).
type Handler = job.Handler

// Registry maps job types to handler functions. Once frozen, no new
// registrations are accepted — this prevents the claim loop from picking
// up handlers added after startup. Safe for concurrent reads.
//
// AZIONE 7 (July 2026): added producesArtifacts map so the runner can
// branch to CompleteWithArtifacts for artifact-producing jobs instead of
// hard-coding job.Type string checks.
type Registry struct {
	mu                sync.RWMutex
	handlers          map[string]Handler
	producesArtifacts map[string]bool
	frozen            bool
}

func NewRegistry() *Registry {
	return &Registry{
		handlers:          make(map[string]Handler),
		producesArtifacts: make(map[string]bool),
	}
}

// Register adds a handler for the given job type. Returns a sentinel error
// if the type is empty/whitespace, the handler is nil, a duplicate exists,
// or the registry is already frozen.
func (r *Registry) Register(jobType string, h Handler) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.frozen {
		return ErrRegistryFrozen
	}
	jobType = strings.TrimSpace(jobType)
	if jobType == "" {
		return ErrEmptyJobType
	}
	if h == nil {
		return ErrNilHandler
	}
	if _, exists := r.handlers[jobType]; exists {
		return ErrDuplicateHandler
	}
	r.handlers[jobType] = h
	return nil
}

// Freeze prevents any further registrations. Must be called before the
// claim loop starts. Once frozen, Register returns ErrRegistryFrozen.
func (r *Registry) Freeze() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.frozen = true
}

// Len returns the number of registered handlers.
func (r *Registry) Len() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.handlers)
}

// Has returns true if a handler is registered for the given job type.
func (r *Registry) Has(jobType string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	_, ok := r.handlers[jobType]
	return ok
}

// ProducesArtifacts returns whether the given job type is declared to
// produce artifacts. Workers that produce artifacts (videos, images,
// documents, voiceovers) MUST be completed via CompleteWithArtifacts so
// asset records, versions, locations, and outbox events are written in
// the same transaction as the job SUCCEEDED transition.
//
// Returns false for unknown job types (nil-safe: also returns false when
// the receiver is nil).
//
// AZIONE 7 (July 2026): replaces hard-coded job.Type string checks in
// the runner's terminal completion path.
func (r *Registry) ProducesArtifacts(jobType string) bool {
	if r == nil {
		return false
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.producesArtifacts[jobType]
}

// SetProducesArtifacts records whether the given job type produces
// artifacts. Called during composition to seed the worker registry from
// the compiled job registry's ProducesArtifactsMap. Unlike Register, this
// method is callable both before and after Freeze() — metadata seeding
// from the compiled registry typically happens after handler registration
// is complete.
//
// Returns the receiver for fluent chaining. Nil-safe no-op.
// // AZIONE 7 (July 2026): seeds the map consumed by ProducesArtifacts above.
func (r *Registry) SetProducesArtifacts(jobType string, v bool) *Registry {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.producesArtifacts[jobType] = v
	return r
}

// SeedProducesArtifacts bulk-seeds the producesArtifacts map from a
// pre-built map (typically jobs.Compose().ProducesArtifactsMap()).
// Unlike SetProducesArtifacts, this accepts a batch map so the caller
// doesn't need to iterate in application code. Nil-safe no-op.
func (r *Registry) SeedProducesArtifacts(m map[string]bool) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for tp, v := range m {
		r.producesArtifacts[tp] = v
	}
}

// JobTypes returns a sorted, defensive copy of all registered job types.
// The returned slice is safe to modify without affecting the registry.
func (r *Registry) JobTypes() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]string, 0, len(r.handlers))
	for t := range r.handlers {
		out = append(out, t)
	}
	sort.Strings(out)
	return out
}

// Dispatch routes a job to its registered handler. Returns
// ErrHandlerNotRegistered if no handler exists for the job type.
//
// P1 #13 boundary: the public Dispatch seam stays `*Tools`-bound
// because the worker runtime owns the broker-facade lifecycle
// (renewLoop + atomic revision). Internally, Dispatch translates
// the broker-facade *Tools into a JobExecutionTools callback envelope
// (Progress / Event / IsCancelled) so the canonical jobs.Handler
// signature (ctx, *job.Job, *JobExecutionTools) is observed at the
// actual handler invocation site. The translation is a 1:1
// forwarding adapter — no field is dropped or remapped.
//
// FASE 0.2 (July 4 2026): the third parameter `j *job.Job`
// is threaded into translateToolsToExecutionTools so the closure
// layer (Progress / IsCancelled / Event) can attribute its
// telemetry-emit counters to the canonical job_type label (godlike/06
// SSOT: domain/job.Job is the canonical owner of job_type
// metadata; the closure layer reads from the same source rather
// than a parallel string). Pre-FASE-0.2 `translateToolsToExecutionTools`
// took only `(ctx, t *Tools)` and could not label the counters.
func (r *Registry) Dispatch(ctx context.Context, j *job.Job, tools *Tools) (map[string]any, error) {
	r.mu.RLock()
	h, ok := r.handlers[j.Type]
	r.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrHandlerNotRegistered, j.Type)
	}
	jen := translateToolsToExecutionTools(ctx, tools, j.Type)
	return h(ctx, j, jen)
}

// translateToolsToExecutionTools builds a JobExecutionTools envelope
// from the worker.Tools broker facade. The 3 callbacks forward 1:1 —
// Progress → broker.Progress; IsCancelled → broker.IsCancelled; Event
// is wired to the broker via a typed-events hook (when available; nil
// closure if the worker has no event-emission port configured so the
// canonical nil-tolerant SafeProgressFn / future SafeEventFn semantics
// apply at the handler site).
//
// godlike/07 fail-closed: if tools is nil, every callback becomes a
// no-op closure so the handler observes the canonical nil-tolerant
// contract rather than a nil-deref panic. Mirrors the
// SafeProgressFn(tools) pattern at the application layer.
//
// FASE 0.2 (July 4 2026) silent-drop rewrite per
// PR-GODOBJ-14-WORKER-REGISTRY godlike/07 no-fake-availability. Pre-PR
// the closure layer had `_ = t.Progress(ctx, ...)` and
// `ok, _ := t.IsCancelled(ctx)` — both silent-drops violating the
// no-fake-availability contract. Post-PR each emit-failure bumps one
// of the 3 canonical worker_* counters (WorkerProgressEmittedTotal /
// WorkerProgressErrorsTotal / WorkerEventDropsTotal) with `jobType`
// as the bounded label. The IsCancelled closure fail-closes to
// `false` (NOT cancelled) on broker error so a transient
// IsCancelled check failure cannot accidentally true-positive the
// cancellation semantic (godlike/07 — would mask a real cancellation
// or trigger a premature handler branch).
//
// Cardinality bound on the counters: the `jobType` string is the
// canonical job-type label (godlike/06 SSOT: derived from the
// canonical job.Job object at the Dispatch call site, not a
// free-form runtime argument). Empty `jobType` propagates to a ""
// label on the counter — the gauge-side per-worker_id attribution
// (separate axis) is unaffected.
//
// Pre-PR tooling risk: pre-PR domain/job.JobExecutionTools did NOT
// expose a logger field (by design — domain layer keeps logger out).
// The closure cannot emit WARN logs; the canonical observability
// signal is the counter (godlike/06 SSOT) + the upstream
// worker-package log to r.log.Warn at the Runner.runJob entry site
// (see runner.go:210).
func translateToolsToExecutionTools(ctx context.Context, t *Tools, jobType string) *job.JobExecutionTools {
	if t == nil {
		return &job.JobExecutionTools{
			Progress: func(int, string) {},
			Event:    func(string, string, map[string]any) {},
		}
	}
	return &job.JobExecutionTools{
		// FASE 4(b) (July 2026): the IsCancelled closure is REMOVED.
		// The pre-Fase-4 2-second IsCancelled-poll goroutine
		// (startCancelWatcher at worker_execution.go) is gone; cancel
		// now propagates through the typed
		// job.RenewLeaseResult.State observation in
		// renewLeaseLoopWith. The dispatch seam here is the canonical
		// translation from worker.Tools to domainob.JobExecutionTools
		// — without IsCancelled, the only 2 callbacks remaining are
		// Progress and Event. Handlers observe cancellation natively
		// via ctx.Err() at their next phase boundary.
		Progress: func(progress int, message string) {
			// FASE 0.2 silent-drop rewrite: error-checked emit with
			// counter telemetry (NOT log emit because the closure
			// has no logger access — domain/job.JobExecutionTools
			// signature deliberately excludes logger per Pattern 0).
			if err := t.Progress(ctx, progress, message); err != nil {
				observability.WorkerProgressEmittedTotal.WithLabelValues(jobType, "error").Inc()
				observability.WorkerProgressErrorsTotal.WithLabelValues(jobType, "broker_emit_failed").Inc()
				return
			}
			observability.WorkerProgressEmittedTotal.WithLabelValues(jobType, "success").Inc()
		},
		// FASE 0.2 silent-drop rewrite: Event is forwarded to the
		// broker facade's typed event port. Errors are counted but
		// not propagated — event emission must never fail the job.
		Event: func(eventType, message string, data map[string]any) {
			if err := t.Event(ctx, eventType, message, data); err != nil {
				observability.WorkerEventDropsTotal.WithLabelValues(jobType).Inc()
			}
		},
	}
}
