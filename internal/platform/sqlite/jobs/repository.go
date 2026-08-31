// Package jobs provides the canonical job repository with atomic CAS operations
// for the 8-state job lifecycle.
//
// States: queued → leased → running → finalizing → succeeded / retry_wait / failed / cancelled
//
// Implements the canonical job.Store contract from internal/domain/job directly,
// without conversion through legacy model types (PR4: job.Job SSOT).
//
// queue_notifier.go holds the in-process wake-up broadcast primitive
// (canonical per PR-Polling / ADR-0002 §D6.5, June 2026).
//
// # File organisation (PR-SPLIT-JOBS-REPO-RESIDUAL, July 2026)
//
// The canonical SQLiteStore implementation is decomposed across multiple
// single-purpose files per AGENTS.md Pattern 5 godlike/06 SSOT
// one-canonical-owner-per-fact. This file is the slim orchestrator — it
// owns ONLY the SQLiteStore struct, the constructor, the in-process queue
// notifier ports (Subscribe/Broadcast/queueChanged), the deprecated raw DB
// accessor, and the two compile-time pin assertions. All read/write
// surfaces on the `jobs` and `job_events` tables live in sister files.
//
//	repository.go                 (this file, slim orchestrator)
//	repository_jobs_crud.go       — Create / Get / List / ListAwaitingAggregation /
//	                                 FindActiveByKey / FindByTypeAndCorrelation + jobColumns +
//	                                 scanner interface + scanJobColumns
//	repository_events.go          — ListEvents + eventsColumns (read-side timeline)
//	repository_stats.go           — GetStats + RefreshMetrics + JobStats (pre-extracted via 152ca16d)
//	repository_scanner.go         — rfc3339TimeScanner (RED-2 / JOBS-T01-001 typed strftime adapter)
//	repository_claims.go          — ClaimNext / Start / RenewLease / RequeueExpiredLeases (claim/lease lifecycle)
//	repository_commands.go        — typed command structs + ValidateTransition
//	transition.go                 — Transition / TransitionRequest / JobUpdates (generic CAS update)
//	lifecycle_complete.go         — Complete / Fail (FASE 0.1 artifact-gated terminal transitions)
//	lifecycle_progress.go         — SetProgress / AddEvent / MarkRunningJobsOlderThanFailed / validateOwnership
//	lifecycle_finalize.go         — FinalizeAggregateParent + parentStateTypedColumn (post-fan-out flip)
//	lifecycle_aggregation.go      — ScheduleRetry / Cancel / DeadLetter / Retry (aggregation-phase transitions)
//	store.go                      — package-level sentinels (ErrLeaseLost, ErrTransitionConflict)
package jobs

import (
	"database/sql"

	"go.uber.org/zap"

	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
)

// SQLiteStore — canonical job.Store implementation.
//
// Concurrency model (post-PR-Polling design, ADR-0003 §Implementation-
// status #6 supersession by PR-Queue-Split-claimMu cleanup, June 2026):
// the previous `claimMu` application-level mutex on ClaimNext is REMOVED.
// SQLite's WAL write-serialisation + the `AND revision = ?` CAS gate in
// repository_claims.go::Start() are sufficient for ClaimNext atomicity.
// Two workers racing the same row will both SELECT the same `id` at the
// LIMIT 1 read; the loser's UPDATE matches a stale `revision` and
// returns rows-affected=0 → ErrTransitionConflict. No application-level
// mutex is needed; SQLite is the synchronisation point.
type SQLiteStore struct {
	db                *sql.DB
	log               *zap.Logger
	notifier          *notifier
	producesArtifacts map[string]bool // job types that MUST use CompleteWithArtifacts
}

func NewSQLiteStore(db *sql.DB, log *zap.Logger) *SQLiteStore {
	return &SQLiteStore{db: db, log: log, notifier: newNotifier()}
}

// SetProducesArtifacts configures which job types produce artifacts and
// must use CompleteWithArtifacts instead of the legacy Complete path.
// Passing nil clears the gate (allows all types through Complete).
func (r *SQLiteStore) SetProducesArtifacts(types map[string]bool) {
	r.producesArtifacts = types
}

// ── In-process queue-notifier port (PR-Polling / ADR-0002 §D6.5) ────────────

// Subscribe returns a shared channel that wakes on every QueueChanged
// (Enqueue / Retry / RequeueExpiredLeases) notification. The returned
// channel is the LIVE channel at the call moment; the next Broadcast
// closes it AND replaces it with a fresh open channel (a subsequent
// Subscribe call returns the new channel, not the closed one).
//
// Implementation note: lifecycle is fully owned by *SQLiteStore (the
// notifier is constructed in NewSQLiteStore). Workers / runners
// subscribe per-loop via this method.
func (r *SQLiteStore) Subscribe() <-chan struct{} {
	return r.notifier.Subscribe()
}

// Broadcast closes the current notifier channel and replaces it with
// a fresh open channel. All in-flight subscribers unblock; new
// subscribers join the fresh channel.
//
// Trigger surface: this method is called from Create, Retry, and
// RequeueExpiredLeases — the only three canonical paths that ADD
// jobs to the queue. ClaimNext does NOT call Broadcast per ADR
// §D6.5 ("no fake availability": raw SQL operators do not get wakes).
//
// In-process scope: the broadcast is single-process only. A future
// postgres adapter will need a separate LISTEN/NOTIFY adapter (out of
// scope for PR-Polling / §D6.5 single-node).
func (r *SQLiteStore) Broadcast() {
	r.notifier.Broadcast()
}

// queueChanged is a private helper that centralises the
// "after a write added a job to the queue, wake every sleep­ing
// Worker" pattern. It is the canonical call site for Broadcast on
// the SQLiteStore write paths; triggering code MUST go through
// this helper rather than calling r.Broadcast() directly so the
// trigger set stays in one place (the linter cannot enforce this,
// but the doc-comment on Create / Retry / RequeueExpiredLeases pins
// the canonical call).
func (r *SQLiteStore) queueChanged() {
	r.Broadcast()
}

// DB returns the underlying *sql.DB for direct query access.
//
// Deprecated: prefer the typed methods on *SQLiteStore (Get, List, Create,
// Transition, ClaimNext, etc.) over raw *sql.DB access. Direct access
// bypasses the optimistic-lock guards, lease fencing, and queue-notifier
// broadcasts that make the store concurrency-safe. This method is kept for
// test fixtures and admin one-shot commands only. Scheduled for removal in
// 2026-Q4; new production callers will fail code review.
func (r *SQLiteStore) DB() *sql.DB { return r.db }

// Compile-time checks: the adapter satisfies the canonical persistence and
// queue-notification contracts without exposing either concrete type upstream.
var _ job.Store = (*SQLiteStore)(nil)
var _ job.QueueNotifier = (*SQLiteStore)(nil)

// Compile-time check: SQLiteStore satisfies the canonical job.JobBroker
// port (PR-B, Wave 22, June 2026). The same assertion will be added at
// the top of any future PostgreSQL adapter's repository file — the
// port + this assertion is the seam that lets internal/application/**
// depend on a portable interface instead of *SQLiteStore directly.
//
// Rationale for the embedding-not-alias choice (and the call sites a
// future PR-postgres author must touch): see ADR-0002 §D2 audit notes
// (`architecture/decisions/0002-p2-p3-roadmap.md`).
var _ job.JobBroker = (*SQLiteStore)(nil)

// GetStats + RefreshMetrics — extracted to repository_stats.go (PR-REPO-SPLIT, July 2026, SHA 152ca16d).
