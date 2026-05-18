package clips

import (
	"context"
	"database/sql"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"
	"go.uber.org/zap"
	"velox/go-master/internal/media/models"
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
		updated_at TEXT NOT NULL,
		deleted_at DATETIME
		);
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

	err = repo.UpsertClip(ctx, &models.MediaAsset{
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

	// Verify that GetClip excludes it
	clip, err := repo.GetClip(ctx, "clip_1")
	if clip != nil {
		t.Fatal("expected nil clip after deleting clip")
	}

	// Verify the record is actually still in the DB (soft delete check)
	var deletedAt sql.NullString
	err = db.QueryRowContext(ctx, "SELECT deleted_at FROM clips WHERE id = 'clip_1'").Scan(&deletedAt)
	if err != nil {
		t.Fatalf("expected record to still exist in db, got err: %v", err)
	}
	if !deletedAt.Valid || deletedAt.String == "" {
		t.Fatal("expected deleted_at to be set")
	}

	// Test SearchClipsByKeywords excludes it
	clips, err := repo.SearchClipsByKeywords(ctx, "", []string{"test"}, 10)
	if err != nil {
		t.Fatalf("search failed: %v", err)
	}
	if len(clips) > 0 {
		t.Fatal("expected search to exclude soft-deleted clips")
	}
}

func TestRestoreClip(t *testing.T) {
	ctx := context.Background()

	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	_, _ = db.Exec(testSchema)
	repo := NewRepository(db, zap.NewNop())

	_ = repo.UpsertClip(ctx, &models.MediaAsset{
		ID:        "clip_res",
		Name:      "Restore Clip",
		Tags:      []string{"restore"},
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	})

	_ = repo.DeleteClip(ctx, "clip_res")

	// Ensure excluded
	clip, _ := repo.GetClip(ctx, "clip_res")
	if clip != nil {
		t.Fatal("expected nil clip after soft delete")
	}

	// Restore
	if err := repo.RestoreClip(ctx, "clip_res"); err != nil {
		t.Fatalf("RestoreClip failed: %v", err)
	}

	// Ensure present
	clip, err = repo.GetClip(ctx, "clip_res")
	if err != nil || clip == nil {
		t.Fatalf("expected clip to be present after restore, err: %v", err)
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

	clip := &models.MediaAsset{
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

	c, _ := repo.GetClip(ctx, "clip_2")
	if c != nil {
		t.Fatal("expected nil clip after deleting by drive link")
	}

	// Verify the record is actually still in the DB (soft delete check)
	var deletedAt sql.NullString
	err = db.QueryRowContext(ctx, "SELECT deleted_at FROM clips WHERE id = 'clip_2'").Scan(&deletedAt)
	if err != nil || !deletedAt.Valid || deletedAt.String == "" {
		t.Fatalf("expected record to still exist with deleted_at set, got err: %v", err)
	}
}
