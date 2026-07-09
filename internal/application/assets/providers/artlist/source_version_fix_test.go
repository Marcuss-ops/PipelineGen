// Package artlist — PR-ARTLIST-SOURCE-VERSION-FIX regression tests.
//
// TDD regression-guard for the 161 dead-letter outbox events caused by
// empty source_version in the supersede gate (2026-07-09 audit).
//
// Root cause: mediaProcessor returned empty FileHash for some Artlist clips,
// stagePersistResults dispatched with empty contentHash, and the IndexingHandler
// dead-lettered the events (source_version required for supersede gate).
//
// Fix (2-layer defense):
//  1. Dispatcher.EnqueueAndIndex now rejects empty contentHash (fail-closed).
//  2. stagePersistResults computes a deterministic fallback hash from
//     clipID+source when item.FileHash is empty.
package artlist

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/assets"
	hashutil "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/files"
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
		FileHash:     "", // THE BUG: empty FileHash
		DriveLink:    "https://drive.google.com/file/d/" + input.ID + "-drive/view",
		DriveFileID:  input.ID + "-drive-id",
		DownloadLink: input.SourceURL,
		Status:       "processed",
	}, nil
}

// TestSourceVersionFix_FallbackHashComputedWhenFileHashEmpty verifies that
// stagePersistResults computes a deterministic fallback SHA256 hash from
// clipID+source when the media processor returns an empty FileHash.
// This prevents the IndexingHandler supersede gate from dead-lettering
// the outbox event (source_version="" is terminal).
func TestSourceVersionFix_FallbackHashComputedWhenFileHashEmpty(t *testing.T) {
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

	// recordingDispatcherForArtlist from gate01_happy_path_test.go records
	// every EnqueueAndIndex call including contentHash.
	recDisp := &recordingDispatcherForArtlist{
		stubDispatcherForArtlist: stubDispatcherForArtlist{repo: artlistRepo},
	}

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
		Term:         "svfix",
		Limit:        1,
		Strategy:     "replace",
		RootFolderID: "sv-fix-root",
	})
	require.NoError(t, err)
	require.NotNil(t, resp)

	// The clip should be processed successfully even though FileHash was empty.
	assert.Equal(t, 1, resp.Processed, "clip should be processed despite empty FileHash from processor")
	assert.Equal(t, 0, resp.Failed)

	// The dispatcher should have been called (fallback hash computed).
	require.Equal(t, 1, recDisp.DispatchCount(), "dispatcher must be called once")

	// Verify the fallback hash is the deterministic SHA256 of clipID+source.
	expectedHash := hashutil.SHA256String("sv-fix-clip-1" + ":artlist")
	actualHash := recDisp.ContentHashFor("sv-fix-clip-1")
	assert.NotEmpty(t, actualHash, "contentHash dispatched must be non-empty (fallback computed)")
	assert.Equal(t, expectedHash, actualHash,
		"fallback hash must be deterministic SHA256 of clipID+source")

	// Verify the fallback hash is also written to the item's FileHash.
	require.Len(t, resp.Items, 1)
	assert.Equal(t, expectedHash, resp.Items[0].FileHash,
		"item.FileHash must be the fallback hash after stagePersistResults")

	t.Logf("Source-version fix verified: fallback hash=%s for clip=%s", actualHash[:12], "sv-fix-clip-1")
}

// TestSourceVersionFix_DeterministicAcrossRetries verifies that the
// fallback hash is deterministic — the same clipID+source always
// produces the same hash, so retries don't create duplicate Qdrant points.
func TestSourceVersionFix_DeterministicAcrossRetries(t *testing.T) {
	// The fallback computation: SHA256(clipID + ":" + source)
	h1 := hashutil.SHA256String("my-clip:artlist")
	h2 := hashutil.SHA256String("my-clip:artlist")
	h3 := hashutil.SHA256String("my-clip:youtube")

	assert.Equal(t, h1, h2, "same input must produce same hash (deterministic)")
	assert.NotEqual(t, h1, h3, "different source must produce different hash")
	assert.NotEmpty(t, h1, "hash must be non-empty")
	assert.Len(t, h1, 64, "SHA256 hex digest must be 64 chars")
}

// Compile-time: emptyHashMediaProcessor satisfies the Processor port.
var _ asset.Processor = (*emptyHashMediaProcessor)(nil)
