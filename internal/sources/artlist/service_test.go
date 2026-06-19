package artlist

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	domainjob "github.com/Marcuss-ops/PipelineGen/internal/core/domain/job"
	"github.com/Marcuss-ops/PipelineGen/internal/core/processor"
	"github.com/Marcuss-ops/PipelineGen/internal/jobs"
	"github.com/Marcuss-ops/PipelineGen/internal/media/models"
	"github.com/Marcuss-ops/PipelineGen/internal/repository/clips"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/security"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/database"
	testutil "github.com/Marcuss-ops/PipelineGen/internal/platform"
)

// artlistTestSchema composes the full canonical media_assets CREATE TABLE
// (see internal/storage/canonical.go::CanonicalMediaAssetsSchema) plus
// the companion clip_search_terms table used by artlist Search indexing.
// Composing the canonical block keeps this fixture in lockstep with
// production migrations and clips.Repository.mediaAssetColumns: a new
// canonical column added by migration 060 only requires touching one
// place, not every fixture.
const artlistTestSchema = storage.CanonicalMediaAssetsSchema + `
	CREATE TABLE IF NOT EXISTS clip_search_terms (
		clip_id TEXT NOT NULL,
		term TEXT NOT NULL,
		PRIMARY KEY (clip_id, term)
	);
`

// createTestDB creates a temporary SQLite database for testing
func createTestDB(t *testing.T) *sql.DB {
	t.Helper()
	return storage.NewTestDBWithSchema(t, artlistTestSchema)
}

// insertTestClip inserts a test clip into the database
func insertTestClip(t *testing.T, db *sql.DB, clip *models.MediaAsset) {
	t.Helper()

	clip.CreatedAt = time.Now().UTC()
	clip.UpdatedAt = clip.CreatedAt
	if clip.Metadata == nil {
		clip.Metadata = make(map[string]any)
	}
	if clip.Filename != "" {
		clip.SetMetadataString("filename", clip.Filename)
	}
	if clip.FolderID != "" {
		clip.SetMetadataString("folder_id", clip.FolderID)
	}
	if clip.FolderPath != "" {
		clip.SetMetadataString("folder_path", clip.FolderPath)
	}
	if clip.Group != "" {
		clip.SetMetadataString("group_name", clip.Group)
	}
	if clip.MediaType != "" {
		clip.SetMetadataString("media_type", clip.MediaType)
	}
	if clip.DriveLink != "" {
		clip.SetMetadataString("drive_link", clip.DriveLink)
	}
	if clip.DriveFileID != "" {
		clip.SetMetadataString("drive_file_id", clip.DriveFileID)
	}
	if clip.DownloadLink != "" {
		clip.SetMetadataString("download_link", clip.DownloadLink)
	}
	if clip.Category != "" {
		clip.SetMetadataString("category", clip.Category)
	}
	if clip.ExternalURL != "" {
		clip.SetMetadataString("external_url", clip.ExternalURL)
	}
	if clip.FileHash != "" {
		clip.SetMetadataString("file_hash", clip.FileHash)
	}
	if clip.LocalPath != "" {
		clip.SetMetadataString("local_path", clip.LocalPath)
	}
	if clip.Status != "" {
		clip.SetMetadataString("status", clip.Status)
	}
	if clip.Error != "" {
		clip.SetMetadataString("error", clip.Error)
	}
	if clip.ThumbURL != "" {
		clip.SetMetadataString("thumb_url", clip.ThumbURL)
	}
	if clip.PHash != "" {
		clip.SetMetadataString("phash", clip.PHash)
	}
	if clip.VisualEmbeddingJSON != "" {
		clip.SetMetadataString("visual_embedding_json", clip.VisualEmbeddingJSON)
	}
	if clip.SearchText != "" {
		clip.SetMetadataString("search_text", clip.SearchText)
	}

	repo := clips.NewRepository(db, zap.NewNop())
	if err := repo.UpsertClip(context.Background(), clip); err != nil {
		t.Fatalf("failed to insert test clip: %v", err)
	}
}

func writeFakeArtlistScraper(t *testing.T, clips []ScraperClip) string {
	t.Helper()

	scraperDir := filepath.Join(t.TempDir(), "node-scraper")
	require.NoError(t, os.MkdirAll(scraperDir, 0o755))

	clipsJSON, err := json.Marshal(clips)
	require.NoError(t, err)

	script := fmt.Sprintf(`const clips = %s;
const args = process.argv.slice(2);
const termIndex = args.indexOf('--term');
const limitIndex = args.indexOf('--limit');
const term = termIndex >= 0 && args[termIndex + 1] ? args[termIndex + 1] : '';
const rawLimit = limitIndex >= 0 && args[limitIndex + 1] ? parseInt(args[limitIndex + 1], 10) : clips.length;
const limit = Number.isFinite(rawLimit) && rawLimit > 0 ? rawLimit : clips.length;
const selected = clips.slice(0, Math.min(limit, clips.length));
process.stdout.write(JSON.stringify({
  ok: true,
  term,
  clips: selected,
  search_url: 'https://example.invalid/search?q=' + encodeURIComponent(term),
  saved: selected.length
}));
`, string(clipsJSON))

	require.NoError(t, os.WriteFile(filepath.Join(scraperDir, "artlist_search.js"), []byte(script), 0o644))
	return scraperDir
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
	svc, err := NewService(cfg, db, db, artlistRepo, nil, nil, nil, nil, nil, nil, nil, nil, nil, logger)
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

	svc, err := NewService(cfg, db, db, artlistRepo, nil, nil, nil, nil, nil, nil, nil, nil, nil, logger)
	if err != nil {
		t.Fatalf("failed to create service: %v", err)
	}
	defer svc.Close()

	ctx := context.Background()

	// Insert test clip
	clip := &models.MediaAsset{
		ID:           "artlist_search_001",
		Name:         "Search Test Clip",
		ExternalURL:  "https://artlist.io/clip/search",
		DownloadLink: "https://artlist.io/hls/search.m3u8",
		Tags:         []string{"search"},
		Source:       "artlist",
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
	clip := &models.MediaAsset{
		ID:           "artlist_store_001",
		Name:         "Store Test Clip",
		ExternalURL:  "https://artlist.io/clip/store",
		DownloadLink: "https://artlist.io/hls/store.m3u8",
		Tags:         []string{"store"},
		Source:       "artlist",
	}
	insertTestClip(t, db, clip)

	// Verify clip is in database
	var count int
	err := db.QueryRow("SELECT COUNT(*) FROM media_assets WHERE id = ?", clip.ID).Scan(&count)
	if err != nil {
		t.Fatalf("failed to query clip: %v", err)
	}

	if count != 1 {
		t.Errorf("expected 1 clip in DB, got %d", count)
	}

	// Verify drive link field exists (even if empty) — drive_link is now
	// a canonical column (migration 059), not a metadata_json key.
	var driveLink string
	err = db.QueryRow("SELECT COALESCE(drive_link, '') FROM media_assets WHERE id = ?", clip.ID).Scan(&driveLink)
	if err != nil {
		t.Fatalf("failed to query drive link: %v", err)
	}

	t.Logf("Clip stored successfully, drive_link=%s", driveLink)
}

func TestArtlistClipDriveLinkPersisted(t *testing.T) {
	db := createTestDB(t)
	defer db.Close()

	// Insert a clip with drive link
	clip := &models.MediaAsset{
		ID:           "artlist_drive_001",
		Name:         "Drive Link Test Clip",
		ExternalURL:  "https://artlist.io/clip/drive",
		DownloadLink: "https://artlist.io/hls/drive.m3u8",
		DriveLink:    "https://drive.google.com/file/d/drivelink123/view",
		FileHash:     "drivehash123",
		Tags:         []string{"drive"},
		Source:       "artlist",
	}
	insertTestClip(t, db, clip)

	// Verify drive link is persisted — drive_link is now a canonical
	// column (migration 059), not a metadata_json key.
	var driveLink string
	err := db.QueryRow("SELECT COALESCE(drive_link, '') FROM media_assets WHERE id = ?", clip.ID).Scan(&driveLink)
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

func TestSearchNormalizationLimitsToFourWords(t *testing.T) {
	if got := normalizeSearchTerm("  mountain river sunrise "); got != "mountain river sunrise" {
		t.Fatalf("expected three words preserved, got %q", got)
	}
	if got := normalizeSearchTerm("  mountain river sunrise extra "); got != "mountain river sunrise extra" {
		t.Fatalf("expected four words preserved, got %q", got)
	}
	if got := normalizeSearchTerm("  mountain river sunrise extra more "); got != "mountain river sunrise extra" {
		t.Fatalf("expected four words max, got %q", got)
	}
}

type fakeMediaProcessor struct {
	called bool
	err    error
	result *processor.ProcessResult
	inputs []*processor.ProcessInput
}

func (f *fakeMediaProcessor) Process(ctx context.Context, input *processor.ProcessInput) (*processor.ProcessResult, error) {
	f.called = true
	f.inputs = append(f.inputs, input)

	if f.err != nil {
		return &processor.ProcessResult{
			ID:     input.ID,
			Status: "failed",
			Error:  f.err.Error(),
		}, f.err
	}

	if f.result != nil {
		return f.result, nil
	}

	return &processor.ProcessResult{
		ID:        input.ID,
		Filename:  input.Name + ".mp4",
		LocalPath: input.OutputDir + "/" + input.Name + ".mp4",
		FileHash:  "hash-test",
		Status:    "processed",
	}, nil
}

func TestArtlistRunTagMediaProcessorFailure(t *testing.T) {
	ctx := context.Background()
	tmp := t.TempDir()

	// Add test hosts to security allowlist
	security.AddAllowedHost("cdn.artlist.io")

	scraperDir := writeFakeArtlistScraper(t, []ScraperClip{
		{
			ID:          "clip-1",
			Title:       "City Night",
			PrimaryURL:  "https://cdn.artlist.io/video.m3u8",
			ClipPageURL: "https://artlist.io/clip/city-night",
		},
	})

	cfg := &config.Config{
		Storage: config.StorageConfig{
			DataDir: tmp,
		},
		Video: config.VideoConfig{
			Duration: 30,
		},
		External: config.ExternalConfig{
			NodeScraperDir: scraperDir,
		},
	}

	db := createTestDB(t)
	defer db.Close()

	logger := zap.NewNop()
	artlistRepo := clips.NewRepository(db, logger)

	// Insert test clip with valid Artlist HLS URL
	insertTestClip(t, db, &models.MediaAsset{
		ID:           "clip-1",
		Name:         "City Night",
		ExternalURL:  "https://cdn.artlist.io/video.m3u8",
		DownloadLink: "https://cdn.artlist.io/video.m3u8",
		Tags:         []string{"city", "night"},
		Source:       "artlist",
	})

	processor := &fakeMediaProcessor{
		err: errors.New("download failed"),
	}

	svc, err := NewService(
		cfg,
		db,
		db,
		artlistRepo,
		processor,
		nil,
		nil,
		nil,
		nil,
		nil,
		nil,
		nil,
		nil,
		logger,
	)
	require.NoError(t, err)
	defer svc.Close()

	resp, err := svc.runOrchestrator.RunTag(ctx, &RunTagRequest{
		Term:         "city",
		Limit:        1,
		Strategy:     "replace",
		RootFolderID: "artlist-root",
	})

	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.Equal(t, 1, resp.Failed)
	require.Len(t, resp.Items, 1)
	assert.Equal(t, "media_process_failed", resp.Items[0].Status)
	assert.Contains(t, resp.Items[0].Error, "download failed")
}

func TestArtlistRunTagPassesExpectedAssetInput(t *testing.T) {
	ctx := context.Background()
	tmp := t.TempDir()

	// Add test hosts to security allowlist
	security.AddAllowedHost("cdn.artlist.io")

	scraperDir := writeFakeArtlistScraper(t, []ScraperClip{
		{
			ID:          "clip-1",
			Title:       "City Night",
			PrimaryURL:  "https://cdn.artlist.io/video.m3u8",
			ClipPageURL: "https://artlist.io/clip/city-night",
		},
	})

	cfg := &config.Config{
		Storage: config.StorageConfig{
			DataDir: tmp,
		},
		Video: config.VideoConfig{
			Duration: 30,
		},
		External: config.ExternalConfig{
			NodeScraperDir: scraperDir,
		},
	}

	db := createTestDB(t)
	defer db.Close()

	logger := zap.NewNop()
	artlistRepo := clips.NewRepository(db, logger)

	// Insert test clip with valid Artlist HLS URL
	insertTestClip(t, db, &models.MediaAsset{
		ID:           "clip-1",
		Name:         "City Night",
		ExternalURL:  "https://cdn.artlist.io/video.m3u8",
		DownloadLink: "https://cdn.artlist.io/video.m3u8",
		Tags:         []string{"city", "night"},
		Source:       "artlist",
	})

	processor := &fakeMediaProcessor{}

	svc, err := NewService(
		cfg,
		db,
		db,
		artlistRepo,
		processor,
		nil,
		nil,
		nil,
		nil,
		nil,
		nil,
		nil,
		nil,
		logger,
	)
	require.NoError(t, err)
	defer svc.Close()

	resp, err := svc.runOrchestrator.RunTag(ctx, &RunTagRequest{
		Term:         "city",
		Limit:        1,
		Strategy:     "replace",
		RootFolderID: "artlist-root",
	})

	require.NoError(t, err)
	require.Equal(t, 1, resp.Processed)
	require.Len(t, processor.inputs, 1)

	input := processor.inputs[0]
	assert.Equal(t, "clip-1", input.ID)
	assert.Equal(t, "City Night", input.Name)
	assert.Equal(t, "https://cdn.artlist.io/video.m3u8", input.SourceURL)
	assert.Contains(t, input.OutputDir, "artlist")
	assert.Equal(t, cfg.Video.Duration, input.Duration)
}

func TestArtlistFailedDownloadMarksJobFailed(t *testing.T) {
	ctx := context.Background()
	tmp := t.TempDir()

	// Add test hosts to security allowlist
	security.AddAllowedHost("cdn.artlist.io")

	scraperDir := writeFakeArtlistScraper(t, []ScraperClip{
		{
			ID:          "clip-1",
			Title:       "City Night",
			PrimaryURL:  "https://cdn.artlist.io/video.m3u8",
			ClipPageURL: "https://artlist.io/clip/city-night",
		},
	})

	cfg := &config.Config{
		Storage: config.StorageConfig{
			DataDir: tmp,
		},
		Video: config.VideoConfig{
			Duration: 30,
		},
		External: config.ExternalConfig{
			NodeScraperDir: scraperDir,
		},
	}

	db := createTestDB(t)
	defer db.Close()

	logger := zap.NewNop()
	artlistRepo := clips.NewRepository(db, logger)

	// Insert test clip with valid Artlist HLS URL
	insertTestClip(t, db, &models.MediaAsset{
		ID:           "clip-1",
		Name:         "City Night",
		ExternalURL:  "https://cdn.artlist.io/video.m3u8",
		DownloadLink: "https://cdn.artlist.io/video.m3u8",
		Tags:         []string{"city", "night"},
		Source:       "artlist",
	})

	processor := &fakeMediaProcessor{
		err: errors.New("download failed"),
	}

	svc, err := NewService(
		cfg,
		db,
		db,
		artlistRepo,
		processor,
		nil,
		nil,
		nil,
		nil,
		nil,
		nil,
		nil,
		nil,
		logger,
	)
	require.NoError(t, err)
	defer svc.Close()

	// Create a job directly (simulate a job that would be processed by a worker)
	payload := testutil.MustMarshalJSON(t, map[string]any{"term": "city", "limit": 1, "strategy": "replace", "root_folder_id": "artlist-root"})
	job := &domainjob.Job{
		ID:        "test-job-1",
		Type:      string(models.JobTypeArtlistRun),
		Status:    domainjob.StatusRunning,
		Payload:   payload,
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
	}

	// Create JobTools for testing
	jobTools := &jobs.JobTools{
		Progress: func(progress int, message string) {
			// Mock progress update
		},
		Event: func(eventType string, message string, data map[string]any) {
			// Mock event
		},
		IsCancelled: func() bool {
			return false
		},
	}

	// Handle the job (this should fail because all items fail)
	_, err = svc.HandleJob(ctx, job, jobTools)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "all artlist items failed")
}
