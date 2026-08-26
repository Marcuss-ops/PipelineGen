package adapters

import (
	"context"
	asset "github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	"testing"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/sourcing"
	youtubetypes "github.com/Marcuss-ops/PipelineGen/internal/capabilities/youtube/dto"
	youtubeports "github.com/Marcuss-ops/PipelineGen/internal/capabilities/youtube/ports"
	youtubeapp "github.com/Marcuss-ops/PipelineGen/internal/capabilities/youtube/usecase"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset/detail"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/shared/portutil"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// ── YouTube composition fail-fast tests (PR2 typed-nil defence) ─────────

// TestYouTubeComposition_FailsBeforeServiceConstruction verifies that
// typed-nil required dependencies are caught at composition time (before
// NewService returns), not at first invocation.
func TestYouTubeComposition_FailsBeforeServiceConstruction(t *testing.T) {
	var nilRunner *stubSearchRunner
	var runner youtubeports.SearchRunnerPort = nilRunner // typed-nil

	// PR-GRUPOC-1 (July 2026): sub-builder ctor. MediaProcessor
	// intentionally nil to exercise the typed-nil guard via the
	// missing required port (the validator's 4 required-port
	// checks fire in order: SearchRunner → AssetRepo →
	// VideoPipeline → MediaProcessor; typed-nil SearchRunner
	// surfaces first).
	core, asset, video, storage, adapter := validYouTubeSubBundles()
	adapter.SearchRunner = runner
	asset.MediaProcessor = nil // intentionally missing
	err := youtubeapp.ValidateServiceDepsFromSubBundles(core, asset, video, storage, adapter)
	require.Error(t, err, "ValidateServiceDepsFromSubBundles should reject typed-nil SearchRunner")
	assert.True(t, portutil.IsNilPort(runner), "precondition: runner should be typed-nil")
	assert.True(t, runner != nil, "precondition: typed-nil runner should != nil (Go semantics)")
}

// TestYouTubeComposition_ValidDepsBuildSuccessfully verifies that a fully
// wired ServiceDeps passes validation and can construct a Service.
func TestYouTubeComposition_ValidDepsBuildSuccessfully(t *testing.T) {
	// PR-GRUPOC-1 (July 2026): sub-builder ctor. Cfg + Log land in
	// ServiceCoreDeps; the rest of the 5-bundle pattern uses the
	// canonical test fixture. ProcessSeg lands in ServiceVideoDeps
	// (the per-segment orchestrator is video-pipeline-clustered
	// per godlike/06 SSOT).
	core, asset, video, storage, adapter := validYouTubeSubBundles()
	core.Cfg = youtubetypes.RuntimeConfig{}
	video.ProcessSeg = stubProcessYouTubeSegmentUseCase(t)

	err := youtubeapp.ValidateServiceDepsFromSubBundles(core, asset, video, storage, adapter)
	require.NoError(t, err, "fully wired deps should pass validation")

	svc := youtubeapp.NewServiceFromSubBundles(core, asset, video, storage, adapter)
	require.NotNil(t, svc, "NewServiceFromSubBundles should return non-nil with valid deps")

	// Smoke: optional port methods should not panic.
	err = svc.IndexClip(context.Background(), "test")
	assert.NoError(t, err, "IndexClip should no-op when no indexer wired")

	transcript, err := svc.TranscribeAudio(context.Background(), "/tmp/test.wav")
	assert.NoError(t, err, "TranscribeAudio should no-op when no whisper wired")
	assert.Empty(t, transcript)
} // validYouTubeSubBundles is the canonical composition-test fixture
// for YouTube sub-builder patterns (PR-GRUPOC-1, July 2026). Returns
// 5 capability-area sub-bundles with the minimum required ports
// wired (SearchRunner / AssetRepo / VideoPipeline / MediaProcessor).
// Tests mutate specific sub-bundles to exercise the typed-nil guards.
func validYouTubeSubBundles() (youtubeapp.ServiceCoreDeps, youtubeapp.ServiceAssetDeps, youtubeapp.ServiceVideoDeps, youtubeapp.ServiceStorageDeps, youtubeapp.ServiceAdapterDeps) {
	return youtubeapp.ServiceCoreDeps{},
		youtubeapp.ServiceAssetDeps{
			AssetRepo:      &stubAssetRepo{},
			MediaProcessor: &stubMediaProcessor{},
		},
		youtubeapp.ServiceVideoDeps{VideoPipeline: &stubVideoPipeline{}},
		youtubeapp.ServiceStorageDeps{},
		youtubeapp.ServiceAdapterDeps{SearchRunner: &stubSearchRunner{}}
}

// stubProcessYouTubeSegmentUseCase builds a minimal
// *ProcessYouTubeSegmentUseCase with no-op stubs for the 5
// required ports (Cache/VideoPipeline/Hash/Writer/SegmentsSvc).
// Used by composition tests that exercise NewService's
// construction path without actually invoking the per-segment
// pipeline. The stubs satisfy the interface signatures so
// NewProcessYouTubeSegmentFromSubBundles validates the required ports
// do not panic.
func stubProcessYouTubeSegmentUseCase(t *testing.T) *youtubeapp.ProcessYouTubeSegmentUseCase {
	t.Helper()
	return youtubeapp.NewProcessYouTubeSegmentFromSubBundles(
		youtubeapp.ProcessSegmentCoreDeps{
			Cache:         &stubClipCachePort{},
			VideoPipeline: &stubVideoPipelinePort{},
			Hash:          &stubHashServicePort{},
			Writer:        &stubClipAtomicWriterPort{},
			SegmentsSvc:   youtubeapp.NewSegmentsService(),
			Log:           zap.NewNop(),
		},
		youtubeapp.ProcessSegmentMediaDeps{},
		youtubeapp.ProcessSegmentMetadataDeps{},
		youtubeapp.ProcessSegmentObservabilityDeps{},
	)
}

// ── ProcessSeg stub ports (PR-GODOBJ-1 composition-test fix) ────
//
// Each stub satisfies the EXACT interface signature from
// internal/application/youtube/ports/ports.go so the compile-time
// type check at NewProcessYouTubeSegmentFromSubBundles passes.

type stubClipCachePort struct{}

func (s *stubClipCachePort) GetExisting(_ context.Context, _ string) (*youtubetypes.ExtractItem, bool, error) {
	return nil, false, nil
}

type stubVideoPipelinePort struct{}

func (s *stubVideoPipelinePort) DownloadAndCutYouTubeVideo(_ context.Context, _ youtubeports.VideoCutRequest) (*youtubeports.VideoCutResult, error) {
	return &youtubeports.VideoCutResult{}, nil
}

type stubHashServicePort struct{}

func (s *stubHashServicePort) SHA256File(_ string) (string, error) { return "", nil }
func (s *stubHashServicePort) SHA256String(_ string) string        { return "" }
func (s *stubHashServicePort) MD5String(_ string) string           { return "" }
func (s *stubHashServicePort) MD5File(_ string) (string, error)    { return "", nil }

type stubClipAtomicWriterPort struct{}

func (s *stubClipAtomicWriterPort) CommitClipAndIndexEvent(_ context.Context, _ string, _ youtubetypes.ClipAsset, _ youtubeports.IndexEventPayload) error {
	return nil
}

// TestYouTubeComposition_AllRequiredDepsRejectsNil validates that every
// required port is checked individually.
//
// PR-GRUPOC-1 (July 2026): the mutate callback operates directly on
// the 3 sub-bundles that carry the validator's 4 required ports
// (asset: AssetRepo + MediaProcessor, video: VideoPipeline,
// adapter: SearchRunner). The pre-refactor mutate signature
// (func(*youtubeapp.ServiceDeps)) was retired alongside the
// ServiceDeps type.
func TestYouTubeComposition_AllRequiredDepsRejectsNil(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*youtubeapp.ServiceAssetDeps, *youtubeapp.ServiceVideoDeps, *youtubeapp.ServiceAdapterDeps)
		wantMsg string
	}{
		{
			name: "nil SearchRunner",
			mutate: func(_ *youtubeapp.ServiceAssetDeps, _ *youtubeapp.ServiceVideoDeps, ad *youtubeapp.ServiceAdapterDeps) {
				ad.SearchRunner = nil
			},
			wantMsg: "SearchRunner",
		},
		{
			name: "nil AssetRepo",
			mutate: func(a *youtubeapp.ServiceAssetDeps, _ *youtubeapp.ServiceVideoDeps, _ *youtubeapp.ServiceAdapterDeps) {
				a.AssetRepo = nil
			},
			wantMsg: "AssetRepo",
		},
		{
			name: "nil VideoPipeline",
			mutate: func(_ *youtubeapp.ServiceAssetDeps, v *youtubeapp.ServiceVideoDeps, _ *youtubeapp.ServiceAdapterDeps) {
				v.VideoPipeline = nil
			},
			wantMsg: "VideoPipeline",
		},
		{
			name: "nil MediaProcessor",
			mutate: func(a *youtubeapp.ServiceAssetDeps, _ *youtubeapp.ServiceVideoDeps, _ *youtubeapp.ServiceAdapterDeps) {
				a.MediaProcessor = nil
			},
			wantMsg: "MediaProcessor",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			core, asset, video, storage, adapter := validYouTubeSubBundles()
			tc.mutate(&asset, &video, &adapter)
			err := youtubeapp.ValidateServiceDepsFromSubBundles(core, asset, video, storage, adapter)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.wantMsg)
		})
	}
}

// ── Stub types for composition tests ─────────────────────────────────────

type stubSearchRunner struct{}

func (s *stubSearchRunner) SearchLive(ctx context.Context, query string, limit int, sort string) ([]youtubeports.SearchLiveResult, error) {
	return nil, nil
}
func (s *stubSearchRunner) GetVideoInfo(ctx context.Context, videoURL string) (*youtubeports.DownloaderMetadata, error) {
	return nil, nil
}

type stubAssetRepo struct{}

func (s *stubAssetRepo) Upsert(ctx context.Context, a *asset.Asset) error         { return nil }
func (s *stubAssetRepo) Get(ctx context.Context, id string) (*asset.Asset, error) { return nil, nil }
func (s *stubAssetRepo) List(ctx context.Context, filter asset.Filter) ([]*asset.Asset, error) {
	return nil, nil
}
func (s *stubAssetRepo) Count(ctx context.Context, filter asset.Filter) (int64, error) { return 0, nil }
func (s *stubAssetRepo) SoftDelete(ctx context.Context, id string) error               { return nil }
func (s *stubAssetRepo) Restore(ctx context.Context, id string) error                  { return nil }
func (s *stubAssetRepo) HardDelete(ctx context.Context, id string) error               { return nil }
func (s *stubAssetRepo) FindByExternalRef(ctx context.Context, provider, externalID string) (*asset.Asset, error) {
	return nil, nil
}

type stubVideoPipeline struct{}

func (s *stubVideoPipeline) DownloadAndCutYouTubeVideo(ctx context.Context, req youtubeports.VideoCutRequest) (*youtubeports.VideoCutResult, error) {
	return &youtubeports.VideoCutResult{}, nil
}

type stubMediaProcessor struct{}

func (s *stubMediaProcessor) Process(ctx context.Context, input *detail.ProcessInput) (*detail.ProcessResult, error) {
	return &detail.ProcessResult{Status: "ok"}, nil
}

// ── fromExistingClip mapper tests ───────────────────────────────────────

// TestFromExistingClip_SetsMediaTypeAndLifecycleState regression-guards the
// YouTube registration path: without MediaType and LifecycleState, UpsertClipTx
// writes empty strings to media_assets.media_type and lifecycle_state columns.
// This is the canonical 2-line bug that this test prevents from recurring.
func TestFromExistingClip_SetsMediaTypeAndLifecycleState(t *testing.T) {
	clip := &sourcing.ExistingClip{
		ID:            "yt_abc123_0_10_v1",
		Name:          "Test Clip",
		Filename:      "test-clip.mp4",
		Source:        "youtube",
		SourceURL:     "https://www.youtube.com/watch?v=abc123",
		SourceVideoID: "abc123",
		Category:      "fight",
		Duration:      10 * time.Second,
		DriveFolderID: "folder-id",
		DrivePath:     "Mike Tyson",
	}
	a := fromExistingClip(clip)
	require.NotNil(t, a, "fromExistingClip must return non-nil for non-nil input")

	assert.Equal(t, asset.MediaTypeClip, a.MediaType,
		"fromExistingClip must set MediaType=clip so UpsertClipTx writes media_type correctly")
	assert.Equal(t, asset.StateActive, a.LifecycleState,
		"fromExistingClip must set LifecycleState=ACTIVE so UpsertClipTx writes lifecycle_state correctly")
	assert.Equal(t, clip.SourceURL, a.SourceURL,
		"fromExistingClip must preserve source_url for the canonical SQLite columns")
	assert.Equal(t, clip.Category, a.Category,
		"fromExistingClip must preserve category for the canonical SQLite columns")
	assert.Equal(t, clip.Duration, a.Duration,
		"fromExistingClip must preserve duration for duration_ms")
	assert.Equal(t, clip.SourceVideoID, a.GetMetadataString("source_video_id"),
		"fromExistingClip must preserve source_video_id in metadata")
	assert.Equal(t, clip.DriveFolderID, a.FolderID(),
		"fromExistingClip must preserve drive folder id")
	assert.Equal(t, clip.DrivePath, a.FolderPath(),
		"fromExistingClip must preserve drive folder path")
}

// TestFromExistingClip_RoundTrip_RichMetadata verifies the rich metadata
// fields survive the fromExistingClip → toExistingClip round-trip.
func TestFromExistingClip_RoundTrip_RichMetadata(t *testing.T) {
	original := &sourcing.ExistingClip{
		ID:              "yt_xyz_0_10_v1",
		Name:            "Round Trip Clip",
		Source:          "youtube",
		Summary:         "A summary",
		Topics:          []string{"boxing", "sports"},
		Speakers:        []string{"Joe"},
		MentionedPeople: []string{"Mike"},
		Hook:            "The hook!",
	}
	assetNode := fromExistingClip(original)
	require.NotNil(t, assetNode)

	roundTripped := toExistingClip(assetNode)
	require.NotNil(t, roundTripped)

	assert.Equal(t, original.Summary, roundTripped.Summary)
	assert.Equal(t, original.Topics, roundTripped.Topics)
	assert.Equal(t, original.Speakers, roundTripped.Speakers)
	assert.Equal(t, original.MentionedPeople, roundTripped.MentionedPeople)
	assert.Equal(t, original.Hook, roundTripped.Hook)
}

// Compile-time assertion (Wave 16 follow-up, June 2026): *stubAssetRepo
// statically satisfies detail.Repository. AGENTS.md Pattern 0 doctrine
// applied at the test-stub home: if detail.Repository evolves in a future
// wave (e.g. a new method), this assertion will fail at test compile
// time, forcing the stub to grow BEFORE the next vet run unblocks —
// preventing the rot that the FindByExternalRef addition above fixes
// from recurring. Mirrors the production-side precedents at
// internal/platform/qdrant/search_adapter.go and
// internal/platform/sqlite/assets/clips_repository.go.
var _ detail.Repository = (*stubAssetRepo)(nil)
