// Package artlist — Gate 02 + Gate 09 Drive Contract Tests (ARTLIST-DOD-2026-07-07).
//
// PR-ARTLIST-DOD-GATE-02-DRIVE-UPLOAD: verify every processed clip has
// non-empty DriveFileID + DriveLink + DownloadLink + FileHash after
// a successful run.
//
// PR-ARTLIST-DOD-GATE-09-DRIVE-FAILURE: verify the Drive-failure
// fail-closed contract — when the processor returns Status="processed"
// but empty Drive fields, stagePersistResults skips the clip, does NOT
// increment Processed, and does NOT dispatch.
//
// godlike/07 no-fake-availability: the Drive gate in stagePersistResults
// exits early with a Warn log when item.DriveFileID or item.DriveLink
// is empty. This prevents the fake-success anti-pattern where a clip
// appears "processed" but was never uploaded.
package artlist

import (
	"context"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/assets"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/security"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
)

// driveFailureProcessor is a Gate 09 test double that returns
// Status="processed" but leaves DriveLink and DriveFileID empty.
// This simulates a processor/transcoder that succeeded but whose
// internal Drive upload step failed silently. The Gate 02/09
// test verifies that stagePersistResults correctly rejects these
// items (no Processed increment, no dispatch).
type driveFailureProcessor struct {
	mu     sync.Mutex
	inputs []*asset.ProcessInput
}

func (f *driveFailureProcessor) Process(_ context.Context, input *asset.ProcessInput) (*asset.ProcessResult, error) {
	f.mu.Lock()
	f.inputs = append(f.inputs, input)
	f.mu.Unlock()

	return &asset.ProcessResult{
		ID:        input.ID,
		Filename:  input.ID + "_processed.mp4",
		LocalPath: input.OutputDir + "/" + input.ID + "_processed.mp4",
		FileHash:  "drivefail-hash-" + input.ID,
		// Drive fields intentionally left empty — simulates Drive upload failure
		DriveLink:     "",
		DriveFileID:   "",
		DownloadLink:  input.SourceURL,
		PublishAction: "",
		Status:        "processed",
		Error:         "",
	}, nil
}

// partialDriveProcessor is a Gate 02/09 test double that returns
// one clip WITH Drive fields (success) and one clip WITHOUT Drive
// fields (failure). This tests mixed-result batches where the gate
// must discriminate per item.
type partialDriveProcessor struct {
	mu     sync.Mutex
	inputs []*asset.ProcessInput
}

func (f *partialDriveProcessor) Process(_ context.Context, input *asset.ProcessInput) (*asset.ProcessResult, error) {
	f.mu.Lock()
	f.inputs = append(f.inputs, input)
	f.mu.Unlock()

	result := &asset.ProcessResult{
		ID:           input.ID,
		Filename:     input.ID + "_processed.mp4",
		LocalPath:    input.OutputDir + "/" + input.ID + "_processed.mp4",
		FileHash:     "partial-hash-" + input.ID,
		DownloadLink: input.SourceURL,
		Status:       "processed",
	}

	// Deterministic: only clips with "-ok" suffix get Drive fields.
	// This is immune to goroutine processing order (the count-based
	// approach was non-deterministic under parallel stageProcessBatch).
	if input.ID == "gate09-mixed-ok" {
		result.DriveLink = "https://drive.google.com/file/d/" + input.ID + "-drive/view"
		result.DriveFileID = input.ID + "-drive-id"
		result.PublishAction = "created"
	}

	return result, nil
}

// Compile-time assertions: both processors satisfy the Processor port.
var _ asset.Processor = (*driveFailureProcessor)(nil)
var _ asset.Processor = (*partialDriveProcessor)(nil)

// TestGate02_DriveFieldsPopulated verifies that every clip processed
// through the happy-path pipeline has non-empty DriveFileID,
// DriveLink, DownloadLink, and FileHash. This is the canonical
// assertion for the "Drive upload succeeded → fields are present"
// contract (Gate 02 of ARTLIST-DOD-2026-07-07).
func TestGate02_DriveFieldsPopulated(t *testing.T) {
	ctx := context.Background()
	tmp := t.TempDir()

	security.AddAllowedHost("cdn.artlist.io")

	scraperDir := writeFakeArtlistScraper(t, []Candidate{
		{
			ID:        "gate02-clip-1",
			Title:     "Drive Test Clip A",
			SourceRef: "https://cdn.artlist.io/video/gate02-a.m3u8",
			PageURL:   "https://artlist.io/clip/drive-test-a",
		},
		{
			ID:        "gate02-clip-2",
			Title:     "Drive Test Clip B",
			SourceRef: "https://cdn.artlist.io/video/gate02-b.m3u8",
			PageURL:   "https://artlist.io/clip/drive-test-b",
		},
	})

	cfg := &config.Config{
		Storage: config.StorageConfig{DataDir: tmp},
		Video:   config.VideoConfig{Duration: 15},
		External: config.ExternalConfig{
			NodeScraperDir: scraperDir,
		},
	}

	db := createTestDB(t)
	defer db.Close()

	logger := zap.NewNop()
	artlistRepo := assets.NewClipsRepository(db, logger)

	// Pre-populate clip_search_terms and insert STAGING clips
	for _, clip := range []struct {
		id, name, sourceURL, term1, term2 string
	}{
		{"gate02-clip-1", "Drive Test Clip A", "https://cdn.artlist.io/video/gate02-a.m3u8", "drive", "test"},
		{"gate02-clip-2", "Drive Test Clip B", "https://cdn.artlist.io/video/gate02-b.m3u8", "drive", "test"},
	} {
		_, _ = db.Exec("INSERT OR IGNORE INTO clip_search_terms (term, clip_id) VALUES (?, ?)", clip.term1, clip.id)
		_, _ = db.Exec("INSERT OR IGNORE INTO clip_search_terms (term, clip_id) VALUES (?, ?)", clip.term2, clip.id)

		a := &asset.Asset{
			ID:             clip.id,
			Name:           clip.name,
			SourceURL:      clip.sourceURL,
			Source:         "artlist",
			LifecycleState: asset.StateStaging,
			MediaType:      "video",
		}
		a.SetDownloadLink(clip.sourceURL)
		a.SetMetadataString("index_state", string(asset.StateDiscovered))
		insertTestClip(t, db, a)
	}

	processor := &successMediaProcessor{}
	recDisp := &recordingDispatcherForArtlist{stubDispatcherForArtlist: stubDispatcherForArtlist{repo: artlistRepo}}

	svc, err := NewService(ServiceDeps{
		ServicePorts: ServicePorts{
			AssetStore:    artlistRepo,
			Publisher:     &stubPublisherForArtlist{},
			RunRepository: &stubRunRepoForArtlist{},
		},
		ServiceDependencies: ServiceDependencies{
			Cfg:            cfg,
			MainDB:         db,
			Log:            logger,
			Dispatcher:     recDisp,
			MediaProcessor: processor,
		},
	})
	require.NoError(t, err)
	defer svc.Close()

	resp, err := svc.runOrchestrator.RunTag(ctx, &RunTagRequest{
		Term:         "drive test",
		Limit:        2,
		Strategy:     "replace",
		RootFolderID: "gate02-root-folder",
	})
	require.NoError(t, err)
	require.NotNil(t, resp)

	// Gate 2 assertion: every clip has all 4 Drive fields non-empty
	require.Len(t, resp.Items, 2)
	for i, item := range resp.Items {
		t.Logf("item[%d]: clip_id=%s drive_file_id=%s drive_link=%s download_link=%s file_hash=%s",
			i, item.ClipID, item.DriveFileID, item.DriveLink, item.DownloadLink, item.FileHash)

		assert.NotEmpty(t, item.DriveFileID, "item[%d] %s: DriveFileID must be non-empty", i, item.ClipID)
		assert.NotEmpty(t, item.DriveLink, "item[%d] %s: DriveLink must be non-empty", i, item.ClipID)
		assert.NotEmpty(t, item.DownloadLink, "item[%d] %s: DownloadLink must be non-empty", i, item.ClipID)
		assert.NotEmpty(t, item.FileHash, "item[%d] %s: FileHash must be non-empty", i, item.ClipID)

		// DriveLink must look like a valid Drive URL
		assert.Contains(t, item.DriveLink, "drive.google.com", "item[%d] %s: DriveLink must be a Drive URL", i, item.ClipID)

		// DriveFileID must match the processor's pattern
		assert.Contains(t, item.DriveFileID, "-drive-id", "item[%d] %s: DriveFileID must match pattern", i, item.ClipID)
	}

	assert.Equal(t, 2, resp.Processed, "both clips should be processed")
	assert.Equal(t, 0, resp.Failed, "no clips should fail")
	assert.Equal(t, 2, recDisp.DispatchCount(), "both clips should be dispatched")

	// Gate 3: SQLite Drive columns verified
	for _, clipID := range []string{"gate02-clip-1", "gate02-clip-2"} {
		var driveLink, fileHash, driveFileID string
		err := db.QueryRow(`
			SELECT COALESCE(drive_link, ''),
			       COALESCE(file_hash, ''),
			       COALESCE(drive_file_id, '')
			FROM media_assets WHERE id = ?
		`, clipID).Scan(&driveLink, &fileHash, &driveFileID)
		require.NoError(t, err, "clip %s should exist in media_assets", clipID)
		assert.NotEmpty(t, driveLink, "SQLite: clip %s drive_link must be non-empty", clipID)
		assert.NotEmpty(t, fileHash, "SQLite: clip %s file_hash must be non-empty", clipID)
		assert.NotEmpty(t, driveFileID, "SQLite: clip %s drive_file_id must be non-empty", clipID)
	}
}

// TestGate09_DriveFailureFailClosed verifies the Drive-failure
// fail-closed contract (Gate 09 of ARTLIST-DOD-2026-07-07):
// when the processor returns Status="processed" but with empty
// DriveFileID and DriveLink, stagePersistResults MUST skip the
// item — no Processed increment, no dispatcher call, and the
// SQLite row stays untouched (lifecycle_state remains STAGING,
// not ACTIVE).
func TestGate09_DriveFailureFailClosed(t *testing.T) {
	ctx := context.Background()
	tmp := t.TempDir()

	security.AddAllowedHost("cdn.artlist.io")

	scraperDir := writeFakeArtlistScraper(t, []Candidate{
		{
			ID:        "gate09-clip-1",
			Title:     "Drive Failure Clip",
			SourceRef: "https://cdn.artlist.io/video/gate09.m3u8",
			PageURL:   "https://artlist.io/clip/drive-failure",
		},
	})

	cfg := &config.Config{
		Storage: config.StorageConfig{DataDir: tmp},
		Video:   config.VideoConfig{Duration: 15},
		External: config.ExternalConfig{
			NodeScraperDir: scraperDir,
		},
	}

	db := createTestDB(t)
	defer db.Close()

	logger := zap.NewNop()
	artlistRepo := assets.NewClipsRepository(db, logger)

	// Pre-populate clip_search_terms
	_, _ = db.Exec("INSERT OR IGNORE INTO clip_search_terms (term, clip_id) VALUES ('drive', 'gate09-clip-1')")
	_, _ = db.Exec("INSERT OR IGNORE INTO clip_search_terms (term, clip_id) VALUES ('failure', 'gate09-clip-1')")

	a := &asset.Asset{
		ID:             "gate09-clip-1",
		Name:           "Drive Failure Clip",
		SourceURL:      "https://cdn.artlist.io/video/gate09.m3u8",
		Source:         "artlist",
		LifecycleState: asset.StateStaging,
		MediaType:      "video",
	}
	a.SetDownloadLink("https://cdn.artlist.io/video/gate09.m3u8")
	a.SetMetadataString("index_state", string(asset.StateDiscovered))
	insertTestClip(t, db, a)

	processor := &driveFailureProcessor{}
	recDisp := &recordingDispatcherForArtlist{stubDispatcherForArtlist: stubDispatcherForArtlist{repo: artlistRepo}}

	svc, err := NewService(ServiceDeps{
		ServicePorts: ServicePorts{
			AssetStore:    artlistRepo,
			Publisher:     &stubPublisherForArtlist{},
			RunRepository: &stubRunRepoForArtlist{},
		},
		ServiceDependencies: ServiceDependencies{
			Cfg:            cfg,
			MainDB:         db,
			Log:            logger,
			Dispatcher:     recDisp,
			MediaProcessor: processor,
		},
	})
	require.NoError(t, err)
	defer svc.Close()

	resp, err := svc.runOrchestrator.RunTag(ctx, &RunTagRequest{
		Term:         "drive failure",
		Limit:        1,
		Strategy:     "replace",
		RootFolderID: "gate09-root-folder",
	})
	require.NoError(t, err)
	require.NotNil(t, resp)

	// ── Gate 9 assertions ──

	// The item appears in Items (processor returned it) but Processed
	// is 0 because stagePersistResults skipped it.
	require.Len(t, resp.Items, 1)
	assert.Equal(t, "processed", resp.Items[0].Status, "processor status should be 'processed'")
	assert.Empty(t, resp.Items[0].DriveFileID, "DriveFileID should be empty (Drive upload failed)")
	assert.Empty(t, resp.Items[0].DriveLink, "DriveLink should be empty (Drive upload failed)")
	assert.NotEmpty(t, resp.Items[0].FileHash, "FileHash should be present (transcode succeeded)")

	// Processed MUST be 0 — the gate in stagePersistResults skipped it
	assert.Equal(t, 0, resp.Processed, "Processed must be 0 when Drive fields are missing")
	assert.Equal(t, 0, resp.Failed, "Failed must be 0 (processor returned success)")

	// Dispatcher MUST NOT have been called
	assert.Equal(t, 0, recDisp.DispatchCount(), "dispatcher must NOT be called when Drive fields are missing")

	// SQLite: lifecycle_state should still be STAGING (not ACTIVE)
	var lifecycleState string
	err = db.QueryRow("SELECT lifecycle_state FROM media_assets WHERE id = ?", "gate09-clip-1").Scan(&lifecycleState)
	require.NoError(t, err)
	assert.Equal(t, string(asset.StateStaging), lifecycleState,
		"lifecycle_state must remain STAGING when Drive upload failed — no persist happened")
}

// TestGate09_ArtlistFullRun_PartialDriveFailure verifies mixed-result
// batches: one clip with Drive (success) and one without (failure).
// The gate must discriminate per-item: Processed=1, dispatch count=1,
// SQLite has one clip ACTIVE and one still STAGING.
func TestGate09_ArtlistFullRun_PartialDriveFailure(t *testing.T) {
	ctx := context.Background()
	tmp := t.TempDir()

	security.AddAllowedHost("cdn.artlist.io")

	scraperDir := writeFakeArtlistScraper(t, []Candidate{
		{
			ID:        "gate09-mixed-ok",
			Title:     "Mixed OK Clip",
			SourceRef: "https://cdn.artlist.io/video/mixed-ok.m3u8",
			PageURL:   "https://artlist.io/clip/mixed-ok",
		},
		{
			ID:        "gate09-mixed-fail",
			Title:     "Mixed Fail Clip",
			SourceRef: "https://cdn.artlist.io/video/mixed-fail.m3u8",
			PageURL:   "https://artlist.io/clip/mixed-fail",
		},
	})

	cfg := &config.Config{
		Storage: config.StorageConfig{DataDir: tmp},
		Video:   config.VideoConfig{Duration: 15},
		External: config.ExternalConfig{
			NodeScraperDir: scraperDir,
		},
	}

	db := createTestDB(t)
	defer db.Close()

	logger := zap.NewNop()
	artlistRepo := assets.NewClipsRepository(db, logger)

	for _, clip := range []struct {
		id, name, sourceURL, term1, term2 string
	}{
		{"gate09-mixed-ok", "Mixed OK Clip", "https://cdn.artlist.io/video/mixed-ok.m3u8", "mixed", "ok"},
		{"gate09-mixed-fail", "Mixed Fail Clip", "https://cdn.artlist.io/video/mixed-fail.m3u8", "mixed", "fail"},
	} {
		_, _ = db.Exec("INSERT OR IGNORE INTO clip_search_terms (term, clip_id) VALUES (?, ?)", clip.term1, clip.id)
		_, _ = db.Exec("INSERT OR IGNORE INTO clip_search_terms (term, clip_id) VALUES (?, ?)", clip.term2, clip.id)

		a := &asset.Asset{
			ID:             clip.id,
			Name:           clip.name,
			SourceURL:      clip.sourceURL,
			Source:         "artlist",
			LifecycleState: asset.StateStaging,
			MediaType:      "video",
		}
		a.SetDownloadLink(clip.sourceURL)
		a.SetMetadataString("index_state", string(asset.StateDiscovered))
		insertTestClip(t, db, a)
	}

	processor := &partialDriveProcessor{}
	recDisp := &recordingDispatcherForArtlist{stubDispatcherForArtlist: stubDispatcherForArtlist{repo: artlistRepo}}

	svc, err := NewService(ServiceDeps{
		ServicePorts: ServicePorts{
			AssetStore:    artlistRepo,
			Publisher:     &stubPublisherForArtlist{},
			RunRepository: &stubRunRepoForArtlist{},
		},
		ServiceDependencies: ServiceDependencies{
			Cfg:            cfg,
			MainDB:         db,
			Log:            logger,
			Dispatcher:     recDisp,
			MediaProcessor: processor,
		},
	})
	require.NoError(t, err)
	defer svc.Close()

	resp, err := svc.runOrchestrator.RunTag(ctx, &RunTagRequest{
		Term:         "mixed ok fail",
		Limit:        2,
		Strategy:     "replace",
		RootFolderID: "gate09-root-folder",
	})
	require.NoError(t, err)
	require.NotNil(t, resp)

	// ── Per-item discrimination ──
	assert.Equal(t, 1, resp.Processed, "only 1 clip (the one with Drive fields) should be Processed")
	assert.Equal(t, 0, resp.Failed, "no clips should be marked failed (processor returned success for both)")
	require.Len(t, resp.Items, 2)

	// Only 1 dispatcher call (for the clip with Drive fields)
	assert.Equal(t, 1, recDisp.DispatchCount(), "only 1 clip should be dispatched (the one with Drive fields)")

	// SQLite: OK clip should be ACTIVE with Drive fields; FAIL clip should still be STAGING
	var okLifecycle, okDriveLink, okDriveFileID string
	err = db.QueryRow(`
		SELECT lifecycle_state,
		       COALESCE(drive_link, ''),
		       COALESCE(drive_file_id, '')
		FROM media_assets WHERE id = ?
	`, "gate09-mixed-ok").Scan(&okLifecycle, &okDriveLink, &okDriveFileID)
	require.NoError(t, err)
	assert.Equal(t, string(asset.StateActive), okLifecycle, "OK clip should be ACTIVE")
	assert.NotEmpty(t, okDriveLink, "OK clip should have drive_link")
	assert.NotEmpty(t, okDriveFileID, "OK clip should have drive_file_id")

	var failLifecycle, failDriveLink, failDriveFileID string
	err = db.QueryRow(`
		SELECT lifecycle_state,
		       COALESCE(drive_link, ''),
		       COALESCE(drive_file_id, '')
		FROM media_assets WHERE id = ?
	`, "gate09-mixed-fail").Scan(&failLifecycle, &failDriveLink, &failDriveFileID)
	require.NoError(t, err)
	assert.Equal(t, string(asset.StateStaging), failLifecycle, "FAIL clip should still be STAGING")
	assert.Empty(t, failDriveLink, "FAIL clip should have empty drive_link")
	assert.Empty(t, failDriveFileID, "FAIL clip should have empty drive_file_id")
}
