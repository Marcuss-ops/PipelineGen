package catalogsync

import (
	"context"
	"database/sql"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"
	"go.uber.org/zap"

	"velox/go-master/internal/media/models"
	"velox/go-master/internal/repository/clips"
)

const catalogSyncTestSchema = `
	CREATE TABLE media_assets (
		id TEXT PRIMARY KEY,
		source TEXT NOT NULL DEFAULT '',
		name TEXT NOT NULL DEFAULT '',
		tags TEXT NOT NULL DEFAULT '[]',
		tags_norm TEXT NOT NULL DEFAULT '',
		embedding_json TEXT NOT NULL DEFAULT '[]',
		duration_ms INTEGER NOT NULL DEFAULT 0,
		url TEXT NOT NULL DEFAULT '',
		created_at TEXT,
		metadata_json TEXT NOT NULL DEFAULT '{}'
	);

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

func TestPruneMissingFoldersDeletesStaleRecords(t *testing.T) {
	ctx := context.Background()

	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	if _, err := db.Exec(catalogSyncTestSchema); err != nil {
		t.Fatal(err)
	}

	repo := clips.NewRepository(db, zap.NewNop())
	now := time.Now().UTC()
	for _, folder := range []*models.ClipFolder{
		{
			ID:         "folder_row_keep",
			Source:     "artlist",
			FolderID:   "keep-folder-id",
			FolderPath: "Keep",
			CreatedAt:  now,
			UpdatedAt:  now,
		},
		{
			ID:         "folder_row_drop",
			Source:     "artlist",
			FolderID:   "drop-folder-id",
			FolderPath: "Drop",
			CreatedAt:  now,
			UpdatedAt:  now,
		},
	} {
		if err := repo.UpsertClipFolder(ctx, folder); err != nil {
			t.Fatal(err)
		}
	}

	svc := &Service{}
	seen := map[string]struct{}{
		"keep-folder-id": {},
	}

	if err := svc.pruneMissingFolders(ctx, repo, "artlist", seen); err != nil {
		t.Fatalf("pruneMissingFolders failed: %v", err)
	}

	folders, err := repo.ListClipFolders(ctx, "artlist")
	if err != nil {
		t.Fatal(err)
	}
	if len(folders) != 1 {
		t.Fatalf("expected 1 remaining folder, got %d", len(folders))
	}
	if folders[0].FolderID != "keep-folder-id" {
		t.Fatalf("expected keep-folder-id to remain, got %q", folders[0].FolderID)
	}
}
