// Package artlist — Gate 02 + Gate 09 Drive Contract Tests (ARTLIST-DOD-2026-07-07).
//
// PR-ARTLIST-DOD-GATE-02-DRIVE-UPLOAD: verify every processed clip has
// non-empty DriveFileID + DriveLink + DownloadLink + LegacyFileMD5 after
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

	assetfinalizer "github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/finalizer"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	assets "github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/assets/imagesregistry"
	"github.com/Marcuss-ops/PipelineGen/pkg/security"
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
		ID:            input.ID,
		Filename:      input.ID + "_processed.mp4",
		LocalPath:     input.OutputDir + "/" + input.ID + "_processed.mp4",
		LegacyFileMD5: "drivefail-hash-" + input.ID,
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
		ID:            input.ID,
		Filename:      input.ID + "_processed.mp4",
		LocalPath:     input.OutputDir + "/" + input.ID + "_processed.mp4",
		LegacyFileMD5: "partial-hash-" + input.ID,
		DownloadLink:  input.SourceURL,
		Status:        "processed",
	}

	// Deterministic: any clip ID ending with "-ok" gets Drive fields;
	// any other clip simulates Drive upload failure.
	// This suffix-based check is immune to goroutine processing order
	// and works across all test fixtures (Gate 09 mixed, Gate 05 dispatch).
	if len(input.ID) >= 3 && input.ID[len(input.ID)-3:] == "-ok" {
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
// DriveLink, DownloadLink, and LegacyFileMD5. This is the canonical
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
			LifecycleState: asset.StateActive,
			MediaType:      "video",
		}
		a.SetDownloadLink(clip.sourceURL)
		a.SetMetadataString("index_state", string(asset.StateDiscovered))
		insertTestClip(t, db, a)
	}

	processor := &successMediaProcessor{}

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
			i, item.ClipID, item.DriveFileID, item.DriveLink, item.DownloadLink, item.LegacyFileMD5)

		assert.NotEmpty(t, item.DriveFileID, "item[%d] %s: DriveFileID must be non-empty", i, item.ClipID)
		assert.NotEmpty(t, item.DriveLink, "item[%d] %s: DriveLink must be non-empty", i, item.ClipID)
		assert.NotEmpty(t, item.DownloadLink, "item[%d] %s: DownloadLink must be non-empty", i, item.ClipID)
		assert.NotEmpty(t, item.LegacyFileMD5, "item[%d] %s: LegacyFileMD5 must be non-empty", i, item.ClipID)

		// DriveLink must look like a valid Drive URL
		assert.Contains(t, item.DriveLink, "drive.google.com", "item[%d] %s: DriveLink must be a Drive URL", i, item.ClipID)

		// DriveFileID must match the processor's pattern
		assert.Contains(t, item.DriveFileID, "-drive-id", "item[%d] %s: DriveFileID must match pattern", i, item.ClipID)
	}

	assert.Equal(t, 2, resp.Processed, "both clips should be processed")
	assert.Equal(t, 0, resp.Failed, "no clips should fail")
	assert.Equal(t, 2, outboxEventCount(db), "finalizer should emit 2 outbox events")

	// Gate 3: SQLite Drive columns verified
	for _, clipID := range []string{"gate02-clip-1", "gate02-clip-2"} {
		var driveLink, fileHash, driveFileID string
		err := db.QueryRow(`
			SELECT COALESCE(drive_link, ''),
			       COALESCE(legacy_file_md5, ''),
			       COALESCE(drive_file_id, '')
			FROM media_assets WHERE id = ?
		`, clipID).Scan(&driveLink, &fileHash, &driveFileID)
		require.NoError(t, err, "clip %s should exist in media_assets", clipID)
		assert.NotEmpty(t, driveLink, "SQLite: clip %s drive_link must be non-empty", clipID)
		assert.NotEmpty(t, fileHash, "SQLite: clip %s legacy_file_md5 must be non-empty", clipID)
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
		LifecycleState: asset.StateActive,
		MediaType:      "video",
	}
	a.SetDownloadLink("https://cdn.artlist.io/video/gate09.m3u8")
	a.SetMetadataString("index_state", string(asset.StateDiscovered))
	insertTestClip(t, db, a)

	processor := &driveFailureProcessor{}

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
		Term:         "drive failure",
		Limit:        1,
		Strategy:     "replace",
		RootFolderID: "gate09-root-folder",
	})
	require.NoError(t, err)
	require.NotNil(t, resp)

	// ── Gate 9 assertions ──

	// The item appears in Items (processor returned it) but stagePersistResults
	// marked it as "drive_upload_failed" because Drive fields were missing.
	// Processed is 0 — the gate skipped it.
	require.Len(t, resp.Items, 1)
	assert.Equal(t, "drive_upload_failed", resp.Items[0].Status, "item status should be 'drive_upload_failed'")
	assert.Contains(t, resp.Items[0].Error, "missing Drive fields", "item Error should explain the failure")
	assert.Empty(t, resp.Items[0].DriveFileID, "DriveFileID should be empty (Drive upload failed)")
	assert.Empty(t, resp.Items[0].DriveLink, "DriveLink should be empty (Drive upload failed)")
	assert.NotEmpty(t, resp.Items[0].LegacyFileMD5, "LegacyFileMD5 should be present (transcode succeeded)")

	// Processed MUST be 0 — the gate in stagePersistResults skipped it
	assert.Equal(t, 0, resp.Processed, "Processed must be 0 when Drive fields are missing")
	// PR-ARTLIST-OUTCOME-ACCOUNTING (P1, July 2026): the missing-Drive-fields
	// clip is now correctly counted as Failed (no longer a silent drop).
	// See run_orchestrator_stages.go stagePersistResults Drive-gate branch.
	assert.Equal(t, 1, resp.Failed, "PR-ARTLIST-OUTCOME-ACCOUNTING: missing-Drive-fields clip counts as Failed")

	// Dispatcher MUST NOT have been called
	assert.Equal(t, 0, outboxEventCount(db), "finalizer must NOT emit outbox event when Drive fields are missing")

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
			LifecycleState: asset.StateActive,
			MediaType:      "video",
		}
		a.SetDownloadLink(clip.sourceURL)
		a.SetMetadataString("index_state", string(asset.StateDiscovered))
		insertTestClip(t, db, a)
	}

	processor := &partialDriveProcessor{}

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
		Term:         "mixed ok fail",
		Limit:        2,
		Strategy:     "replace",
		RootFolderID: "gate09-root-folder",
	})
	require.NoError(t, err)
	require.NotNil(t, resp)

	// ── Per-item discrimination ──
	assert.Equal(t, 1, resp.Processed, "only 1 clip (the one with Drive fields) should be Processed")
	// PR-ARTLIST-OUTCOME-ACCOUNTING (P1, July 2026): the missing-Drive-fields
	// clip is now correctly counted as Failed in this mixed batch. Pre-PR
	// this clip was a silent drop with no Failed tally — the operator never
	// noticed the gap. See run_orchestrator_stages.go stagePersistResults
	// Drive-gate branch.
	assert.Equal(t, 1, resp.Failed, "PR-ARTLIST-OUTCOME-ACCOUNTING: missing-Drive-fields clip counts as Failed in mixed batch")
	require.Len(t, resp.Items, 2)

	// Only 1 dispatcher call (for the clip with Drive fields)
	assert.Equal(t, 1, outboxEventCount(db), "finalizer should emit 1 outbox event for the clip with Drive fields")

	// SQLite: OK clip should be ACTIVE with Drive fields; FAIL clip should still be STAGING
	var okLifecycle, okDriveLink, okDriveFileID string
	err = db.QueryRow(`
		SELECT lifecycle_state,
		       COALESCE(drive_link, ''),
		       COALESCE(drive_file_id, '')
		FROM media_assets WHERE id = ?
	`, "gate09-mixed-ok").Scan(&okLifecycle, &okDriveLink, &okDriveFileID)
	require.NoError(t, err)
	assert.Equal(t, "PUBLISHED", okLifecycle, "OK clip should be PUBLISHED")
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
	assert.Equal(t, string(asset.StateStaging), failLifecycle, "FAIL clip should remain in pre-run state")
	assert.Empty(t, failDriveLink, "FAIL clip should have empty drive_link")
	assert.Empty(t, failDriveFileID, "FAIL clip should have empty drive_file_id")
}

// TestGate05_OutboxDispatchContract verifies the outbox dispatch
// contract (Gate 05 of ARTLIST-DOD-2026-07-07):
//
//  1. The dispatcher is called exactly once per successfully processed clip
//  2. Each dispatch carries the clip's LegacyFileMD5 as the content hash
//  3. Dispatch only fires for clips with non-empty Drive fields AND LegacyFileMD5
//  4. Dispatch is NOT called for clips skipped by the Drive gate
//
// godlike/07 no-fake-availability: a clip with empty LegacyFileMD5 must NOT
// reach the dispatcher — the persist gate in stagePersistResults must
// reject it before dispatch.
func TestGate05_OutboxDispatchContract(t *testing.T) {
	ctx := context.Background()
	tmp := t.TempDir()

	security.AddAllowedHost("cdn.artlist.io")

	scraperDir := writeFakeArtlistScraper(t, []Candidate{
		{
			ID:        "gate05-clip-a",
			Title:     "Outbox Test A",
			SourceRef: "https://cdn.artlist.io/video/gate05-a.m3u8",
			PageURL:   "https://artlist.io/clip/outbox-test-a",
		},
		{
			ID:        "gate05-clip-b",
			Title:     "Outbox Test B",
			SourceRef: "https://cdn.artlist.io/video/gate05-b.m3u8",
			PageURL:   "https://artlist.io/clip/outbox-test-b",
		},
		{
			ID:        "gate05-clip-c",
			Title:     "Outbox Test C",
			SourceRef: "https://cdn.artlist.io/video/gate05-c.m3u8",
			PageURL:   "https://artlist.io/clip/outbox-test-c",
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
		{"gate05-clip-a", "Outbox Test A", "https://cdn.artlist.io/video/gate05-a.m3u8", "outbox", "test"},
		{"gate05-clip-b", "Outbox Test B", "https://cdn.artlist.io/video/gate05-b.m3u8", "outbox", "test"},
		{"gate05-clip-c", "Outbox Test C", "https://cdn.artlist.io/video/gate05-c.m3u8", "outbox", "test"},
	} {
		_, _ = db.Exec("INSERT OR IGNORE INTO clip_search_terms (term, clip_id) VALUES (?, ?)", clip.term1, clip.id)
		_, _ = db.Exec("INSERT OR IGNORE INTO clip_search_terms (term, clip_id) VALUES (?, ?)", clip.term2, clip.id)

		a := &asset.Asset{
			ID:             clip.id,
			Name:           clip.name,
			SourceURL:      clip.sourceURL,
			Source:         "artlist",
			LifecycleState: asset.StateActive,
			MediaType:      "video",
		}
		a.SetDownloadLink(clip.sourceURL)
		a.SetMetadataString("index_state", string(asset.StateDiscovered))
		insertTestClip(t, db, a)
	}

	// successMediaProcessor returns unique LegacyFileMD5 per clip ID.
	// The gate05-hash- prefix is the canonical pattern from
	// successMediaProcessor.Process in gate01_happy_path_test.go.
	processor := &successMediaProcessor{}

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
		Term:         "outbox test",
		Limit:        3,
		Strategy:     "replace",
		RootFolderID: "gate05-root-folder",
	})
	require.NoError(t, err)
	require.NotNil(t, resp)

	// ── Gate 5: Contract 1 — dispatch count equals processed count ──
	assert.Equal(t, 3, resp.Processed, "all 3 clips should be processed")
	assert.Equal(t, 3, outboxEventCount(db), "dispatcher should be called exactly once per processed clip")

	// ── Gate 5: Contract 2 — per-clip content hash verification ──
	expectedClipIDs := []string{"gate05-clip-a", "gate05-clip-b", "gate05-clip-c"}
	for _, clipID := range expectedClipIDs {
		dispatchedHash := outboxSourceVersionFor(db, clipID)
		assert.NotEmpty(t, dispatchedHash, "clip %s should have been dispatched", clipID)

		// successMediaProcessor sets LegacyFileMD5 = "gate01-hash-" + input.ID
		// stagePersistResults calls bridge.Dispatch(ctx, clip, clip.LegacyFileMD5())
		// which passes clip.LegacyFileMD5() as the content hash to EnqueueAndIndex.
		//
		// After stagePersistResults hydrates the clip, clip.LegacyFileMD5() should
		// equal the processor's LegacyFileMD5 ("gate01-hash-<clipID>").
		// The recording dispatcher sees the hash that EnqueueAndIndex received.
		expectedHash := "gate01-hash-" + clipID
		assert.Equal(t, expectedHash, dispatchedHash,
			"clip %s: dispatched content hash must match the processor's LegacyFileMD5", clipID)
	}

	// ── Gate 5: Contract 3 — only dispatched clips are in SQLite ──
	for _, clipID := range expectedClipIDs {
		var driveLink string
		err := db.QueryRow("SELECT COALESCE(drive_link, '') FROM media_assets WHERE id = ?", clipID).Scan(&driveLink)
		require.NoError(t, err)
		assert.NotEmpty(t, driveLink, "SQLite: clip %s should have non-empty drive_link after dispatch", clipID)
	}

	// ── Gate 5: Contract 4 — no duplicate outbox events ──
	// Each clip ID should appear exactly once in outbox_events.
	seen := map[string]int{}
	rows, err := db.Query(`SELECT aggregate_id FROM outbox_events WHERE event_type = 'asset.index.requested'`)
	require.NoError(t, err)
	defer rows.Close()
	for rows.Next() {
		var cid string
		require.NoError(t, rows.Scan(&cid))
		seen[cid]++
	}
	require.NoError(t, rows.Err())
	for _, clipID := range expectedClipIDs {
		assert.Equal(t, 1, seen[clipID], "clip %s should have exactly one outbox event (no duplicates)", clipID)
	}
}

// TestGate05_OutboxNoDispatchWithoutDriveFields verifies the negative
// contract: when Drive fields are missing, the dispatcher is NOT called.
// Uses partialDriveProcessor to produce a mixed batch — one clip with
// Drive, one without — and asserts the dispatcher is only called for
// the clip with Drive fields.
func TestGate05_OutboxNoDispatchWithoutDriveFields(t *testing.T) {
	ctx := context.Background()
	tmp := t.TempDir()

	security.AddAllowedHost("cdn.artlist.io")

	scraperDir := writeFakeArtlistScraper(t, []Candidate{
		{
			ID:        "gate05-ok",
			Title:     "Dispatch OK",
			SourceRef: "https://cdn.artlist.io/video/gate05-ok.m3u8",
			PageURL:   "https://artlist.io/clip/dispatch-ok",
		},
		{
			ID:        "gate05-nodrive",
			Title:     "Dispatch No Drive",
			SourceRef: "https://cdn.artlist.io/video/gate05-nodrive.m3u8",
			PageURL:   "https://artlist.io/clip/dispatch-nodrive",
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
		{"gate05-ok", "Dispatch OK", "https://cdn.artlist.io/video/gate05-ok.m3u8", "dispatch", "ok"},
		{"gate05-nodrive", "Dispatch No Drive", "https://cdn.artlist.io/video/gate05-nodrive.m3u8", "dispatch", "nodrive"},
	} {
		_, _ = db.Exec("INSERT OR IGNORE INTO clip_search_terms (term, clip_id) VALUES (?, ?)", clip.term1, clip.id)
		_, _ = db.Exec("INSERT OR IGNORE INTO clip_search_terms (term, clip_id) VALUES (?, ?)", clip.term2, clip.id)

		a := &asset.Asset{
			ID:             clip.id,
			Name:           clip.name,
			SourceURL:      clip.sourceURL,
			Source:         "artlist",
			LifecycleState: asset.StateActive,
			MediaType:      "video",
		}
		a.SetDownloadLink(clip.sourceURL)
		a.SetMetadataString("index_state", string(asset.StateDiscovered))
		insertTestClip(t, db, a)
	}

	// partialDriveProcessor gives Drive fields only to "gate05-ok"
	// (the ID-suffix check covers -ok but not -nodrive).
	processor := &partialDriveProcessor{}

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
		Term:         "dispatch ok nodrive",
		Limit:        2,
		Strategy:     "replace",
		RootFolderID: "gate05-root-folder",
	})
	require.NoError(t, err)
	require.NotNil(t, resp)

	// Only gate05-ok has Drive fields → dispatched; gate05-nodrive skipped
	assert.Equal(t, 1, resp.Processed)
	assert.Equal(t, 1, outboxEventCount(db))

	// The OK clip was dispatched with the correct hash
	assert.NotEmpty(t, outboxSourceVersionFor(db, "gate05-ok"),
		"gate05-ok should have been dispatched with a content hash")

	// The no-drive clip was NOT dispatched
	assert.Empty(t, outboxSourceVersionFor(db, "gate05-nodrive"),
		"gate05-nodrive should NOT have been dispatched (no Drive fields)")

	// SQLite: OK clip has Drive link, no-drive clip doesn't
	var okDrive, nodriveDrive string
	_ = db.QueryRow("SELECT COALESCE(drive_link, '') FROM media_assets WHERE id = 'gate05-ok'").Scan(&okDrive)
	_ = db.QueryRow("SELECT COALESCE(drive_link, '') FROM media_assets WHERE id = 'gate05-nodrive'").Scan(&nodriveDrive)
	assert.NotEmpty(t, okDrive, "OK clip should have drive_link in SQLite")
	assert.Empty(t, nodriveDrive, "no-drive clip should have empty drive_link in SQLite")
}
