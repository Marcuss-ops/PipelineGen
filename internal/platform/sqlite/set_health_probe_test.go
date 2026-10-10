// Package sqlite — set_health_probe_test.go.
//
// Pins the readiness-path probe. It used to be `PRAGMA quick_check`, which
// scans every page: the 1.16 GB media plane took 10.6s per /ready poll and the
// 1.5 GB jobs plane never finished inside the budget, so /ready reported
// "context deadline exceeded" for healthy planes. The probe must therefore be
// O(1) (schema_version, a page-1 read) and still fail closed when the handle is
// broken.
package sqlite

import (
	"context"
	"database/sql"
	"testing"
	"time"
)

func TestPlaneLivenessCheckSucceedsOnALiveHandleAndFailsClosed(t *testing.T) {
	ctx := context.Background()

	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("open in-memory db: %v", err)
	}
	defer db.Close()

	if _, err := db.Exec(`CREATE TABLE probe (id INTEGER PRIMARY KEY)`); err != nil {
		t.Fatalf("create table: %v", err)
	}

	started := time.Now()
	if err := planeLivenessCheck(ctx, db); err != nil {
		t.Fatalf("live handle must pass the plane probe: %v", err)
	}
	// A page-1 read, not a full-file scan: the guard is intentionally loose so
	// a loaded CI machine cannot flake it, while a quick_check-scale scan on a
	// production-sized file (seconds) could never fit.
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("plane probe must be bounded, took %s", elapsed)
	}

	if err := planeLivenessCheck(ctx, nil); err == nil {
		t.Fatal("nil handle must fail closed")
	}

	if err := db.Close(); err != nil {
		t.Fatalf("close db: %v", err)
	}
	if err := planeLivenessCheck(ctx, db); err == nil {
		t.Fatal("closed handle must fail closed")
	}
}
