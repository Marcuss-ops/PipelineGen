package assetversions

import (
	"context"
	"sync"
	"testing"

	"velox/go-master/internal/storage"
)

const testSchema = `
CREATE TABLE IF NOT EXISTS asset_versions (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    asset_id        TEXT NOT NULL,
    version         INTEGER NOT NULL,
    content_hash    TEXT NOT NULL DEFAULT '',
    file_hash       TEXT NOT NULL DEFAULT '',
    file_size_bytes INTEGER NOT NULL DEFAULT 0,
    mime_type       TEXT NOT NULL DEFAULT '',
    metadata_json   TEXT NOT NULL DEFAULT '{}',
    created_by      TEXT NOT NULL DEFAULT '',
    created_at      TEXT NOT NULL DEFAULT '',
    UNIQUE (asset_id, version)
);
`

func newTestRepo(t *testing.T) (*Repository, func()) {
	t.Helper()
	db := storage.NewTestDBWithSchema(t, testSchema)
	return NewRepository(db), func() { db.Close() }
}

func TestCreateNext(t *testing.T) {
	ctx := context.Background()
	repo, cleanup := newTestRepo(t)
	defer cleanup()

	input := VersionInput{
		ContentHash:   "sha256:abc",
		FileHash:      "md5:def",
		FileSizeBytes: 1024,
		MimeType:      "video/mp4",
		MetadataJSON:  `{"encoder":"h264"}`,
		CreatedBy:     "pipelinegen",
	}

	// First version should be v1.
	v1, err := repo.CreateNext(ctx, "asset_atomic", input)
	if err != nil {
		t.Fatalf("CreateNext v1: %v", err)
	}
	if v1.Version != 1 {
		t.Errorf("expected version 1, got %d", v1.Version)
	}
	if v1.ContentHash != "sha256:abc" {
		t.Errorf("content_hash: got %s", v1.ContentHash)
	}

	// Second version should be v2.
	v2, err := repo.CreateNext(ctx, "asset_atomic", input)
	if err != nil {
		t.Fatalf("CreateNext v2: %v", err)
	}
	if v2.Version != 2 {
		t.Errorf("expected version 2, got %d", v2.Version)
	}
}

func TestCreateNext_RaceFree(t *testing.T) {
	ctx := context.Background()
	repo, cleanup := newTestRepo(t)
	defer cleanup()

	// Simulate two concurrent workers creating versions.
	const goroutines = 10
	var wg sync.WaitGroup
	versions := make(chan int, goroutines)

	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			input := VersionInput{
				ContentHash: "sha256:race_test",
				MimeType:    "video/mp4",
				CreatedBy:   "race_test",
			}
			v, err := repo.CreateNext(ctx, "asset_race", input)
			if err != nil {
				t.Errorf("CreateNext race: %v", err)
				return
			}
			versions <- v.Version
		}()
	}
	wg.Wait()
	close(versions)

	// Collect all versions.
	var got []int
	for v := range versions {
		got = append(got, v)
	}

	// We should have exactly goroutines unique version numbers (1..goroutines).
	if len(got) != goroutines {
		t.Fatalf("expected %d versions, got %d: %v", goroutines, len(got), got)
	}

	// Check that all version numbers are unique and in range 1..goroutines.
	seen := make(map[int]bool)
	for _, v := range got {
		if v < 1 || v > goroutines {
			t.Errorf("version out of range: %d", v)
		}
		if seen[v] {
			t.Errorf("duplicate version: %d", v)
		}
		seen[v] = true
	}
}

func TestCreateAndGet(t *testing.T) {
	ctx := context.Background()
	repo, cleanup := newTestRepo(t)
	defer cleanup()

	v := Version{
		AssetID:       "asset_v1",
		Version:       1,
		ContentHash:   "sha256:abc123",
		FileHash:      "md5:def456",
		FileSizeBytes: 1024,
		MimeType:      "video/mp4",
		MetadataJSON:  `{"encoder":"h264"}`,
		CreatedBy:     "pipelinegen",
	}
	if err := repo.Create(ctx, v); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	got, err := repo.Get(ctx, "asset_v1", 1)
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}
	if got == nil {
		t.Fatal("expected version, got nil")
	}
	if got.ContentHash != "sha256:abc123" {
		t.Errorf("content_hash: got %s", got.ContentHash)
	}
	if got.FileSizeBytes != 1024 {
		t.Errorf("file_size: got %d", got.FileSizeBytes)
	}
	if got.MimeType != "video/mp4" {
		t.Errorf("mime_type: got %s", got.MimeType)
	}
	if got.CreatedBy != "pipelinegen" {
		t.Errorf("created_by: got %s", got.CreatedBy)
	}
	if got.CreatedAt.IsZero() {
		t.Error("created_at should be populated")
	}
}

func TestGetCurrent(t *testing.T) {
	ctx := context.Background()
	repo, cleanup := newTestRepo(t)
	defer cleanup()

	repo.Create(ctx, Version{AssetID: "a1", Version: 1, ContentHash: "v1hash", MimeType: "video/mp4", CreatedBy: "manual"})
	repo.Create(ctx, Version{AssetID: "a1", Version: 2, ContentHash: "v2hash", MimeType: "video/mp4", CreatedBy: "pipelinegen"})

	current, err := repo.GetCurrent(ctx, "a1")
	if err != nil {
		t.Fatalf("GetCurrent: %v", err)
	}
	if current == nil {
		t.Fatal("expected current version, got nil")
	}
	if current.Version != 2 {
		t.Errorf("expected version 2, got %d", current.Version)
	}
	if current.ContentHash != "v2hash" {
		t.Errorf("content_hash: got %s", current.ContentHash)
	}
}

func TestGetCurrentNone(t *testing.T) {
	ctx := context.Background()
	repo, cleanup := newTestRepo(t)
	defer cleanup()

	current, err := repo.GetCurrent(ctx, "nonexistent")
	if err != nil {
		t.Fatalf("GetCurrent: %v", err)
	}
	if current != nil {
		t.Fatal("expected nil for asset with no versions")
	}
}

func TestList(t *testing.T) {
	ctx := context.Background()
	repo, cleanup := newTestRepo(t)
	defer cleanup()

	repo.Create(ctx, Version{AssetID: "a2", Version: 1, ContentHash: "h1", MimeType: "video/mp4"})
	repo.Create(ctx, Version{AssetID: "a2", Version: 2, ContentHash: "h2", MimeType: "video/mp4"})
	repo.Create(ctx, Version{AssetID: "a2", Version: 3, ContentHash: "h3", MimeType: "video/mp4"})

	versions, err := repo.List(ctx, "a2")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(versions) != 3 {
		t.Fatalf("expected 3 versions, got %d", len(versions))
	}
	if versions[0].Version != 3 {
		t.Errorf("expected version 3 first, got %d", versions[0].Version)
	}
}

func TestNextVersion(t *testing.T) {
	ctx := context.Background()
	repo, cleanup := newTestRepo(t)
	defer cleanup()

	v, err := repo.NextVersion(ctx, "new_asset")
	if err != nil {
		t.Fatalf("NextVersion (new): %v", err)
	}
	if v != 1 {
		t.Errorf("expected 1 for new asset, got %d", v)
	}

	repo.Create(ctx, Version{AssetID: "a3", Version: 1, ContentHash: "h1", MimeType: "video/mp4"})
	repo.Create(ctx, Version{AssetID: "a3", Version: 2, ContentHash: "h2", MimeType: "video/mp4"})

	v, err = repo.NextVersion(ctx, "a3")
	if err != nil {
		t.Fatalf("NextVersion: %v", err)
	}
	if v != 3 {
		t.Errorf("expected 3, got %d", v)
	}
}

func TestDelete(t *testing.T) {
	ctx := context.Background()
	repo, cleanup := newTestRepo(t)
	defer cleanup()

	repo.Create(ctx, Version{AssetID: "a4", Version: 1, ContentHash: "h1", MimeType: "video/mp4"})
	repo.Create(ctx, Version{AssetID: "a4", Version: 2, ContentHash: "h2", MimeType: "video/mp4"})

	if err := repo.Delete(ctx, "a4", 1); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	versions, _ := repo.List(ctx, "a4")
	if len(versions) != 1 {
		t.Fatalf("expected 1 version after delete, got %d", len(versions))
	}
	if versions[0].Version != 2 {
		t.Errorf("expected version 2, got %d", versions[0].Version)
	}
}

func TestDeleteAll(t *testing.T) {
	ctx := context.Background()
	repo, cleanup := newTestRepo(t)
	defer cleanup()

	repo.Create(ctx, Version{AssetID: "a5", Version: 1, ContentHash: "h1", MimeType: "video/mp4"})
	repo.Create(ctx, Version{AssetID: "a5", Version: 2, ContentHash: "h2", MimeType: "video/mp4"})

	if err := repo.DeleteAll(ctx, "a5"); err != nil {
		t.Fatalf("DeleteAll: %v", err)
	}

	versions, _ := repo.List(ctx, "a5")
	if len(versions) != 0 {
		t.Fatalf("expected 0 versions after DeleteAll, got %d", len(versions))
	}
}

func TestCreateDuplicate(t *testing.T) {
	ctx := context.Background()
	repo, cleanup := newTestRepo(t)
	defer cleanup()

	repo.Create(ctx, Version{AssetID: "a6", Version: 1, ContentHash: "h1", MimeType: "video/mp4"})
	err := repo.Create(ctx, Version{AssetID: "a6", Version: 1, ContentHash: "h2", MimeType: "video/mp4"})
	if err == nil {
		t.Fatal("expected error on duplicate version")
	}
}

func TestCreate_InvalidJSON(t *testing.T) {
	ctx := context.Background()
	repo, cleanup := newTestRepo(t)
	defer cleanup()

	err := repo.Create(ctx, Version{
		AssetID:      "asset_inv_json",
		Version:      1,
		ContentHash:  "abc",
		MimeType:     "video/mp4",
		MetadataJSON: "not-valid-json",
	})
	if err == nil {
		t.Fatal("expected error for invalid metadata_json in Create")
	}
}

func TestCreateNext_InvalidJSON(t *testing.T) {
	ctx := context.Background()
	repo, cleanup := newTestRepo(t)
	defer cleanup()

	_, err := repo.CreateNext(ctx, "asset_cn_inv", VersionInput{
		ContentHash:  "abc",
		MimeType:     "video/mp4",
		MetadataJSON: "broken json {{{",
	})
	if err == nil {
		t.Fatal("expected error for invalid metadata_json in CreateNext")
	}
}

func TestCreate_ValidJSON(t *testing.T) {
	ctx := context.Background()
	repo, cleanup := newTestRepo(t)
	defer cleanup()

	err := repo.Create(ctx, Version{
		AssetID:       "asset_vj",
		Version:       1,
		ContentHash:   "abc",
		MimeType:      "video/mp4",
		MetadataJSON:  `{"pipeline":"v3","resolution":"4K"}`,
	})
	if err != nil {
		t.Fatalf("expected success for valid metadata_json, got: %v", err)
	}

	v, _ := repo.Get(ctx, "asset_vj", 1)
	if v.MetadataJSON != `{"pipeline":"v3","resolution":"4K"}` {
		t.Errorf("metadata_json mismatch: got %s", v.MetadataJSON)
	}
}

func TestCreateNext_ValidJSON(t *testing.T) {
	ctx := context.Background()
	repo, cleanup := newTestRepo(t)
	defer cleanup()

	v, err := repo.CreateNext(ctx, "asset_cnv", VersionInput{
		ContentHash:  "abc",
		MimeType:     "video/mp4",
		MetadataJSON: `{"pipeline":"next","version":1}`,
	})
	if err != nil {
		t.Fatalf("expected success for valid metadata_json in CreateNext, got: %v", err)
	}
	if v.MetadataJSON != `{"pipeline":"next","version":1}` {
		t.Errorf("metadata_json mismatch: got %s", v.MetadataJSON)
	}
}
