package drivereconcile

import (
	"context"
	"database/sql"
	"testing"

	_ "github.com/mattn/go-sqlite3"
	"go.uber.org/zap"

	"velox/go-master/internal/repository/clips"
)

func TestNewService(t *testing.T) {
	repo := &clips.Repository{}
	svc := NewService(repo, nil, zap.NewNop())
	if svc == nil {
		t.Fatal("expected service, got nil")
	}
}

func TestReconcile_EmptySource_NoPanic(t *testing.T) {
	// Use in-memory DB to avoid nil pointer
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	_, err = db.Exec(`CREATE TABLE clips (
		id TEXT PRIMARY KEY,
		name TEXT,
		filename TEXT,
		folder_id TEXT,
		folder_path TEXT,
		group_name TEXT,
		media_type TEXT,
		drive_link TEXT,
		download_link TEXT,
		tags TEXT,
		source TEXT,
		category TEXT,
		external_url TEXT,
		duration REAL,
		metadata TEXT,
		file_hash TEXT,
		local_path TEXT,
		created_at TEXT,
		updated_at TEXT
	)`)
	if err != nil {
		t.Fatal(err)
	}

	repo := clips.NewRepository(db, zap.NewNop())
	svc := NewService(repo, nil, zap.NewNop())

	result, err := svc.Reconcile(context.Background(), "", "", true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected result, got nil")
	}
	if !result.DryRun {
		t.Fatal("expected dry run to be true")
	}
}

// Note: Full integration tests would require a mock Drive service
// The Reconcile method with a nil driveSvc will skip the Drive->SQLite check
// and only check SQLite->Drive (which requires a repo with data)
