package app

import (
	"database/sql"
	"testing"

	_ "github.com/mattn/go-sqlite3"
	"go.uber.org/zap/zaptest"
)

// TestBuildJobsBundle_FieldsAreNonNil is the Phase-B safety guard for the
// Jobs module ownership inversion. It asserts that BuildJobsBundle returns
// a fully-populated *JobsBundle with non-nil Repo, Dispatcher, Service,
// and Facade fields.
//
// This test does NOT depend on the migration runner (no WireServices call),
// so it stays regression-safe even when other tests trip pre-existing
// migration issues (see docs/followups/2026-06-migration-053-test-failure.md).
func TestBuildJobsBundle_FieldsAreNonNil(t *testing.T) {
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("sql.Open in-memory: %v", err)
	}
	defer db.Close()

	log := zaptest.NewLogger(t)
	bundle, err := BuildJobsBundle(db, log)
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
	if _, err := BuildJobsBundle(nil, zaptest.NewLogger(t)); err == nil {
		t.Fatal("BuildJobsBundle(nil db) expected error, got nil")
	}
	if _, err := BuildJobsBundle(nil, nil); err == nil {
		t.Fatal("BuildJobsBundle(nil logger) expected error, got nil")
	}
}
