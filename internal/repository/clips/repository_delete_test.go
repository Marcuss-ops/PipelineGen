package clips

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"go.uber.org/zap"
	"github.com/Marcuss-ops/PipelineGen/internal/media/models"
	"github.com/Marcuss-ops/PipelineGen/internal/storage"
)

// testSchema composes the canonical media_assets CREATE TABLE (see
// internal/storage/canonical.go::CanonicalMediaAssetsSchema) plus the
// clip_folders companion table used by Repository.GetClipFolder /
// ListClipFolders. DeleteClip / RestoreClip / DeleteClipByDriveLink all
// UPDATE lifecycle_state (canonical soft-delete column from migration
// 037) so the canonical block is mandatory here.
const testSchema = storage.CanonicalMediaAssetsSchema + `

	CREATE TABLE clip_folders (
		id TEXT PRIMARY KEY,
		source TEXT NOT NULL DEFAULT '',
		source_url TEXT NOT NULL DEFAULT '',
		video_id TEXT NOT NULL DEFAULT '',
		folder_id TEXT NOT NULL DEFAULT '',
		folder_path TEXT NOT NULL DEFAULT '',
		local_folder_path TEXT NOT NULL DEFAULT '',
		group_name TEXT NOT NULL DEFAULT '',
		manifest_txt_path TEXT NOT NULL DEFAULT '',
		manifest_json_path TEXT NOT NULL DEFAULT '',
		clip_count INTEGER NOT NULL DEFAULT 0,
		processed_count INTEGER NOT NULL DEFAULT 0,
		failed_count INTEGER NOT NULL DEFAULT 0,
		skipped_count INTEGER NOT NULL DEFAULT 0,
		last_error TEXT NOT NULL DEFAULT '',
		metadata TEXT NOT NULL DEFAULT '{}',
		created_at TEXT NOT NULL DEFAULT '',
		updated_at TEXT NOT NULL DEFAULT '',
		search_key TEXT
	);
`

func TestDeleteClip(t *testing.T) {
	ctx := context.Background()

	db := storage.NewTestDBWithSchema(t, testSchema)
	defer db.Close()

	repo := NewRepository(db, zap.NewNop())

	err := repo.UpsertClip(ctx, &models.MediaAsset{
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

	clip, err := repo.GetClip(ctx, "clip_1")
	if clip != nil {
		t.Fatal("expected nil clip after deleting clip")
	}
	_ = err

	var deletedAt sql.NullString
	err = db.QueryRowContext(ctx, "SELECT deleted_at FROM media_assets WHERE id = 'clip_1'").Scan(&deletedAt)
	if err != nil {
		t.Fatalf("expected record to still exist in db, got err: %v", err)
	}
	if !deletedAt.Valid || deletedAt.String == "" {
		t.Fatal("expected deleted_at to be set")
	}

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

	db := storage.NewTestDBWithSchema(t, testSchema)
	defer db.Close()
	repo := NewRepository(db, zap.NewNop())

	_ = repo.UpsertClip(ctx, &models.MediaAsset{
		ID:        "clip_res",
		Name:      "Restore Clip",
		Tags:      []string{"restore"},
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	})

	_ = repo.DeleteClip(ctx, "clip_res")

	clip, _ := repo.GetClip(ctx, "clip_res")
	if clip != nil {
		t.Fatal("expected nil clip after soft delete")
	}

	if err := repo.RestoreClip(ctx, "clip_res"); err != nil {
		t.Fatalf("RestoreClip failed: %v", err)
	}

	clip, err := repo.GetClip(ctx, "clip_res")
	if err != nil || clip == nil {
		t.Fatalf("expected clip to be present after restore, err: %v", err)
	}
}

func TestDeleteClipByDriveLink(t *testing.T) {
	ctx := context.Background()

	db := storage.NewTestDBWithSchema(t, testSchema)
	defer db.Close()

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

	var deletedAt sql.NullString
	err := db.QueryRowContext(ctx, "SELECT deleted_at FROM media_assets WHERE id = 'clip_2'").Scan(&deletedAt)
	if err != nil || !deletedAt.Valid || deletedAt.String == "" {
		t.Fatalf("expected record to still exist with deleted_at set, got err: %v", err)
	}
}
