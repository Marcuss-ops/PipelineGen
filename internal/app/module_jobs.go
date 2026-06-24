package app

import (
	"fmt"

	appjobs "github.com/Marcuss-ops/PipelineGen/internal/application/jobs"
	job "github.com/Marcuss-ops/PipelineGen/internal/domain/job"
	storage "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database"
	sqljobs "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/jobs"
	"go.uber.org/zap"
)

// JobsBundle is the Job module's *owned* runtime surface.
//
// Phase-B ownership inversion (June 2026): these objects are constructed
// once by BuildJobsBundle, returned as a typed bundle, and consumed by
// composeIntegration for cross-module handler registration.
type JobsBundle struct {
	Repo       *sqljobs.SQLiteStore
	Dispatcher *appjobs.Dispatcher
	Service    *appjobs.Service
	Facade     job.Service // canonical domain interface satisfied by *appjobs.Service
}

// BuildJobsBundle constructs the Job runtime pieces in the canonical order:
//
//  1. SQLite-backed job Store.
//  2. In-process Dispatcher (handler registry; kept nil-free until Freeze).
//  3. The application Service that orchestrates enqueue / list / cancel /
//     status propagation.
//  4. The domain job.Service interface satisfied by *appjobs.Service.
//
// Returns a JobsBundle. The bundle is fully constructed but its Dispatcher
// is NOT frozen yet — freezing is performed by WireServices in bootstrap.go
// *after* WireRegistry, so that no new module can register a handler while
// workers are claiming jobs.
//
// Returning `(nil, error)` is reserved for unrecoverable construction errors
// (nil db / nil logger). All four fields are required to be non-nil on success.
func BuildJobsBundle(db *storage.SQLiteDB, log *zap.Logger) (*JobsBundle, error) {
	if db == nil {
		return nil, fmt.Errorf("build jobs bundle: db is nil")
	}
	if log == nil {
		return nil, fmt.Errorf("build jobs bundle: log is nil")
	}

	repo := sqljobs.NewSQLiteStore(db.DB, log)
	dispatcher := appjobs.NewDispatcher()
	svc := appjobs.NewService(repo, dispatcher, log)

	// *appjobs.Service satisfies the domain job.Service interface directly.
	// No facade needed — consumers declare their dependency as job.Service
	// (interface value) and the composition root injects this concrete pointer.
	return &JobsBundle{
		Repo:       repo,
		Dispatcher: dispatcher,
		Service:    svc,
		Facade:     svc,
	}, nil
}
