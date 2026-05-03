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

func TestDeleteClip(t *testing.T) {
	ctx := context.Background()

	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	_, err = db.Exec(`
		CREATE TABLE clips (
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
		)
	`)
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

	_, err = db.Exec(`
		CREATE TABLE clips (
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
		)
	`)
	if err != nil {
		t.Fatal(err)
	}

	repo := NewRepository(db, zap.NewNop())

	clip := &models.Clip{
		ID:           "clip_2",
		Name:         "Test Clip 2",
		Filename:     "test2.mp4",
		DriveLink:    "https://drive.google.com/file/d/ABC123/view",
		DownloadLink: "https://drive.google.com/uc?id=ABC123",
		Tags:         []string{"test"},
		CreatedAt:    time.Now(),
		UpdatedAt:    time.Now(),
	}

	if err := repo.UpsertClip(ctx, clip); err != nil {
		t.Fatal(err)
	}

	if err := repo.DeleteClipByDriveLink(ctx, "https://drive.google.com/file/d/ABC123/view"); err != nil {
		t.Fatalf("DeleteClipByDriveLink failed: %v", err)
	}

	if _, err := repo.GetClip(ctx, "clip_2"); err == nil {
		t.Fatal("expected error after deleting clip by drive link, got nil")
	}
}
