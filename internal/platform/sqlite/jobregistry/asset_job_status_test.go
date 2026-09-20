package jobregistry

import (
	"context"
	"database/sql"
	"testing"

	_ "github.com/mattn/go-sqlite3"
)

// newLedgerDB builds the minimal production-faithful schema the reader touches.
func newLedgerDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Exec(`
		CREATE TABLE jobs (
			id TEXT PRIMARY KEY,
			status TEXT NOT NULL,
			retry_count INTEGER NOT NULL DEFAULT 0,
			created_at TEXT NOT NULL
		);
		CREATE TABLE job_asset_relations (
			job_id TEXT NOT NULL,
			asset_id TEXT NOT NULL,
			relation TEXT NOT NULL,
			step_id TEXT NOT NULL DEFAULT '',
			ordinal INTEGER NOT NULL,
			created_at TEXT NOT NULL,
			PRIMARY KEY(job_id, asset_id, relation, step_id)
		);`); err != nil {
		t.Fatalf("create schema: %v", err)
	}
	return db
}

func seedJob(t *testing.T, db *sql.DB, id, status string, retry int, createdAt, assetID string) {
	t.Helper()
	if _, err := db.Exec(`INSERT INTO jobs (id, status, retry_count, created_at) VALUES (?,?,?,?)`,
		id, status, retry, createdAt); err != nil {
		t.Fatalf("insert job: %v", err)
	}
	if assetID != "" {
		if _, err := db.Exec(`INSERT INTO job_asset_relations (job_id, asset_id, relation, ordinal, created_at) VALUES (?,?,?,?,?)`,
			id, assetID, "GENERATED", 0, createdAt); err != nil {
			t.Fatalf("insert relation: %v", err)
		}
	}
}

func TestLatestAssetJobStatusReturnsMostRecentJob(t *testing.T) {
	db := newLedgerDB(t)
	seedJob(t, db, "job_old", "SUCCEEDED", 0, "2026-09-20T08:00:00Z", "yt_1")
	seedJob(t, db, "job_new", "RUNNING", 2, "2026-09-20T09:00:00Z", "yt_1")
	seedJob(t, db, "job_other", "FAILED", 5, "2026-09-20T10:00:00Z", "yt_other")

	reg, err := New(db)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	status, retry, found := reg.LatestAssetJobStatus(context.Background(), "yt_1")
	if !found {
		t.Fatal("found=false, want true")
	}
	if status != "RUNNING" || retry != 2 {
		t.Fatalf("got %s/%d, want RUNNING/2 (the newest job for the asset)", status, retry)
	}
}

func TestLatestAssetJobStatusUnknownAssetIsNotFound(t *testing.T) {
	db := newLedgerDB(t)
	seedJob(t, db, "job_1", "RUNNING", 1, "2026-09-20T09:00:00Z", "yt_1")
	reg, _ := New(db)

	if _, _, found := reg.LatestAssetJobStatus(context.Background(), "yt_missing"); found {
		t.Fatal("found=true for an asset with no relation, want false")
	}
	if _, _, found := reg.LatestAssetJobStatus(context.Background(), "  "); found {
		t.Fatal("found=true for an empty asset id, want false")
	}
}

func TestLatestAssetJobStatusNilReceiverFailsClosed(t *testing.T) {
	var reg *Registry
	if _, _, found := reg.LatestAssetJobStatus(context.Background(), "yt_1"); found {
		t.Fatal("found=true on a nil receiver, want false")
	}
}
