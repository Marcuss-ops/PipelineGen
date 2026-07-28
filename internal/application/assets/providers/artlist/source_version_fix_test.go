// Package artlist — PR-ARTLIST-SOURCE-VERSION-FIX regression tests.
//
// TDD regression-guard for the 161 dead-letter outbox events caused by
// empty source_version in the supersede gate (2026-07-09 audit).
//
// Root cause: mediaProcessor returned empty FileHash for some Artlist clips,
// stagePersistResults dispatched with empty contentHash, and the IndexingHandler
// dead-lettered the events (source_version="" is terminal).
//
// Fix: stagePersistResults is now fail-closed — it rejects assets with an
// empty SHA-256 and never emits an outbox event. The fallback hash path has
// been retired; every persisted asset must carry a real content hash.
package artlist

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	assetfinalizer "github.com/Marcuss-ops/PipelineGen/internal/application/assets/finalizer"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/assets"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/security"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
)

// emptyHashMediaProcessor returns ProcessResults with empty FileHash,
// simulating the real scenario where the media processor fails to compute
// a content hash (e.g. download step succeeded but hash step was skipped).
type emptyHashMediaProcessor struct{}

func (f *emptyHashMediaProcessor) Process(_ context.Context, input *asset.ProcessInput) (*asset.ProcessResult, error) {
	return &asset.ProcessResult{
		ID:           input.ID,
		Filename:     input.ID + "_processed.mp4",
		LocalPath:    input.OutputDir + "/" + input.ID + "_processed.mp4",
		FileHash:     "", // empty SHA-256
		DriveLink:    "https://drive.google.com/file/d/" + input.ID + "-drive/view",
		DriveFileID:  input.ID + "-drive-id",
		DownloadLink: input.SourceURL,
		Status:       "processed",
	}, nil
}

// TestSourceVersionFix_EmptyHashRejected verifies that stagePersistResults
// is fail-closed: when the media processor returns an empty FileHash the
// clip is marked as "hash_missing", no outbox event is emitted, and no
// row is written to media_assets by the AssetFinalizerTx.
func TestSourceVersionFix_EmptyHashRejected(t *testing.T) {
	ctx := context.Background()
	tmp := t.TempDir()

	security.AddAllowedHost("cdn.artlist.io")

	scraperDir := writeFakeArtlistScraper(t, []Candidate{
		{
			ID:        "sv-fix-clip-1",
			Title:     "Empty Hash Clip A",
			SourceRef: "https://cdn.artlist.io/video/sv-fix-a.m3u8",
			PageURL:   "https://artlist.io/clip/sv-fix-a",
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

	// Pre-populate clip_search_terms + STAGING/DISCOVERED clip.
	_, _ = db.Exec("INSERT OR IGNORE INTO clip_search_terms (term, clip_id) VALUES (?, ?)", "svfix", "sv-fix-clip-1")
	a := &asset.Asset{
		ID:             "sv-fix-clip-1",
		Name:           "Empty Hash Clip A",
		SourceURL:      "https://cdn.artlist.io/video/sv-fix-a.m3u8",
		Source:         "artlist",
		LifecycleState: asset.StateStaging,
		MediaType:      "video",
	}
	a.SetDownloadLink("https://cdn.artlist.io/video/sv-fix-a.m3u8")
	a.SetMetadataString("index_state", string(asset.StateDiscovered))
	insertTestClip(t, db, a)

	processor := &emptyHashMediaProcessor{}

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
		Term:         "svfix",
		Limit:        1,
		Strategy:     "replace",
		RootFolderID: "sv-fix-root",
	})
	require.NoError(t, err)
	require.NotNil(t, resp)

	// The clip is NOT processed — fail-closed on empty SHA-256.
	assert.Equal(t, 0, resp.Processed, "clip with empty FileHash must NOT be processed")
	assert.Equal(t, 1, resp.Failed, "clip with empty FileHash must be counted as failed")
	require.Len(t, resp.Items, 1)
	assert.Equal(t, "hash_missing", resp.Items[0].Status,
		"item status must be 'hash_missing' when SHA-256 is empty")
	assert.Contains(t, resp.Items[0].Error, "SHA-256 missing",
		"item error must explain the missing SHA-256")

	// No outbox event should be emitted.
	assert.Equal(t, 0, outboxEventCount(db),
		"no asset.index.requested outbox event should be emitted for rejected clip")

	// media_assets row should remain in its pre-run state (STAGING after
	// discovery, because the finalizer never ran).
	var lifecycleState string
	err = db.QueryRow("SELECT lifecycle_state FROM media_assets WHERE id = ?", "sv-fix-clip-1").Scan(&lifecycleState)
	require.NoError(t, err)
	assert.Equal(t, string(asset.StateStaging), lifecycleState,
		"media_assets row must remain STAGING when finalizer is skipped")

	t.Log("Source-version fix verified: empty SHA-256 is rejected, no outbox event emitted")
}

// Compile-time: emptyHashMediaProcessor satisfies the Processor port.
var _ asset.Processor = (*emptyHashMediaProcessor)(nil)
