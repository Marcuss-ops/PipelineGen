package catalogsync

import (
	"context"
	"testing"
	"time"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	drive "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/assets"
)

// catalogSyncTestSchema composes the canonical media_assets CREATE TABLE
// (see internal/infrastructure/database/canonical.go::CanonicalMediaAssetsSchema)
// plus the companion clip_folders table used by pruneMissingFolders in
// sync_prune.go. Composing the canonical block keeps this fixture in
// lockstep with production migrations — the previous inline 11-column
// media_assets block was a strict subset of canonical and has been folded
// per CANONICAL-DRIFT-MIG094 closure (June 2026): the test fixture only
// UPSERTs folders and never INSERTs into media_assets, so the canonical
// constant's DEFAULT clauses absorb the unused columns with no test
// impact. clip_folders stays inline because it is a test-local table
// representative (not a canonical production table).
const catalogSyncTestSchema = drive.CanonicalMediaAssetsSchema + `

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

	db := drive.NewTestDBWithSchema(t, catalogSyncTestSchema)
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
