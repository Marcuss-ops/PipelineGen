package artlist

import (
	"context"
	"os"
	"testing"

	"go.uber.org/zap"
	"velox/go-master/internal/repository/clips"
	"velox/go-master/internal/storage"
)

func TestArtlistService_Smoke(t *testing.T) {
	log, _ := zap.NewDevelopment()
	ctx := context.Background()

	// 1. Setup temporary DB
	tmpDir, err := os.MkdirTemp("", "artlist_test_*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	db, err := storage.NewSQLiteDB(tmpDir, "test.db", log)
	if err != nil {
		t.Fatalf("failed to init db: %v", err)
	}
	defer db.Close()

	// Run migrations (minimal for clips)
	_, err = db.DB.Exec("CREATE TABLE IF NOT EXISTS clips (id TEXT PRIMARY KEY, name TEXT, filename TEXT, folder_id TEXT, folder_path TEXT, group_name TEXT, media_type TEXT, drive_link TEXT, download_link TEXT, tags TEXT, source TEXT, category TEXT, external_url TEXT, duration INTEGER, metadata TEXT, local_path TEXT, file_hash TEXT, created_at DATETIME DEFAULT CURRENT_TIMESTAMP, updated_at DATETIME DEFAULT CURRENT_TIMESTAMP)")
	if err != nil {
		t.Fatalf("failed to create table: %v", err)
	}
	_, err = db.DB.Exec("CREATE TABLE IF NOT EXISTS clip_tags (clip_id TEXT, tag TEXT, UNIQUE(clip_id, tag))")

	repo := clips.NewRepository(db.DB)

	// 2. Initialize Service (without DriveClient for now to test pure logic)
	svc, err := NewService(db.DB, "", tmpDir, repo, nil, "root_folder", log)
	if err != nil {
		t.Fatalf("failed to create service: %v", err)
	}

	t.Run("ParallelSyncLogic", func(t *testing.T) {
		req := &SyncRequest{
			Terms:  []string{"nature", "tech", "urban"},
			Limit:  2,
			SaveDB: true,
		}

		// We expect this to run and call searchLive, which will fail because node is not there,
		// but it verifies the parallel loop and worker pool logic doesn't crash.
		resp, err := svc.Sync(ctx, req)
		if err != nil {
			t.Logf("Sync returned error as expected: %v", err)
		} else {
			t.Logf("Sync response (failed tasks expected): %+v", resp)
		}
	})

	t.Run("SanitizeFilename", func(t *testing.T) {
		input := "Video: Nature/Forest*Test?"
		expected := "Video_ Nature_Forest_Test_"
		result := sanitizeFilename(input)
		if result != expected {
			t.Errorf("expected %s, got %s", expected, result)
		}
	})
}
