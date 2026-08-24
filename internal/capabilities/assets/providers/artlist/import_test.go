package assets

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	assetfinalizer "github.com/Marcuss-ops/PipelineGen/internal/application/assets/finalizer"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/assets"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/outboxevents"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
)

func TestCandidateToAsset_MapsProviderMetadata(t *testing.T) {
	candidate := &Candidate{
		ID:           "123456",
		Title:        "Skyline at Sundown",
		Description:  "City skyline during sunset",
		Creator:      "John Richter",
		PageURL:      "https://artlist.io/stock-footage/clip/skyline-at-sundown/123456",
		SourceRef:    "https://cdn.artlist.io/123456.mp4",
		ThumbnailURL: "https://cdn.artlist.io/123456/thumb.jpg",
		PreviewURL:   "https://cdn.artlist.io/123456/preview.mp4",
		Keywords:     []string{"Skyline", "Evening", "Clouds"},
		Categories:   []string{"Cities", "Travel"},
		RawMetadata: map[string]any{
			"country":  "Spain",
			"location": "Barcelona",
		},
	}

	clip := candidateToAsset(candidate, candidate.PageURL)

	assert.Equal(t, "123456", clip.ID)
	assert.Equal(t, "Skyline at Sundown", clip.Name)
	assert.Equal(t, asset.Source("artlist"), clip.Source)
	assert.Equal(t, asset.MediaType("video"), clip.MediaType)
	assert.Equal(t, "https://artlist.io/stock-footage/clip/skyline-at-sundown/123456", clip.ClipPageURL)
	assert.Equal(t, "https://cdn.artlist.io/123456.mp4", clip.SourceURL)
	assert.Equal(t, "https://cdn.artlist.io/123456/thumb.jpg", clip.ThumbnailURL)
	assert.ElementsMatch(t, []string{"Skyline", "Evening", "Clouds"}, clip.Tags)
	assert.Contains(t, clip.Metadata, "creator")
	assert.Equal(t, "John Richter", clip.Metadata["creator"])
	assert.Equal(t, []string{"Skyline", "Evening", "Clouds"}, clip.Metadata["provider_tags"])
	assert.Equal(t, []string{"Cities", "Travel"}, clip.Metadata["provider_categories"])
	assert.Equal(t, "Spain", clip.Metadata["country"])
	assert.Equal(t, "Barcelona", clip.Metadata["location"])
	assert.Equal(t, "artlist", clip.Metadata["metadata_origin"])
	assert.Contains(t, clip.Metadata["description"], "sunset")
	assert.Equal(t, "https://cdn.artlist.io/123456/preview.mp4", clip.Metadata["preview_url"])
}

func TestImportClip_MetadataOnly(t *testing.T) {
	rec := &fakeDispatcherForImport{}
	svc := &Service{
		log:        zap.NewNop(),
		dispatcher: rec,
		detailFetcher: &fakeDetailFetcher{
			candidate: &Candidate{
				ID:        "123",
				Title:     "Forest",
				PageURL:   "https://artlist.io/clip/forest/123",
				SourceRef: "https://cdn.artlist.io/123.mp4",
				Keywords:  []string{"forest", "trees"},
			},
		},
	}

	resp, err := svc.ImportClip(context.Background(), &ImportClipRequest{
		ClipPageURL: "https://artlist.io/clip/forest/123",
		Download:    false,
	})

	require.NoError(t, err)
	assert.True(t, resp.OK)
	assert.Equal(t, "123", resp.ClipID)
	assert.Equal(t, "discovered", resp.Status)
	assert.Equal(t, "Forest", resp.Name)
	assert.Equal(t, 1, rec.saveDiscoveredCalls)
	require.NotNil(t, rec.saved)
	assert.Equal(t, "123", rec.saved.ID)
	assert.Equal(t, asset.Source("artlist"), rec.saved.Source)
}

func TestImportClip_RequiresClipPageURL(t *testing.T) {
	svc := &Service{
		log:           zap.NewNop(),
		detailFetcher: &fakeDetailFetcher{},
	}

	_, err := svc.ImportClip(context.Background(), &ImportClipRequest{ClipPageURL: ""})
	assert.ErrorIs(t, err, ErrEmpty)
}

func TestImportClip_DetailFetcherUnavailable(t *testing.T) {
	svc := &Service{
		log: zap.NewNop(),
	}

	_, err := svc.ImportClip(context.Background(), &ImportClipRequest{ClipPageURL: "https://artlist.io/clip/123"})
	assert.ErrorIs(t, err, ErrUnavailable)
}

func TestStringSliceFromMetadata(t *testing.T) {
	assert.Equal(t, []string{"a", "b"}, stringSliceFromMetadata(map[string]any{"k": []string{"a", "b"}}, "k"))
	assert.Equal(t, []string{"a", "b"}, stringSliceFromMetadata(map[string]any{"k": []any{"a", "b"}}, "k"))
	assert.Equal(t, []string{"a", "b"}, stringSliceFromMetadata(map[string]any{"k": []any{"a", 1, "b"}}, "k"))
	assert.Nil(t, stringSliceFromMetadata(map[string]any{"k": "not-a-slice"}, "k"))
	assert.Nil(t, stringSliceFromMetadata(map[string]any{}, "missing"))
	assert.Nil(t, stringSliceFromMetadata(nil, "missing"))
}

func TestImportClip_DownloadPersistsMediaAsset(t *testing.T) {
	ctx := context.Background()
	db := createTestDB(t)
	defer db.Close()

	cfg := &config.Config{
		Storage: config.StorageConfig{DataDir: t.TempDir()},
		Video:   config.VideoConfig{Duration: 15},
		Drive: config.DriveConfig{
			ArtlistRootFolder: "artlist-root-folder",
		},
	}

	logger := zap.NewNop()
	committer := assets.NewSQLiteAssetCommitter(db, outboxevents.NewRepository(db), logger)
	finalizer := assetfinalizer.NewAssetTxFinalizer(logger, committer)
	publisher := &stubPublisherForArtlist{}

	svc := &Service{
		cfg:                cfg,
		log:                logger,
		publisher:          publisher,
		detailFetcher:      &fakeDetailFetcher{candidate: &Candidate{ID: "346928", Title: "Maya Ruins", PageURL: "https://artlist.io/stock-footage/clip/mayan-ruins/346928", SourceRef: "https://cdn.artlist.io/346928.mp4"}},
		mediaProcessor:     &successMediaProcessor{},
		transcriber:        &stubTranscriber{},
		textTrackRepo:      &stubTextTrackRepo{},
		assetFinalizer:     finalizer,
		mainDB:             db,
		destinationService: &DestinationService{publisher: publisher, cfg: cfg},
	}
	svc.runOrchestrator = NewRunOrchestratorService(svc)

	resp, err := svc.ImportClip(ctx, &ImportClipRequest{
		ClipPageURL: "https://artlist.io/stock-footage/clip/mayan-ruins/346928",
		Download:    true,
	})
	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.True(t, resp.OK)
	assert.Equal(t, "processed", resp.Status)
	assert.Equal(t, "346928", resp.ClipID)
	assert.NotEmpty(t, resp.DriveFileID)
	assert.NotEmpty(t, resp.DriveLink)
	assert.NotEmpty(t, resp.LegacyFileMD5)

	var rowCount int
	require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM media_assets WHERE id = ?`, "346928").Scan(&rowCount))
	assert.Equal(t, 1, rowCount)

	var source, driveFileID, driveLink, fileHash string
	require.NoError(t, db.QueryRow(`
		SELECT source, COALESCE(drive_file_id, ''), COALESCE(drive_link, ''), COALESCE(legacy_file_md5, '')
		FROM media_assets WHERE id = ?
	`, "346928").Scan(&source, &driveFileID, &driveLink, &fileHash))
	assert.Equal(t, "artlist", source)
	assert.NotEmpty(t, driveFileID)
	assert.NotEmpty(t, driveLink)
	assert.NotEmpty(t, fileHash)

	var outboxCount int
	require.NoError(t, db.QueryRow(`
		SELECT COUNT(*) FROM outbox_events WHERE event_type = 'asset.index.requested' AND aggregate_id = ?
	`, "346928").Scan(&outboxCount))
	assert.Equal(t, 1, outboxCount)
}
