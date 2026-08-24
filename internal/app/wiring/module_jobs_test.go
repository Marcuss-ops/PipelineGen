package wiring

import (
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/app/wiring"
	storage "github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite"
	"go.uber.org/zap/zaptest"
)

// TestBuildJobsBundle_FieldsAreNonNil is the Phase-B safety guard for the
// Jobs module ownership inversion. It asserts that BuildJobsBundle returns
// a fully-populated *JobsBundle with non-nil Repo, Dispatcher, Service,
// and Facade fields.
//
// This test does NOT depend on the migration runner (no WireServices call),
// so it stays regression-safe even when other tests trip pre-existing
// migration issues.
func TestBuildJobsBundle_FieldsAreNonNil(t *testing.T) {
	// PG-011 typed-handle migration (June 2026): *storage.SQLiteDB
	// fixture; BuildJobsBundle receives sqliteDB.DB (the embedded
	// *sql.DB handle) so the test file can drop its bare database/sql
	// import while exercising the production ctor signature.
	sqliteDB, err := storage.OpenSQLiteDB(":memory:", zaptest.NewLogger(t))
	if err != nil {
		t.Fatalf("storage.OpenSQLiteDB: %v", err)
	}
	t.Cleanup(func() { _ = sqliteDB.Close() })

	log := zaptest.NewLogger(t)
	// PG-011 typed-handle migration (June 2026): BuildJobsBundle
	// signature is now `*storage.SQLiteDB` so we pass the typed
	// handle directly (no `.DB` accessor).
	bundle, err := wiring.BuildJobsBundle(sqliteDB, log, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("BuildJobsBundle: %v", err)
	}
	if bundle == nil {
		t.Fatal("bundle is nil")
	}
	if bundle.Repo == nil {
		t.Fatal("bundle.Repo is nil")
	}
	if bundle.Dispatcher == nil {
		t.Fatal("bundle.Dispatcher is nil")
	}
	if bundle.Service == nil {
		t.Fatal("bundle.Service is nil")
	}
	if bundle.Facade == nil {
		t.Fatal("bundle.Facade is nil")
	}
}

// TestBuildJobsBundle_RejectsNilInputs pins down the defensive guards so
// future callers can rely on the documented error contract.
func TestBuildJobsBundle_RejectsNilInputs(t *testing.T) {
	if _, err := wiring.BuildJobsBundle(nil, zaptest.NewLogger(t), nil, nil, nil, nil); err == nil {
		t.Fatal("wiring.BuildJobsBundle(nil db) expected error, got nil")
	}
	if _, err := wiring.BuildJobsBundle(nil, nil, nil, nil, nil, nil); err == nil {
		t.Fatal("wiring.BuildJobsBundle(nil logger) expected error, got nil")
	}
}
