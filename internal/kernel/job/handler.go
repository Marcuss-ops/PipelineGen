// Package job — handler.go (PR-KERNEL-JOB-POPULATE, commit 9, July 2026).
//
// Canonical job-handler contract surface. Both
// internal/capabilities/jobs/queue.Dispatcher.Register AND
// internal/capabilities/jobs/worker.Registry.Register consume the
// SAME Handler type — declared here in the kernel subzone
// (godlike/06 SSOT: cross-cutting contracts owned by the layer
// dependency-free of every consumer).
//
// MIGRATION FROM internal/domain/job/handler.go (Phase A.2 → commit 9):
//   - Pre-commit-9: Handler was declared in internal/domain/job/ as
//     the canonical surface; the kernel owned reading-of-types
//     (Status / Filter / Job / Event) but the handler shape was
//     the domain subzone's responsibility.
//   - Post-commit-9: Handler is the kernel-level canonical.
//     internal/domain/job/handler.go re-exports
//     `type Handler = kerneljob.Handler` and
//     `type JobExecutionTools = kerneljob.JobExecutionTools`
//     for back-compat with 31 in-tree pre-P1-#13 references.
//
// Per godlike/02 kernel rules:
//   - Stdlib-only imports (no application/infrastructure imports).
//   - The handler signature's parameter types reference
//     intra-package Job/Status (no cross-zone imports).
package job

import (
	"context"
)

// ── Canonical JobExecutionTools ───────────────────────────────────────

// JobExecutionTools provides the callbacks a handler invokes to
// report progress and emit typed events. Both callbacks are
// nil-tolerant at the handler site via the SafeProgressFn /
// SafeEventFn helpers in internal/capabilities/jobs/queue (godlike/07
// no-nil-panic contract).
//
// FASE 4(b) (July 2026) — IsCancelled field REMOVED: the
// pre-Fase-4 `IsCancelled func() bool` field was the handler-
// facing projection of the per-job 2-second IsCancelled-poll
// goroutine at worker_execution.go::startCancelWatcher. FASE 4
// spec close-out removed the goroutine and folded the cancel
// signal into the typed kerneljob.RenewLeaseResult.State return
// (LeaseStateCancelRequested → renewLeaseLoop calls jobCancel).
// Handlers now observe cancellation natively via ctx.Err() at
// their next phase boundary (the canonical cancellation
// pattern pre-Fase-4 handlers already used as a secondary
// signal), so the explicit IsCancelled callback is redundant.
// godlike/07 minimum-blast-radius: removal is breaking on
// any handler that read tools.IsCancelled; the in-tree
// handlers that did so were updated to use ctx.Err() as part of
// the same FASE 4(b) cut.
type JobExecutionTools struct {
	Progress func(progress int, message string)
	Event    func(eventType string, message string, data map[string]any)
}

// ── Result (canonical typed return envelope, today a typed alias of map[string]any) ────

// Result is the canonical typed return envelope for handlers.
// Today it is a Go type alias for map[string]any so every
// pre-P1-#13 handler signature returning `map[string]any` still
// compiles (godlike/06 SSOT back-compat policy: an additive
// unification PR MUST NOT force every handler to migrate to a
// typed envelope). The forward-pointer to a typed envelope
// (`type Result struct { Status string; Payload map[string]any;
// Artifacts map[string]string; Meta map[string]any }`) lives in
// the BACKFILL phase above — exists only as a comment today.
//
// godlike/07 minimal-blast-radius decision: keep Result as a
// type alias of map[string]any here so the kernel subzone has no
// typed-envelope shape that downstream handlers must migrate to.
// When the typed envelope lands in BACKFILL, it lives in a
// separate package (e.g. internal/capabilities/jobs/queue/result_types.go)
// so the kernel keeps a stable typed-alias surface.
type Result = map[string]any

// ── Handler (canonical, kernel-level) ──────────────────────────────────

// Handler is the canonical job-handler signature consumed by BOTH
// internal/capabilities/jobs/queue.Dispatcher.Register AND
// internal/capabilities/jobs/worker.Registry.Register.
//
// Adopted in P1 #13 (July 2026) to unify the previously-divergent
// surfaces (Dispatcher.HandlerFunc + worker.Handler). The
// worker's Handler is now a Go type alias
// `type Handler = kerneljob.Handler` so a single function value
// can be registered against either — future drift in the Handler
// signature is a build failure at THIS site (godlike/06 SSOT).
//
// Parameters:
//
//	ctx   — handler lifetime boundary (per godlike/06 §Post-write,
//	        may be a request context, a job-lifetime context, or a
//	        worker-lifetime context; chosen by the caller). The
//	        canonical post-write save pattern is `context.WithoutCancel`
//	        of the request context (archcheck Check-1 allowlist).
//	j     — the canonical *job.Job (kernel/job.Job, intra-package).
//	        Handlers MUST NOT mutate it; reads for Attempt + Metadata
//	        are sanctioned.
//	tools — JobExecutionTools callbacks for Progress / Event.
//	        The worker runtime translates its *worker.Tools broker
//	        facade into this envelope at Dispatch time so the
//	        handler observes the same shape from both the in-process
//	        Dispatcher call and the remote-worker call.
//
// Return values:
//
//	result — the typed Result envelope (today a map-equivalent;
//	         tightening to a typed envelope is the BACKFILL-phase
//	         forward-pointer above).
//	err    — non-nil surfaces as a typed retryable/terminal error
//	         per the per-job-type retry policy declared in
//	         domain/job/registry.go::JobDefinition.RetryPolicyKey.
type Handler func(ctx context.Context, j *Job, tools *JobExecutionTools) (Result, error)
