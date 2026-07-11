// Package jobs sentinels — error values returned by the canonical
// SQLite-backed job store. The Store interface that previously lived
// here was retired in Wave 17.1.2 (June 2026); the contract now lives
// in internal/domain/job.Store and *SQLiteStore is the only in-tree
// implementation.
//
// Fase 5(a) canonical-home alignment (July 2026): the 2 sentinels
// below are now thin re-export aliases of the canonical declarations
// at `internal/domain/job/errors.go`. Identity is preserved (same
// `error` value), so pre-Fase-5 callers (`appjobs.ErrLeaseLost`,
// `errors.Is(err, jobs.ErrLeaseLost)`) compile and probe unchanged.
// The `.Error()` message returns the domjob-formatted text (canonical
// surface); the legacy fmt.Errorf-formatted text is no longer the
// authoritative message. See godlike/06 SSOT rationale in the
// domain-side file header.
package jobs

import (
	domjob "github.com/Marcuss-ops/PipelineGen/internal/domain/job"
)

// ErrLeaseLost is the canonical Fase 5(a) re-export of
// `domjob.ErrLeaseLost`. Returned by worker-originated operations
// when the supplied lease_id no longer matches the job's current
// lease. Same `error` value as `domjob.ErrLeaseLost` —
// `errors.Is(err, jobs.ErrLeaseLost) == errors.Is(err, domjob.ErrLeaseLost)`.
var ErrLeaseLost = domjob.ErrLeaseLost

// ErrTransitionConflict is the canonical Fase 5(a) re-export of
// `domjob.ErrTransitionConflict`. Returned when the current status
// of the job does not match the expected status (concurrent
// modification via the CAS-fence on revision). Same `error` value
// as `domjob.ErrTransitionConflict`.
var ErrTransitionConflict = domjob.ErrTransitionConflict
