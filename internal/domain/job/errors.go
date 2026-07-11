// Package job — errors.go: canonical typed-error sentinels (Fase 5(a),
// July 2026).
//
// godlike/06 SSOT (one canonical owner per fact): this file is the
// SOLE canonical home for the cross-package job-related sentinels.
//
// Pre-Fase-5 distribution (forward-pointer to Push 5.2 cutover):
//
//   - ErrLeaseLost          → internal/infrastructure/database/sqlite/jobs/store.go:14
//                              (canonical sqlite self-decl; kept as re-export alias)
//   - ErrTransitionConflict → internal/infrastructure/database/sqlite/jobs/store.go:18
//                              (single-decl; kept as re-export alias)
//   - ErrJobNotFound        → internal/infrastructure/database/sqlite/jobs/repository.go
//                              (single-decl)
//   - ErrFinalizeAttempt*   → internal/infrastructure/database/sqlite/jobs/finalize_attempt.go
//                              (Push 4.2 decls; migrated to canonical home)
//
// godlike/07 typed-error contract discipline: every sentinel below is a
// `var X = errors.New("...")` so callers branch reactively via
// `errors.Is(err, domjob.ErrX)` rather than string-compare gymnastics.
//
// Errors.Is(err, domjob.ErrX) is the canonical probe.
//
// Fase 5(b) cutover (NO caller migration in this push):
//
//   - internal/infrastructure/database/sqlite/jobs/store.go will
//     alias `var ErrLeaseLost = domjob.ErrLeaseLost` and drop the
//     canonical local decl (same identity, no callers break).
//   - internal/infrastructure/database/sqlite/outboxevents/repository.go
//     and internal/infrastructure/database/sqlite/assets/channels_repository.go
//     will remain local — outbox + channels are typed differently
//     (no canonical lease-lost contract shared with job-layer).
//   - internal/application/jobs/errors.go will drop the
//     `dbjobs.ErrLeaseLost` re-export alias in favor of
//     `domjob.ErrLeaseLost` directly.
package job

import "errors"

// ErrLeaseLost is returned when a worker-originated operation fails because
// the supplied lease_id no longer matches the job's current lease (the job
// has been reassigned to another worker) or the lease has expired.
//
// This is the canonical Fase 5(a) sentinel. The previous canonical home
// (internal/infrastructure/database/sqlite/jobs/store.go) is now a
// transparent re-export alias — same `error` value, two import paths,
// probes via errors.Is are equivalent.
//
// Errors.Is(err, ErrLeaseLost) is the canonical probe.
var ErrLeaseLost = errors.New("jobs: lease lost — the job has been reassigned to another worker (fenced operation rejected)")

// ErrTransitionConflict is returned when a state transition is attempted
// against a row whose current state does not match the expected pre-state
// (concurrent modification detected via the CAS-fence on revision).
//
// This is the canonical Fase 5(a) sentinel. The previous canonical home
// (internal/infrastructure/database/sqlite/jobs/store.go) is now a
// transparent re-export alias.
//
// Errors.Is(err, ErrTransitionConflict) is the canonical probe.
var ErrTransitionConflict = errors.New("jobs: transition conflict — current status/lease/revision differs from expected (CAS-fence mismatch)")

// ErrJobNotFound is returned by Store.Get/List/JobEvents paths when the
// queried jobID does not exist in the jobs table.
//
// This is the canonical Fase 5(a) sentinel. The previous canonical home
// (internal/infrastructure/database/sqlite/jobs/repository.go) is now a
// transparent re-export alias.
//
// Errors.Is(err, ErrJobNotFound) is the canonical probe.
var ErrJobNotFound = errors.New("jobs: job not found (no row for the requested jobID)")

// ── FinalizeAttempt sentinels (Pushed-by Phase 4(a) Push 4.2) ──────────
//
// godlike/06 SSOT: these 6 sentinels were originally declared at the
// implementation site (internal/infrastructure/database/sqlite/jobs/finalize_attempt.go).
// Fase 5(a) re-homes them here so the application layer can probe them
// without importing the SQLite infrastructure package. The canonical
// implementation sites retain re-export aliases for pre-Fase-5 callers.

// ErrFinalizeAttemptOutcomeInvalid — caller supplied an Outcome outside
// the canonical enum {Succeeded, FailedPermanent, ScheduleRetry}.
//
// godlike/07 fail-closed guard at the FENCE: this sentinel surfaces
// before BeginTx so a bad command doesn't pin a connection in a doomed
// transaction.
//
// Errors.Is(err, ErrFinalizeAttemptOutcomeInvalid) is the canonical probe.
var ErrFinalizeAttemptOutcomeInvalid = errors.New("FinalizeAttempt: outcome not in canonical enum {Succeeded, FailedPermanent, ScheduleRetry} (fail-closed guard at fence)")

// ErrFinalizeAttemptResultMissing — OutcomeSucceeded with empty cmd.Result
// (would silently default to {} on the row, violating wire consistency).
//
// godlike/07 no-fake-availability: silent-default values are explicit
// typed sentinels, NEVER empty defaults.
//
// Errors.Is(err, ErrFinalizeAttemptResultMissing) is the canonical probe.
var ErrFinalizeAttemptResultMissing = errors.New("FinalizeAttempt: SUCCEEDED outcome requires non-empty Result (wire-consistency guard)")

// ErrFinalizeAttemptErrorMissing — non-Succeeded outcome with empty
// cmd.ErrorMessage (silent-empty error message is a hostile trap).
//
// Errors.Is(err, ErrFinalizeAttemptErrorMissing) is the canonical probe.
var ErrFinalizeAttemptErrorMissing = errors.New("FinalizeAttempt: non-SUCCEEDED outcome requires non-empty ErrorMessage (silent-empty error trap)")

// ErrFinalizeAttemptArtifactStale — ArtifactState patch did NOT match
// (artifact missing, wrong job_id, or already-terminal state).
//
// godlike/07 fail-closed semantics: re-patching a terminal artifact
// is observably a no-op; this sentinel surfaces the bug to operators
// instead of silently succeeding.
//
// Errors.Is(err, ErrFinalizeAttemptArtifactStale) is the canonical probe.
var ErrFinalizeAttemptArtifactStale = errors.New("FinalizeAttempt: artifact-state patch stale (row missing, job-id mismatch, or already-terminal state)")

// ErrFinalizeAttemptOutboxEventMissing — OutboxEvent entry has empty
// Type or EventKey (would violate event_key UNIQUE idempotency).
//
// Errors.Is(err, ErrFinalizeAttemptOutboxEventMissing) is the canonical probe.
var ErrFinalizeAttemptOutboxEventMissing = errors.New("FinalizeAttempt: outbox event missing required Type or EventKey (uniqueness invariant)")

// ErrFinalizeAttemptDLQIncompatible — DLQPayload supplied with
// OutcomeSucceeded (incompatible; DLQ is reserved for terminal failure).
//
// Errors.Is(err, ErrFinalizeAttemptDLQIncompatible) is the canonical probe.
var ErrFinalizeAttemptDLQIncompatible = errors.New("FinalizeAttempt: DLQPayload is only valid with FAILED_PERMANENT or SCHEDULE_RETRY outcomes (terminal-failure invariant)")
