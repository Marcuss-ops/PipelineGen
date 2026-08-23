package catalogsync

import (
	"context"
	"testing"
	"time"

	"go.uber.org/zap"

	drive "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/assets"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
)

func TestPruneMissingFoldersDeletesStaleRecords(t *testing.T) {
	ctx := context.Background()

	db := drive.NewMigratedTestDB(t)
	defer db.Close()

	repo := assets.NewClipsRepository(db, zap.NewNop())
	now := time.Now().UTC()
	for _, folder := range []*asset.ClipFolder{
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
		if err := repo.UpsertFolder(ctx, folder); err != nil {
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

	folders, err := repo.ListFolders(ctx, "artlist")
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
