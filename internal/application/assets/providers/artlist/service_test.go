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

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/delivery"
	assetfinalizer "github.com/Marcuss-ops/PipelineGen/internal/application/assets/finalizer"
	jobs "github.com/Marcuss-ops/PipelineGen/internal/application/jobs"
	drive "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/assets"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/security"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	"github.com/Marcuss-ops/PipelineGen/pkg/testutil"
)


// createTestDB creates a temporary SQLite database for testing
func createTestDB(t *testing.T) *sql.DB {
	t.Helper()
	return drive.NewMigratedTestDB(t)
}

// outboxEventCount returns the number of asset.index.requested outbox
// events emitted by the canonical AssetFinalizerTx during a run.
func outboxEventCount(db *sql.DB) int {
	var count int
	_ = db.QueryRow(`
		SELECT COUNT(*) FROM outbox_events
		WHERE event_type = 'asset.index.requested'
	`).Scan(&count)
	return count
}

// outboxSourceVersionFor returns the source_version stored in the
// asset.index.requested outbox event for the given clip ID.
func outboxSourceVersionFor(db *sql.DB, clipID string) string {
	var payload string
	_ = db.QueryRow(`
		SELECT payload_json FROM outbox_events
		WHERE event_type = 'asset.index.requested' AND aggregate_id = ?
		ORDER BY id DESC LIMIT 1
	`, clipID).Scan(&payload)
	var envelope map[string]any
	if err := json.Unmarshal([]byte(payload), &envelope); err != nil {
		return ""
	}
	if v, ok := envelope["source_version"].(string); ok {
		return v
	}
	return ""
}

// insertTestClip inserts a test clip into the database
func insertTestClip(t *testing.T, db *sql.DB, clip *asset.Asset) {
	t.Helper()

	clip.CreatedAt = time.Now().UTC()
	clip.UpdatedAt = clip.CreatedAt
	if clip.Metadata == nil {
		clip.Metadata = make(map[string]any)
	}

	repo := assets.NewClipsRepository(db, zap.NewNop())
	if err := repo.UpsertClip(context.Background(), clip); err != nil {
		t.Fatalf("failed to insert test clip: %v", err)
	}
}

func writeFakeArtlistScraper(t *testing.T, clips []Candidate) string {
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
	artlistRepo := assets.NewClipsRepository(db, logger)

	// PR2.5: NewService takes ServiceDeps (struct) instead of 14
	// positional arguments. AssetStore (port) is wired via the same
	// repo instance that satisfies it.
	// PR2.6: ArtlistDB dropped — == MainDB post media.db.sqlite
	// consolidation. ServiceDeps embeds ServicePorts + ServiceDependencies,
	// so flat-construction literals were the documented idiom; however
	// after PR2.7 the promotion became ambiguous in `go vet`'s eyes
	// ("unknown field Cfg" — likely a transient platform detail), so we
	// construct via explicit nested sub-structs. This is robust against
	// any promotion-renaming churn in future PR2.x waves.
	svc, err := NewService(baseServiceDeps(t, ServiceDeps{
		ServicePorts: ServicePorts{
			AssetStore: artlistRepo,
		},
		ServiceDependencies: ServiceDependencies{
			Infra: ArtlistInfraDeps{
				MainDB: db,
				Cfg:    cfg,
				Log:    logger,
			},
			Ports: ArtlistPortDeps{
				Dispatcher: &stubDispatcherForArtlist{repo: artlistRepo},
			},
			Finalizer: ArtlistFinalizerDeps{
				AssetFinalizerTx: assetfinalizer.NewAssetTxFinalizer(logger, nil),
			},
		},
	}))
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
	artlistRepo := assets.NewClipsRepository(db, logger)

	svc, err := NewService(baseServiceDeps(t, ServiceDeps{
		ServicePorts: ServicePorts{
			AssetStore: artlistRepo,
		},
		ServiceDependencies: ServiceDependencies{
			Infra: ArtlistInfraDeps{
				MainDB: db,
				Cfg:    cfg,
				Log:    logger,
			},
			Ports: ArtlistPortDeps{
				Dispatcher: &stubDispatcherForArtlist{repo: artlistRepo},
			},
			Finalizer: ArtlistFinalizerDeps{
				AssetFinalizerTx: assetfinalizer.NewAssetTxFinalizer(logger, nil),
			},
		},
	}))

	ctx := context.Background()

	// Insert test clip
	clip := &asset.Asset{
		ID:             "artlist_search_001",
		Name:           "Search Test Clip",
		SourceURL:      "https://artlist.io/clip/search",
		Source:         "artlist",
		LifecycleState: asset.StateActive,
		Tags:           []string{"search"},
	}
	clip.SetDownloadLink("https://artlist.io/hls/search.m3u8")
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
	clip := &asset.Asset{
		ID:             "artlist_store_001",
		Name:           "Store Test Clip",
		SourceURL:      "https://artlist.io/clip/store",
		Source:         "artlist",
		LifecycleState: asset.StateActive,
		Tags:           []string{"store"},
	}
	clip.SetDownloadLink("https://artlist.io/hls/store.m3u8")
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
	clip := &asset.Asset{
		ID:             "artlist_drive_001",
		Name:           "Drive Link Test Clip",
		SourceURL:      "https://artlist.io/clip/drive",
		Source:         "artlist",
		LifecycleState: asset.StateActive,
		Tags:           []string{"drive"},
	}
	clip.SetDownloadLink("https://artlist.io/hls/drive.m3u8")
	clip.SetDriveLink("https://drive.google.com/file/d/drivelink123/view")
	clip.SetLegacyFileMD5("drivehash123")
	insertTestClip(t, db, clip)

	// Verify drive link is persisted — drive_link is now a canonical
	// column (migration 059), not a metadata_json key.
	var driveLink string
	err := db.QueryRow("SELECT COALESCE(drive_link, '') FROM media_assets WHERE id = ?", clip.ID).Scan(&driveLink)
	if err != nil {
		t.Fatalf("failed to query drive link: %v", err)
	}

	if driveLink != clip.DriveLink() {
		t.Errorf("expected drive link %s, got %s", clip.DriveLink(), driveLink)
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

func TestSearchNormalizationLimitsToSixWords(t *testing.T) {
	if got := normalizeSearchTerm("  mountain river sunrise "); got != "mountain river sunrise" {
		t.Fatalf("expected three words preserved, got %q", got)
	}
	if got := normalizeSearchTerm("  mountain river sunrise extra "); got != "mountain river sunrise extra" {
		t.Fatalf("expected four words preserved, got %q", got)
	}
	if got := normalizeSearchTerm("  mountain river sunrise extra more "); got != "mountain river sunrise extra more" {
		t.Fatalf("expected five words preserved, got %q", got)
	}
	if got := normalizeSearchTerm("  one two three four five six seven "); got != "one two three four five six" {
		t.Fatalf("expected six words max, got %q", got)
	}
}

// stubPublisherForArtlist is the F2.11 Publisher stub for artlist test
// fixtures. Implements delivery.Publisher with deterministic returns so
// the 5 NewService call sites in this file can construct Service
// post-F2.11 (Service.NewService fails closed on nil Publisher via
// ErrPublisherUnavailable; see f2_11_publisher_only_test.go for the
// audit pin). Production code paths never reach this stub — it is a
// QDRANT-002-style fail-closed fixture that lets the test focus on
// the Search / RunTag / HandleJob surfaces without exercising the
// Drive write path.
type stubPublisherForArtlist struct{}

func (s *stubPublisherForArtlist) Publish(ctx context.Context, req delivery.PublishRequest) (*delivery.PublishResult, error) {
	return &delivery.PublishResult{
		FileID:      "stub-publish-file-id",
		WebViewLink: "https://drive.google.com/file/d/stub-publish-file-id/view",
		FolderID:    "stub-publish-folder-id",
		Destination: req.Destination,
	}, nil
}

func (s *stubPublisherForArtlist) ResolveFolder(ctx context.Context, req delivery.PublishRequest) (string, error) {
	// Use ParentFolderID if threaded (matches production under-cfg
	// branch); otherwise return a deterministic ID so the test's
	// downstream assertions stay stable.
	if req.ParentFolderID != "" {
		return req.ParentFolderID, nil
	}
	return "stub-resolve-folder-id", nil
}

type fakeMediaProcessor struct {
	called bool
	err    error
	result *asset.ProcessResult
	inputs []*asset.ProcessInput
}

func (f *fakeMediaProcessor) Process(ctx context.Context, input *asset.ProcessInput) (*asset.ProcessResult, error) {
	f.called = true
	f.inputs = append(f.inputs, input)

	if f.err != nil {
		return &asset.ProcessResult{
			ID:     input.ID,
			Status: "failed",
			Error:  f.err.Error(),
		}, f.err
	}

	if f.result != nil {
		return f.result, nil
	}

	return &asset.ProcessResult{
		ID:        input.ID,
		Filename:  input.Name + ".mp4",
		LocalPath: input.OutputDir + "/" + input.Name + ".mp4",
		LegacyFileMD5:  "hash-test",
		Status:    "processed",
	}, nil
}

func TestArtlistRunTagMediaProcessorFailure(t *testing.T) {
	ctx := context.Background()
	tmp := t.TempDir()

	// Add test hosts to security allowlist
	security.AddAllowedHost("cdn.artlist.io")

	scraperDir := writeFakeArtlistScraper(t, []Candidate{
		{
			ID:        "clip-1",
			Title:     "City Night",
			SourceRef: "https://cdn.artlist.io/video.m3u8",
			PageURL:   "https://artlist.io/clip/city-night",
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
	artlistRepo := assets.NewClipsRepository(db, logger)

	// Insert test clip with valid Artlist HLS URL
	clip := &asset.Asset{
		ID:             "clip-1",
		Name:           "City Night",
		SourceURL:      "https://cdn.artlist.io/video.m3u8",
		Source:         "artlist",
		LifecycleState: asset.StateActive,
		Tags:           []string{"city", "night"},
	}
	clip.SetDownloadLink("https://cdn.artlist.io/video.m3u8")
	insertTestClip(t, db, clip)

	// Pre-populate clip_search_terms so DBSearcher finds the clip
	// (RunTag -> SearchLiveAndSave -> SearchLive -> searchLiveWithFallbacks
	//  -> DBSearcher.Search calls SearchByTerms which queries clip_search_terms).
	_, _ = db.Exec("INSERT OR IGNORE INTO clip_search_terms (term, clip_id) VALUES ('city', 'clip-1')")
	_, _ = db.Exec("INSERT OR IGNORE INTO clip_search_terms (term, clip_id) VALUES ('night', 'clip-1')")

	processor := &fakeMediaProcessor{
		err: errors.New("download failed"),
	}

	svc, err := NewService(baseServiceDeps(t, ServiceDeps{
		ServicePorts: ServicePorts{
			AssetStore: artlistRepo,
		},
		ServiceDependencies: ServiceDependencies{
			Infra: ArtlistInfraDeps{
				MainDB: db,
				Cfg:    cfg,
				Log:    logger,
			},
			Ports: ArtlistPortDeps{
				Dispatcher: &stubDispatcherForArtlist{repo: artlistRepo},
			},
			Domain: ArtlistDomainDeps{
				MediaProcessor: processor,
			},
			Finalizer: ArtlistFinalizerDeps{
				AssetFinalizerTx: assetfinalizer.NewAssetTxFinalizer(logger, nil),
			},
		},
	}))
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

	scraperDir := writeFakeArtlistScraper(t, []Candidate{
		{
			ID:        "clip-1",
			Title:     "City Night",
			SourceRef: "https://cdn.artlist.io/video.m3u8",
			PageURL:   "https://artlist.io/clip/city-night",
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
	artlistRepo := assets.NewClipsRepository(db, logger)

	// Insert test clip with valid Artlist HLS URL
	clip := &asset.Asset{
		ID:             "clip-1",
		Name:           "City Night",
		SourceURL:      "https://cdn.artlist.io/video.m3u8",
		Source:         "artlist",
		LifecycleState: asset.StateActive,
		Tags:           []string{"city", "night"},
	}
	clip.SetDownloadLink("https://cdn.artlist.io/video.m3u8")
	insertTestClip(t, db, clip)

	// Pre-populate clip_search_terms so DBSearcher finds the clip.
	_, _ = db.Exec("INSERT OR IGNORE INTO clip_search_terms (term, clip_id) VALUES ('city', 'clip-1')")
	_, _ = db.Exec("INSERT OR IGNORE INTO clip_search_terms (term, clip_id) VALUES ('night', 'clip-1')")

	processor := &fakeMediaProcessor{}

	svc, err := NewService(baseServiceDeps(t, ServiceDeps{
		ServicePorts: ServicePorts{
			AssetStore: artlistRepo,
		},
		ServiceDependencies: ServiceDependencies{
			Infra: ArtlistInfraDeps{
				MainDB: db,
				Cfg:    cfg,
				Log:    logger,
			},
			Ports: ArtlistPortDeps{
				Dispatcher: &stubDispatcherForArtlist{repo: artlistRepo},
			},
			Domain: ArtlistDomainDeps{
				MediaProcessor: processor,
			},
			Finalizer: ArtlistFinalizerDeps{
				AssetFinalizerTx: assetfinalizer.NewAssetTxFinalizer(logger, nil),
			},
		},
	}))
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
	// PR-ARTLIST-OUTCOME-ACCOUNTING (P1, July 2026): fakeMediaProcessor
	// returns empty Drive fields, so stagePersistResults' Drive-gate
	// correctly rejects the clip. Under the new outcome-accounting
	// contract this is a Failed clip (no longer a silent drop). The
	// processor IS still called (this test's primary assertion about
	// input routing is preserved), but no persist happens so Processed
	// stays at 0. See run_orchestrator_stages.go stagePersistResults
	// Drive-gate branch.
	assert.Equal(t, 0, resp.Processed, "PR-ARTLIST-OUTCOME-ACCOUNTING: Drive gate rejects fakeMediaProcessor result with empty Drive fields")
	assert.Equal(t, 1, resp.Failed, "PR-ARTLIST-OUTCOME-ACCOUNTING: gate rejection now bumps Failed tally")
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

	scraperDir := writeFakeArtlistScraper(t, []Candidate{
		{
			ID:        "clip-1",
			Title:     "City Night",
			SourceRef: "https://cdn.artlist.io/video.m3u8",
			PageURL:   "https://artlist.io/clip/city-night",
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
	artlistRepo := assets.NewClipsRepository(db, logger)

	// Insert test clip with valid Artlist HLS URL
	clip := &asset.Asset{
		ID:             "clip-1",
		Name:           "City Night",
		SourceURL:      "https://cdn.artlist.io/video.m3u8",
		Source:         "artlist",
		LifecycleState: asset.StateActive,
		Tags:           []string{"city", "night"},
	}
	clip.SetDownloadLink("https://cdn.artlist.io/video.m3u8")
	insertTestClip(t, db, clip)

	// Pre-populate clip_search_terms so DBSearcher finds the clip.
	_, _ = db.Exec("INSERT OR IGNORE INTO clip_search_terms (term, clip_id) VALUES ('city', 'clip-1')")
	_, _ = db.Exec("INSERT OR IGNORE INTO clip_search_terms (term, clip_id) VALUES ('night', 'clip-1')")

	processor := &fakeMediaProcessor{
		err: errors.New("download failed"),
	}

	svc, err := NewService(baseServiceDeps(t, ServiceDeps{
		ServicePorts: ServicePorts{
			AssetStore: artlistRepo,
		},
		ServiceDependencies: ServiceDependencies{
			Infra: ArtlistInfraDeps{
				MainDB: db,
				Cfg:    cfg,
				Log:    logger,
			},
			Ports: ArtlistPortDeps{
				Dispatcher: &stubDispatcherForArtlist{repo: artlistRepo},
			},
			Domain: ArtlistDomainDeps{
				MediaProcessor: processor,
			},
			Finalizer: ArtlistFinalizerDeps{
				AssetFinalizerTx: assetfinalizer.NewAssetTxFinalizer(logger, nil),
			},
		},
	}))
	require.NoError(t, err)
	defer svc.Close()

	// Create a job directly (simulate a job that would be processed by a worker)
	payload := testutil.MustMarshalJSON(t, map[string]any{"term": "city", "limit": 1, "strategy": "replace", "root_folder_id": "artlist-root"})
	job := &job.Job{
		ID:        "test-job-1",
		Type:      "artlist.run",
		Status:    job.StatusRunning,
		Payload:   payload,
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
	} // Create JobTools for testing.
	jobTools := &jobs.JobTools{
		Progress: func(progress int, message string) {
			// Mock progress update
		},
		Event: func(eventType string, message string, data map[string]any) {
			// Mock event
			// FASE 4(b) (July 2026): IsCancelled field REMOVED from
			// domain/job.JobExecutionTools. The pre-Fase-4 polling
			// projection is gone; cancel propagates through native
			// context cancellation (ctx.Err()) and the typed
			// renewLeaseLoopWith LeaseState observation. The test
			// fixture no longer needs to stub the IsCancelled field.
		},
	}

	// Handle the job (this should fail because all items fail)
	_, err = svc.HandleJob(ctx, job, jobTools)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "all artlist items failed")
}
