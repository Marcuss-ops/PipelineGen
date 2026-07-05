// Package job — handler.go (P1 #13 unification, July 2026).
//
// Canonical job-handler contract surface. Both
// internal/application/jobs.Dispatcher.Register AND
// internal/application/jobs/worker.Registry.Register consume the
// SAME Handler type — declared here in the domain layer so neither
// application-layer package must import the other (godlike/06: a
// cross-cutting contract is owned by the layer that is dependency-
// free of both consumers).
//
// ── Why domain ──────────────────────────────────────────────────────
//
// internal/domain/job already owns CompiledJobRegistry /
// MutableJobRegistry / JobDefinition / JobHandlerFunc (see
// registry.go in this package). Adding Handler here extends the
// package's scope from "what jobType-bound shapes exist" to "how
// is a job executed" — both are cross-cutting concerns that neither
// the application nor the infrastructure layer should take
// ownership of (godlike/06 "one canonical owner per fact").
//
// ── Why NOT a separate sub-package ───────────────────────────────────
//
// We considered internal/application/jobs/handler_contract.go as
// an alternative. The 1-type-package shape turned the package
// boundary into ceremony rather than a meaningful layer. The
// domain layer is the right home: it has zero upward dependencies
// (only stdlib + this same domain package), so adding the handler
// surface here does NOT introduce a cycle with any application
// package that imports it.
//
// ── Migration (godlike/07 EXPAND→BACKFILL→CUTOVER→CONTRACT) ────────
//
//	EXPAND (today, P1 #13): canonical-types declared here +
//	                       application-layer aliases sealed
//	                       (jobs.Handler = domainjob.Handler,
//	                       worker.Handler = domainjob.Handler).
//	                       3 authoritative call sites (jobs.Dispatcher,
//	                       worker.Registry, worker.Registry.Dispatch)
//	                       consume the SAME Handler literal.
//	BACKFILL (forward-pointer): the 31 in-tree pre-P1-#13 references
//	                       that declared handler signatures as
//	                       `HandlerFunc` or typed `map[string]any`
//	                       return literal will be renamed to canonical
//	                       Handler / Result via a separate migration PR.
//	                       Today, they compile via go-type-alias
//	                       semantics with zero behaviour drift
//	                       (jobs.HandlerFunc = domainjob.Handler).
//	CUTOVER (forward-pointer, deadline 2026-09-15): the back-compat
//	                       aliases (`jobs.HandlerFunc`, `jobs.JobTools`,
//	                       `worker.Tools` as a Handler-signature param)
//	                       are removed in a single CONTRACT commit on
//	                       a future wave once every typed literal has
//	                       been migrated to the canonical names.
//	CONTRACT (forward-pointer, deadline 2026-10-01): physical
//	                       retirement of the legacy shapes; Check-N in
//	                       scripts/ci-architectural-checks.sh bans the
//	                       legacy literal shapes from new code.
package job

import (
	"context"
)

// ── Canonical handler contract ───────────────────────────────────────

// JobExecutionTools provides the callbacks a handler invokes to
// report progress, emit typed events, and check for cancellation.
// All three callbacks are nil-tolerant at the handler site via the
// SafeProgressFn / SafeEventFn / SafeIsCancelled helpers in
// internal/application/jobs (godlike/07 no-nil-panic contract).
type JobExecutionTools struct {
	Progress    func(progress int, message string)
	Event       func(eventType string, message string, data map[string]any)
	IsCancelled func() bool
}

// Result is the canonical typed return envelope for handlers.
// Today it is a Go type alias for map[string]any so every
// pre-P1-#13 handler signature returning `map[string]any` still
// compiles (godlike/06 SSOT back-compat policy: an additive
// unification PR MUST NOT force every handler to migrate to a
// typed envelope). The forward-pointer to a typed envelope
// (`type Result struct { Status string; Payload map[string]any;
// Artifacts map[string]string; Meta map[string]any }`) lives in
// the BACKFILL phase above — exists only as a comment today.
type Result = map[string]any

// Handler is the canonical job-handler signature consumed by BOTH
// internal/application/jobs.Dispatcher.Register AND
// internal/application/jobs/worker.Registry.Register.
//
// Adopted in P1 #13 (July 2026) to unify the previously-divergent
// surfaces (Dispatcher.HandlerFunc + worker.Handler). The
// worker's Handler is now a Go type alias
// `type Handler = domainjob.Handler` so a single function value
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
//	j     — the canonical *job.Job (kernel/job.Job, aliased in
//	        this package). Handlers MUST NOT mutate it; reads
//	        for Attempt + Metadata are sanctioned.
//	tools — JobExecutionTools callbacks for Progress / Event /
//	        IsCancelled. The worker runtime translates its
//	        *worker.Tools broker facade into this envelope at
//	        Dispatch time so the handler observes the same
//	        shape from both the in-process Dispatcher call and
//	        the remote-worker call.
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
