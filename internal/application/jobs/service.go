// Package jobs — service.go: thin facade over the canonical job.Store port.
//
// PR-GODOBJ-6 (July 2026): mechanically decomposed per the god-object
// decomposition plan. The original 535-LoC service.go is now a thin
// facade; focused concerns live in:
//   - handler_registration.go — RegisterHandler, HasHandler, ValidateHandlerCompleteness
//   - enqueue_service.go      — Enqueue + idempotency pipeline (MaxPayloadSize, generateJobID)
//   - job_queries.go          — Get, List, FindActiveByKey, ListAwaitingAggregation, ListEvents,
//                               IsTerminal, GetStats, RequeueExpiredLeases
//   - job_commands.go         — Cancel, Retry, Progress, AddEvent, Complete, Fail,
//                               FinalizeAggregateParent
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
// `internal/infrastructure/database/sqlite/jobs/repository.go` is the load-bearing
// invariant: a future PR that resurrects `RequeueExpiredLeasesNoArg` (or any
// other SQLite-only method) on this Service would have to widen the JobBroker
// port to expose it, which the architecture review would catch at PR-merge time.
// Composition-root callers in `internal/app` already hold the concrete
// *SQLiteStore via JobsBundle.Repo and call those helpers directly.
package jobs

import (
	"sync"

	"go.uber.org/zap"

	job "github.com/Marcuss-ops/PipelineGen/internal/domain/job"
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

	// enqueueMu serializes FindActiveByKey + Create to prevent the
	// race where two concurrent Enqueue calls both find no existing
	// job and then both insert a duplicate.
	enqueueMu sync.Mutex
}

// NewService constructs the Service from the canonical job.JobBroker port.
// Composition root injects *sqljobs.SQLiteStore today; future PR-`postgres`
// injects *pgbroker.Store (declared via `var _ job.JobBroker = (*pgbroker.Store)(nil)`).
//
// Issue 4 (June 2026, P1): the registry is NOT a required constructor arg
// — attach it via the fluent WithRegistry(reg) builder. Nil-tolerant at
// runtime: when no registry is attached the Enqueue() MaxRetries fallback
// keeps the pre-Issue-4 hard-coded default of 3 for ANY job type (legacy
// safety net preserved for test fixtures that don't wire the registry).
func NewService(repo job.JobBroker, dispatcher *Dispatcher, log *zap.Logger) *Service {
	return &Service{
		repo:       repo,
		dispatcher: dispatcher,
		log:        log,
	}
}

// WithRegistry attaches the canonical job-type *Registry to this Service.
// When attached, the Enqueue()-side MaxRetries fallback uses the registry's
// per-job-type DefaultMaxRetries value for any REGISTERED job type and
// keeps the legacy hard-coded 3 only for UNREGISTERED types. Mirrors the
// HC-1 Worker.WithRegistry(reg *Registry) precedent.
//
// Issue 4 (June 2026, P1): nil-tolerant. Passing nil clears the field
// (test fixture path). Calling WithRegistry multiple times reassigns
// (last writer wins), which is unsafe-but-tolerated composition-only.
func (s *Service) WithRegistry(reg *Registry) *Service {
	if s == nil {
		return s
	}
	s.registry = reg
	return s
}

// Compile-time assertion: *Service satisfies the domain job.Service interface.
var _ job.Service = (*Service)(nil)
