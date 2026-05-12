package drivereconcile

import (
	"context"
	"database/sql"
	"testing"

	_ "github.com/mattn/go-sqlite3"
	"go.uber.org/zap"

	"velox/go-master/internal/repository/clips"
)

const testSchema = `CREATE TABLE clips (
	id TEXT PRIMARY KEY,
	name TEXT NOT NULL DEFAULT '',
	filename TEXT NOT NULL DEFAULT '',
	folder_id TEXT NOT NULL DEFAULT '',
	folder_path TEXT NOT NULL DEFAULT '',
	group_name TEXT NOT NULL DEFAULT '',
	media_type TEXT NOT NULL DEFAULT '',
	drive_link TEXT NOT NULL DEFAULT '',
	drive_file_id TEXT NOT NULL DEFAULT '',
	download_link TEXT NOT NULL DEFAULT '',
	tags TEXT NOT NULL DEFAULT '[]',
	source TEXT NOT NULL DEFAULT '',
	category TEXT NOT NULL DEFAULT '',
	external_url TEXT NOT NULL DEFAULT '',
	duration INTEGER NOT NULL DEFAULT 0,
	metadata TEXT NOT NULL DEFAULT '{}',
	file_hash TEXT NOT NULL DEFAULT '',
	local_path TEXT NOT NULL DEFAULT '',
	status TEXT NOT NULL DEFAULT '',
	error TEXT NOT NULL DEFAULT '',
	thumb_url TEXT NOT NULL DEFAULT '',
	search_terms TEXT NOT NULL DEFAULT '[]',
	phash TEXT NOT NULL DEFAULT '',
	visual_embedding_json TEXT NOT NULL DEFAULT '[]',
	parent_folder_id TEXT DEFAULT '',
	depth INTEGER DEFAULT 0,
	is_folder INTEGER DEFAULT 0,
	duration_seconds REAL,
	width INTEGER DEFAULT 0,
	height INTEGER DEFAULT 0,
	fps REAL DEFAULT 0,
	codec TEXT DEFAULT '',
	size_bytes INTEGER DEFAULT 0,
	processing_stage TEXT,
	error_message TEXT,
	retry_count INTEGER DEFAULT 0,
	last_attempt_at TEXT,
	processed_at TEXT,
	created_at TEXT NOT NULL,
	updated_at TEXT NOT NULL
)`

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

	_, err = db.Exec(testSchema)
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
