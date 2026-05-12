package clips

import (
	"context"
	"database/sql"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"
	"go.uber.org/zap"
	"velox/go-master/pkg/models"
)

const testSchema = `
	CREATE TABLE clips (
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
	)
`

func TestDeleteClip(t *testing.T) {
	ctx := context.Background()

	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	_, err = db.Exec(testSchema)
	if err != nil {
		t.Fatal(err)
	}

	repo := NewRepository(db, zap.NewNop())

	err = repo.UpsertClip(ctx, &models.Clip{
		ID:        "clip_1",
		Name:      "Test Clip",
		Filename:  "test.mp4",
		Tags:      []string{"test"},
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	})
	if err != nil {
		t.Fatal(err)
	}

	if _, err := repo.GetClip(ctx, "clip_1"); err != nil {
		t.Fatalf("expected clip before delete: %v", err)
	}

	if err := repo.DeleteClip(ctx, "clip_1"); err != nil {
		t.Fatalf("DeleteClip failed: %v", err)
	}

	if _, err := repo.GetClip(ctx, "clip_1"); err == nil {
		t.Fatal("expected error after deleting clip, got nil")
	}
}

func TestDeleteClipByDriveLink(t *testing.T) {
	ctx := context.Background()

	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	_, err = db.Exec(testSchema)
	if err != nil {
		t.Fatal(err)
	}

	repo := NewRepository(db, zap.NewNop())

	clip := &models.Clip{
		ID:        "clip_2",
		Name:      "Drive Clip",
		DriveLink: "https://drive.google.com/file/d/123",
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}

	if err := repo.UpsertClip(ctx, clip); err != nil {
		t.Fatal(err)
	}

	if err := repo.DeleteClipByDriveLink(ctx, clip.DriveLink); err != nil {
		t.Fatalf("DeleteClipByDriveLink failed: %v", err)
	}

	if _, err := repo.GetClip(ctx, "clip_2"); err == nil {
		t.Fatal("expected error after deleting clip by drive link, got nil")
	}
}
