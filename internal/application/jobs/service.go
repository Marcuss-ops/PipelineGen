// Package jobs — service.go: thin facade over the canonical job.Store port.
//
// PR-GODOBJ-6 (July 2026): mechanically decomposed per the god-object
// decomposition plan. The original 535-LoC service.go is now a thin
// facade; focused concerns live in:
//   - handler_registration.go — RegisterHandler, HasHandler, ValidateHandlerCompleteness
//   - enqueue_service.go      — Enqueue + idempotency pipeline (MaxPayloadSize, generateJobID)
//   - job_queries.go          — Get, List, FindActiveByKey, ListAwaitingAggregation, ListEvents,
//     IsTerminal, GetStats, RequeueExpiredLeases
//   - job_commands.go         — Cancel, Retry, Progress, AddEvent, Complete, Fail,
//     FinalizeAggregateParent
//
// Service is the application-layer facade over the canonical job.Store
// port (job.JobBroker). The previous shape held *sqljobs.SQLiteStore
// directly (a godlike/06 violation — application → infrastructure). PR-B
// switches the field type to job.JobBroker (= job.Store in the canonical
// embedding). The compile-time assertion `var _ job.JobBroker = (*SQLiteStore)(nil)`
// in the infrastructure layer guarantees the seam is conformant.
//
// Service-internal transitions (Enqueue idempotency, FindActiveByKey /
// FindByTypeAndCorrelation / Retry / ListEvents) are part of the canonical
// Store surface as of PR-B. SQLite-specific helpers (GetStats,
// RequeueExpiredLeasesNoArg, MarkRunningJobsOlderThanFailed) intentionally
// do NOT live on this Service — the compile-time assertion
// `var _ job.JobBroker = (*SQLiteStore)(nil)` in
// `internal/platform/sqlite/jobs/repository.go` is the load-bearing
// invariant: a future PR that resurrects `RequeueExpiredLeasesNoArg` (or any
// other SQLite-only method) on this Service would have to widen the JobBroker
// port to expose it, which the architecture review would catch at PR-merge time.
// Composition-root callers in `internal/app` already hold the concrete
// *SQLiteStore via JobsBundle.Repo and call those helpers directly.
package jobs

import (
	"go.uber.org/zap"

	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
)

// Service manages job life cycle: enqueue, query, cancel.
//
// PR-B: repo field is the canonical job.JobBroker port, not the concrete
// *sqljobs.SQLiteStore. Any future broker adapter (e.g. PostgreSQL) can be
// injected without touching this file.
//
// Issue 4 (June 2026, P1): optional `registry *Registry` carries the
// per-job-type default retries. Wired via the fluent WithRegistry(reg)
// builder (mirrors the HC-1 Worker.WithRegistry precedent) so the
// legacy NewService(repo, dispatcher, log) signature stays stable for
// tests that don't depend on the registry.
type Service struct {
	repo       job.JobBroker
	dispatcher *Dispatcher
	log        *zap.Logger
	registry   *Registry
}

// NewService constructs the Service from the canonical job.JobBroker port.
// Composition root injects *sqljobs.SQLiteStore today; future PR-`postgres`
// injects *pgbroker.Store (declared via `var _ job.JobBroker = (*pgbroker.Store)(nil)`).
//
// PR-jobs-retry-contract (July 2026): the registry is REQUIRED at
// construction time (fail-closed typed contract per godlike/07
// no-fake-availability). The 4-arg signature returns (*Service, error) so
// composition-root misconfiguration surfaces at startup as ErrRegistryRequired
// instead of failing silently on first Enqueue with the legacy 3-retry
// fallback.
//
// Composition root wiring contract:
//
//	svc, err := appjobs.NewService(repo, dispatcher, log, appjobs.Compose())
//	if err != nil {
//	    return fmt.Errorf("build jobs bundle: %w", err)
//	}
//
// (Pre-PR behavior) the legacy 3-arg NewService(repo, dispatcher, log)
// was nil-tolerant at the registry level: a nil-registry wiring kept the
// pre-Issue-4 hard-coded 3-retry safety net for ANY job type — a
// godlike/07 silent-success risk if the composition root forgot the
// WithRegistry call. The 4-arg signature eliminates that risk entirely.
func NewService(repo job.JobBroker, dispatcher *Dispatcher, log *zap.Logger, reg *Registry) (*Service, error) {
	if reg == nil {
		return nil, ErrRegistryRequired
	}
	if repo == nil {
		return nil, ErrRepoRequired
	}
	if log == nil {
		return nil, ErrLogRequired
	}
	return &Service{
		repo:       repo,
		dispatcher: dispatcher,
		log:        log,
		registry:   reg,
	}, nil
}

// WithRegistry attaches the canonical job-type *Registry to this Service.
//
// Deprecated: the registry is now REQUIRED at construction time via the
// 4-arg NewService signature (PR-jobs-retry-contract, July 2026). This
// setter is preserved (NOT removed) for back-compat with pre-PR test
// fixtures + composition-root code that incrementally migrates; passing
// a non-nil registry here AFTER NewService will OVERWRITE the
// constructor-set registry (last writer wins — unsafe-but-tolerated for
// composition-only use).
//
// Mirrors HC-1 Worker.WithRegistry(reg *Registry) precedent. Nil-tolerant.
// Calling WithRegistry(nil) clears the field (test fixture path); do NOT
// call nil-WithRegistry in production — the 4-arg NewService enforces the
// canonical fail-closed surface.
//
// Forward-pointer: future CUTOVER removes this setter entirely once all
// composition-root callers migrate to the 4-arg signature.
func (s *Service) WithRegistry(reg *Registry) *Service {
	if s == nil {
		return s
	}
	s.registry = reg
	return s
}

// Compile-time assertion: *Service satisfies the domain job.Service interface.
var _ job.Service = (*Service)(nil)
