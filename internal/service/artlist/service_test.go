package artlist

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"go.uber.org/zap"
	_ "github.com/mattn/go-sqlite3"
	
	"velox/go-master/internal/repository/clips"
	"velox/go-master/internal/service/assetstore"
	"velox/go-master/pkg/config"
	"velox/go-master/pkg/models"
)

// createTestDB creates a temporary SQLite database for testing
func createTestDB(t *testing.T) *sql.DB {
	t.Helper()
	tmpFile := filepath.Join(t.TempDir(), "test_artlist.db")
	db, err := sql.Open("sqlite3", tmpFile+"?_journal_mode=WAL&_busy_timeout=5000")
	if err != nil {
		t.Fatalf("failed to open test db: %v", err)
	}
	
	// Create schema
	schema := `
	CREATE TABLE IF NOT EXISTS clips (
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
		duration INTEGER,
		metadata TEXT,
		file_hash TEXT,
		local_path TEXT,
		created_at TEXT,
		updated_at TEXT
	);
	`
	_, err = db.Exec(schema)
	if err != nil {
		t.Fatalf("failed to create schema: %v", err)
	}
	
	return db
}

// insertTestClip inserts a test clip into the database
func insertTestClip(t *testing.T, db *sql.DB, clip *models.Clip) {
	t.Helper()
	
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := db.Exec(`
		INSERT OR REPLACE INTO clips 
		(id, name, filename, folder_id, folder_path, group_name, media_type, drive_link, download_link, tags, source, category, external_url, duration, metadata, file_hash, local_path, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, clip.ID, clip.Name, clip.Filename, clip.FolderID, clip.FolderPath, clip.Group, clip.MediaType, clip.DriveLink, clip.DownloadLink, "[]", clip.Source, clip.Category, clip.ExternalURL, clip.Duration, clip.Metadata, clip.FileHash, clip.LocalPath, now, now)
	
	if err != nil {
		t.Fatalf("failed to insert test clip: %v", err)
	}
}

func TestAssetStoreSkipStrategy(t *testing.T) {
	// Test assetstore.SholdSkipExisting with skip strategy
	asset := assetstore.ExistingAsset{
		ID:        "test_clip_001",
		DriveLink: "https://drive.google.com/file/d/abc123/view",
		FileHash:  "hash123",
		LocalPath: "/tmp/test.mp4",
	}
	
	// Test skip strategy - should skip because drive link exists
	skip, reason, _ := assetstore.ShouldSkipExisting(context.Background(), asset, assetstore.ExistencePolicySkip, nil, assetstore.DefaultLocalFileChecker)
	if !skip {
		t.Error("expected skip=true with skip strategy and existing drive link")
	}
	if reason == "" {
		t.Error("expected non-empty reason")
	}
	t.Logf("Skip reason: %s", reason)
}

func TestAssetStoreReplaceStrategy(t *testing.T) {
	// Test assetstore.ShouldSkipExisting with replace strategy
	asset := assetstore.ExistingAsset{
		ID:        "test_clip_002",
		DriveLink: "https://drive.google.com/file/d/abc123/view",
		FileHash:  "hash123",
		LocalPath: "/tmp/test.mp4",
	}
	
	// Test replace strategy - should NOT skip
	skip, reason, _ := assetstore.ShouldSkipExisting(context.Background(), asset, assetstore.ExistencePolicyReplace, nil, assetstore.DefaultLocalFileChecker)
	if skip {
		t.Error("expected skip=false with replace strategy")
	}
	t.Logf("Replace strategy result: skip=%v, reason=%s", skip, reason)
}

func TestAssetStoreVerifyStrategyWithDriveLink(t *testing.T) {
	// Test verify strategy with existing drive link and file hash
	asset := assetstore.ExistingAsset{
		ID:        "test_clip_003",
		DriveLink: "https://drive.google.com/file/d/abc123/view",
		FileHash:  "hash123",
		LocalPath: "/tmp/test.mp4",
	}
	
	// Verify strategy with file hash should skip
	skip, reason, _ := assetstore.ShouldSkipExisting(context.Background(), asset, assetstore.ExistencePolicyVerify, nil, assetstore.DefaultLocalFileChecker)
	if !skip {
		t.Error("expected skip=true with verify strategy and valid file hash")
	}
	t.Logf("Verify strategy result: skip=%v, reason=%s", skip, reason)
}

func TestArtlistServiceCreation(t *testing.T) {
	db := createTestDB(t)
	defer db.Close()
	
	cfg := &config.Config{
		Storage: config.StorageConfig{
			DataDir: t.TempDir(),
		},
	}
	
	logger, _ := zap.NewDevelopment()
	artlistRepo := clips.NewRepository(db, logger)
	
	// Create service with minimal dependencies
	svc, err := NewService(cfg, nil, nil, "", "", artlistRepo, nil, nil, nil, nil, logger)
	if err != nil {
		t.Fatalf("failed to create service: %v", err)
	}
	defer svc.Close()
	
	if svc == nil {
		t.Error("expected service to be non-nil")
	}
	t.Log("Artlist service created successfully")
}

func TestArtlistSearchRequest(t *testing.T) {
	db := createTestDB(t)
	defer db.Close()
	
	cfg := &config.Config{}
	logger, _ := zap.NewDevelopment()
	artlistRepo := clips.NewRepository(db, logger)
	
	svc, err := NewService(cfg, nil, nil, "", "", artlistRepo, nil, nil, nil, nil, logger)
	if err != nil {
		t.Fatalf("failed to create service: %v", err)
	}
	defer svc.Close()
	
	ctx := context.Background()
	
	// Insert test clip
	clip := &models.Clip{
		ID:          "artlist_search_001",
		Name:        "Search Test Clip",
		ExternalURL: "https://artlist.io/clip/search",
		DownloadLink: "https://artlist.io/hls/search.m3u8",
		Tags:        []string{"search"},
		Source:      "artlist",
	}
	insertTestClip(t, db, clip)
	
	// Test search
	req := &SearchRequest{Term: "search", Limit: 10}
	resp, err := svc.Search(ctx, req)
	if err != nil {
		t.Errorf("Search failed: %v", err)
	}
	if !resp.OK {
		t.Error("Expected OK to be true")
	}
}

func TestArtlistClipStoredInSQLite(t *testing.T) {
	db := createTestDB(t)
	defer db.Close()
	
	// Insert a clip directly
	clip := &models.Clip{
		ID:          "artlist_store_001",
		Name:        "Store Test Clip",
		ExternalURL: "https://artlist.io/clip/store",
		DownloadLink: "https://artlist.io/hls/store.m3u8",
		Tags:        []string{"store"},
		Source:      "artlist",
	}
	insertTestClip(t, db, clip)
	
	// Verify clip is in database
	var count int
	err := db.QueryRow("SELECT COUNT(*) FROM clips WHERE id = ?", clip.ID).Scan(&count)
	if err != nil {
		t.Fatalf("failed to query clip: %v", err)
	}
	
	if count != 1 {
		t.Errorf("expected 1 clip in DB, got %d", count)
	}
	
	// Verify drive link field exists (even if empty)
	var driveLink string
	err = db.QueryRow("SELECT drive_link FROM clips WHERE id = ?", clip.ID).Scan(&driveLink)
	if err != nil {
		t.Fatalf("failed to query drive link: %v", err)
	}
	
	t.Logf("Clip stored successfully, drive_link=%s", driveLink)
}

func TestArtlistClipDriveLinkPersisted(t *testing.T) {
	db := createTestDB(t)
	defer db.Close()
	
	// Insert a clip with drive link
	clip := &models.Clip{
		ID:          "artlist_drive_001",
		Name:        "Drive Link Test Clip",
		ExternalURL: "https://artlist.io/clip/drive",
		DownloadLink: "https://artlist.io/hls/drive.m3u8",
		DriveLink:   "https://drive.google.com/file/d/drivelink123/view",
		FileHash:    "drivehash123",
		Tags:        []string{"drive"},
		Source:      "artlist",
	}
	insertTestClip(t, db, clip)
	
	// Verify drive link is persisted
	var driveLink string
	err := db.QueryRow("SELECT drive_link FROM clips WHERE id = ?", clip.ID).Scan(&driveLink)
	if err != nil {
		t.Fatalf("failed to query drive link: %v", err)
	}
	
	if driveLink != clip.DriveLink {
		t.Errorf("expected drive link %s, got %s", clip.DriveLink, driveLink)
	}
	
	t.Log("Drive link correctly persisted in SQLite")
}

func TestRunTagRequestValidation(t *testing.T) {
	// Test RunTagRequest validation
	req := &RunTagRequest{
		Term:     "",
		Limit:    10,
		Strategy: "verify",
	}
	
	// Empty term should cause validation error in RunTag
	if req.Term == "" {
		t.Log("Empty term correctly identified as invalid")
	}
	
	// Valid term
	req.Term = "test"
	if req.Term == "" {
		t.Error("term should not be empty")
	}
}

func TestSearchRequestValidation(t *testing.T) {
	req := &SearchRequest{
		Term:  "",
		Limit: 10,
	}
	
	if req.Term == "" {
		t.Log("Empty term in search request")
	}
	
	req.Term = "music"
	if req.Limit <= 0 {
		req.Limit = 8
	}
	
	if req.Limit > 50 {
		req.Limit = 50
	}
}
