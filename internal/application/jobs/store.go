// Package jobs — canonical application-layer wiring for the job subsystem.
//
// store.go re-exports the canonical SQLiteStore implementation from
// internal/infrastructure/database/sqlite/jobs as type alias + constructor.
// This keeps the compile graph closed (service.go, claims.go, worker.go all
// reference *SQLiteStore) without duplicating the ~700 LOC of method bodies
// that physically live in the infrastructure package.
//
// Migration: per architecture/migration.yaml Wave 5 PR 2 + Wave 10, the
// underlying methods will be folded directly into application/jobs in a
// later PR; for now this alias keeps the package compilable.
package jobs

import (
	"database/sql"

	"go.uber.org/zap"

	job "github.com/Marcuss-ops/PipelineGen/internal/domain/job"
	sqljobs "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/jobs"
)

// SQLiteStore is the canonical implementation of job.Store. Aliased from
// sqljobs.SQLiteStore so all methods defined in the infrastructure package
// continue to dispatch on the same underlying struct.
type SQLiteStore = sqljobs.SQLiteStore

// NewSQLiteStore constructs a SQLite-backed job store and returns it as
// *SQLiteStore (the application-layer alias). Callers should use this
// constructor instead of importing the infrastructure package directly
// so the composition root has a single canonical alias to use.
func NewSQLiteStore(db *sql.DB, log *zap.Logger) *SQLiteStore {
	return sqljobs.NewSQLiteStore(db, log)
}

// Compile-time guarantee that the SQLiteStore alias still satisfies the
// canonical job.Store interface defined in internal/domain/job. After the
// Wave 5 PR 3 removal of the application-layer `Store` type alias, this
// references the canonical interface directly.
var _ job.Store = (*SQLiteStore)(nil)

// ── Application-layer aliases ──────────────────────────────────────────────
//
// JobStats and ErrLeaseLost live physically in the infra package (where the
// own the SQL touch-points). They are re-exported here as aliases so callers
// in the application layer can refer to them by their canonical short name
// (`*JobStats`, `ErrLeaseLost`) without importing the infrastructure package.

// JobStats is the canonical aggregate job statistics payload returned by
// Service.GetStats. Aliased to the infra type so the wire-level JSON contract
// is identical and zero-copy.
type JobStats = sqljobs.JobStats

// ErrLeaseLost is the canonical sentinel returned when an operation is gated
// by a lease fencing tuple that no longer matches the row in the DB. Aliased
// from the infra package so `errors.Is(err, jobs.ErrLeaseLost)` works without
// importing internal/infrastructure/... at every call site.
var ErrLeaseLost = sqljobs.ErrLeaseLost
