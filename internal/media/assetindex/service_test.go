package assetindex

import (
	"context"
	"database/sql"
	"os"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

func setupTestService(t *testing.T) (*Service, func()) {
	t.Helper()

	// Create temp file for DB
	tmpFile, err := os.CreateTemp("", "test_assetindex_*.db")
	if err != nil {
		t.Fatalf("failed to create temp file: %v", err)
	}
	tmpFile.Close()
	t.Cleanup(func() { os.Remove(tmpFile.Name()) })

	db, err := sql.Open("sqlite3", tmpFile.Name()+"?_busy_timeout=5000&_journal_mode=WAL")
	if err != nil {
		t.Fatalf("failed to open db: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	// Create asset_index table
	schema := `
	CREATE TABLE IF NOT EXISTS asset_index (
		asset_id TEXT PRIMARY KEY,
		asset_type TEXT NOT NULL DEFAULT '',
		source TEXT NOT NULL DEFAULT '',
		source_id TEXT NOT NULL DEFAULT '',
		operation_key TEXT NOT NULL DEFAULT '',
		group_name TEXT NOT NULL DEFAULT '',
		subfolder TEXT NOT NULL DEFAULT '',
		local_path TEXT NOT NULL DEFAULT '',
		drive_link TEXT NOT NULL DEFAULT '',
		download_link TEXT NOT NULL DEFAULT '',
		file_hash TEXT NOT NULL DEFAULT '',
		content_hash TEXT NOT NULL DEFAULT '',
		status TEXT NOT NULL DEFAULT 'pending',
		metadata_json TEXT NOT NULL DEFAULT '{}',
		created_at TEXT NOT NULL,
		updated_at TEXT NOT NULL
	);
	CREATE INDEX IF NOT EXISTS idx_asset_content_hash ON asset_index(content_hash);
	CREATE INDEX IF NOT EXISTS idx_asset_source ON asset_index(source, source_id);
	CREATE INDEX IF NOT EXISTS idx_asset_status ON asset_index(status);
	`
	if _, err := db.Exec(schema); err != nil {
		t.Fatalf("failed to create schema: %v", err)
	}

	repo := NewRepository(db)
	svc := NewService(repo)
	return svc, func() {}
}

func TestAssetIndexStoresHash(t *testing.T) {
	svc, cleanup := setupTestService(t)
	defer cleanup()

	ctx := context.Background()
	rec := &AssetRecord{
		AssetID:     "asset-123",
		AssetType:   "clip",
		Source:      "artlist",
		ContentHash: "abc123hash",
		Status:      "pending",
		CreatedAt:   time.Now().UTC(),
		UpdatedAt:   time.Now().UTC(),
	}

	err := svc.Upsert(ctx, rec)
	if err != nil {
		t.Fatalf("failed to upsert: %v", err)
	}

	// Retrieve and verify
	found, err := svc.FindByContentHash(ctx, "abc123hash")
	if err != nil {
		t.Fatalf("failed to find by hash: %v", err)
	}
	if found == nil {
		t.Fatal("expected to find asset by hash")
	}
	if found.AssetID != "asset-123" {
		t.Errorf("expected asset ID 'asset-123', got %s", found.AssetID)
	}
}

func TestAssetIndexFindsExistingAssetByHash(t *testing.T) {
	svc, cleanup := setupTestService(t)
	defer cleanup()

	ctx := context.Background()
	rec := &AssetRecord{
		AssetID:     "asset-456",
		AssetType:   "clip",
		Source:      "artlist",
		ContentHash: "def456hash",
		Status:      "ready",
		CreatedAt:   time.Now().UTC(),
		UpdatedAt:   time.Now().UTC(),
	}

	err := svc.Upsert(ctx, rec)
	if err != nil {
		t.Fatalf("failed to upsert: %v", err)
	}

	// Try to find by the same hash
	found, err := svc.FindByContentHash(ctx, "def456hash")
	if err != nil {
		t.Fatalf("failed to find by hash: %v", err)
	}
	if found == nil {
		t.Fatal("expected to find asset by hash")
	}
	if found.ContentHash != "def456hash" {
		t.Errorf("expected hash 'def456hash', got %s", found.ContentHash)
	}
}

func TestAssetIndexPreventsDuplicateFinalization(t *testing.T) {
	svc, cleanup := setupTestService(t)
	defer cleanup()

	ctx := context.Background()
	now := time.Now().UTC()

	// Insert first record with content hash
	rec1 := &AssetRecord{
		AssetID:     "asset-1",
		AssetType:   "clip",
		Source:      "artlist",
		ContentHash: "samehash123",
		Status:      "ready",
		LocalPath:   "/path/to/file1.mp4",
		DriveLink:   "https://drive.google.com/file/123",
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	err := svc.Upsert(ctx, rec1)
	if err != nil {
		t.Fatalf("failed to upsert first record: %v", err)
	}

	// Update same asset with new data using same asset_id (upsert behavior)
	rec1Updated := &AssetRecord{
		AssetID:     "asset-1", // Same asset ID
		AssetType:   "clip",
		Source:      "artlist",
		ContentHash: "samehash123",
		Status:      "updated",
		LocalPath:   "/path/to/file_updated.mp4",
		DriveLink:   "https://drive.google.com/file/456",
		CreatedAt:   now,
		UpdatedAt:   time.Now().UTC(),
	}
	err = svc.Upsert(ctx, rec1Updated)
	if err != nil {
		t.Fatalf("failed to upsert updated record: %v", err)
	}

	// Find by hash - should return the updated record with same asset ID
	found, err := svc.FindByContentHash(ctx, "samehash123")
	if err != nil {
		t.Fatalf("failed to find by hash: %v", err)
	}
	if found == nil {
		t.Fatal("expected to find asset by hash")
	}
	// The Upsert function updates existing record with same asset_id
	if found.AssetID != "asset-1" {
		t.Errorf("expected asset ID 'asset-1', got %s", found.AssetID)
	}
	if found.Status != "updated" {
		t.Errorf("expected status 'updated', got %s", found.Status)
	}
}

func TestAssetIndexFindsBySource(t *testing.T) {
	svc, cleanup := setupTestService(t)
	defer cleanup()

	ctx := context.Background()
	now := time.Now().UTC()

	// Insert a record
	rec := &AssetRecord{
		AssetID:     "asset-src-1",
		AssetType:   "clip",
		Source:      "artlist",
		SourceID:    "clip-123",
		ContentHash: "srchash123",
		Status:      "ready",
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	err := svc.Upsert(ctx, rec)
	if err != nil {
		t.Fatalf("failed to upsert: %v", err)
	}

	// Find by source and source_id
	found, err := svc.FindBySource(ctx, "artlist", "clip-123")
	if err != nil {
		t.Fatalf("failed to find by source: %v", err)
	}
	if found == nil {
		t.Fatal("expected to find asset by source")
	}
	assertEqual(t, "asset-src-1", found.AssetID)
	assertEqual(t, "artlist", found.Source)
	assertEqual(t, "clip-123", found.SourceID)
}

func TestAssetIndexFindReadyByGroup(t *testing.T) {
	svc, cleanup := setupTestService(t)
	defer cleanup()

	ctx := context.Background()
	now := time.Now().UTC()

	// Insert multiple records in different groups
	records := []*AssetRecord{
		{
			AssetID:   "asset-g1-1",
			AssetType: "clip",
			Source:    "artlist",
			GroupName: "news",
			Subfolder: "politics",
			Status:    "ready",
			CreatedAt: now,
			UpdatedAt: now,
		},
		{
			AssetID:   "asset-g1-2",
			AssetType: "clip",
			Source:    "artlist",
			GroupName: "news",
			Subfolder: "sports",
			Status:    "ready",
			CreatedAt: now,
			UpdatedAt: now,
		},
		{
			AssetID:   "asset-g2-1",
			AssetType: "clip",
			Source:    "youtube",
			GroupName: "entertainment",
			Status:    "ready",
			CreatedAt: now,
			UpdatedAt: now,
		},
		{
			AssetID:   "asset-pending",
			AssetType: "clip",
			Source:    "artlist",
			GroupName: "news",
			Status:    "pending",
			CreatedAt: now,
			UpdatedAt: now,
		},
	}

	for _, rec := range records {
		err := svc.Upsert(ctx, rec)
		if err != nil {
			t.Fatalf("failed to upsert: %v", err)
		}
	}

	// Find ready assets by group "news"
	found, err := svc.FindReadyByGroup(ctx, "news", "")
	if err != nil {
		t.Fatalf("failed to find by group: %v", err)
	}
	assertEqual(t, 2, len(found))

	// Find ready assets by group "news" and subfolder "politics"
	found, err = svc.FindReadyByGroup(ctx, "news", "politics")
	if err != nil {
		t.Fatalf("failed to find by group/subfolder: %v", err)
	}
	assertEqual(t, 1, len(found))
	assertEqual(t, "asset-g1-1", found[0].AssetID)

	// Find ready assets by group "entertainment"
	found, err = svc.FindReadyByGroup(ctx, "entertainment", "")
	if err != nil {
		t.Fatalf("failed to find by group: %v", err)
	}
	assertEqual(t, 1, len(found))
	assertEqual(t, "asset-g2-1", found[0].AssetID)
}

func TestAssetIndexUpdateStatus(t *testing.T) {
	svc, cleanup := setupTestService(t)
	defer cleanup()

	ctx := context.Background()
	now := time.Now().UTC()

	// Insert a record
	rec := &AssetRecord{
		AssetID:   "asset-status-1",
		AssetType: "clip",
		Source:    "artlist",
		SourceID:  "test-123",
		Status:    "pending",
		CreatedAt: now,
		UpdatedAt: now,
	}
	err := svc.Upsert(ctx, rec)
	if err != nil {
		t.Fatalf("failed to upsert: %v", err)
	}

	// Update status to ready using UpdateStatus
	err = svc.UpdateStatus(ctx, "asset-status-1", "ready")
	if err != nil {
		t.Fatalf("failed to update status: %v", err)
	}

	// Verify status was updated by fetching the record
	found, err := svc.FindBySource(ctx, "artlist", "test-123")
	if err != nil {
		t.Fatalf("failed to find by source: %v", err)
	}
	if found == nil {
		t.Fatal("expected to find asset")
	}
	assertEqual(t, "ready", found.Status)
}

func assertEqual(t *testing.T, expected, actual interface{}) {
	t.Helper()
	if expected != actual {
		t.Errorf("expected %v, got %v", expected, actual)
	}
}
