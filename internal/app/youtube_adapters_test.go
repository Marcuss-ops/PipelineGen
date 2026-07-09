package app

import (
	"context"
	"testing"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/sourcing"
	youtubetypes "github.com/Marcuss-ops/PipelineGen/internal/application/youtube/dto"
	youtubeports "github.com/Marcuss-ops/PipelineGen/internal/application/youtube/ports"
	youtubeapp "github.com/Marcuss-ops/PipelineGen/internal/application/youtube/usecase"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	"github.com/Marcuss-ops/PipelineGen/pkg/portutil"

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

	deps := youtubeapp.ServiceDeps{
		SearchRunner:  runner,
		AssetRepo:     &stubAssetRepo{},
		VideoPipeline: &stubVideoPipeline{},
	}
	// MediaProcessor is intentionally missing (nil).
	err := youtubeapp.ValidateServiceDeps(deps)
	require.Error(t, err, "ValidateServiceDeps should reject typed-nil SearchRunner")
	assert.True(t, portutil.IsNilPort(runner), "precondition: runner should be typed-nil")
	assert.True(t, runner != nil, "precondition: typed-nil runner should != nil (Go semantics)")
}

// TestYouTubeComposition_ValidDepsBuildSuccessfully verifies that a fully
// wired ServiceDeps passes validation and can construct a Service.
func TestYouTubeComposition_ValidDepsBuildSuccessfully(t *testing.T) {
	deps := youtubeapp.ServiceDeps{
		Cfg:            youtubetypes.RuntimeConfig{},
		Log:            zap.NewNop(),
		SearchRunner:   &stubSearchRunner{},
		AssetRepo:      &stubAssetRepo{},
		VideoPipeline:  &stubVideoPipeline{},
		MediaProcessor: &stubMediaProcessor{},
	}

	err := youtubeapp.ValidateServiceDeps(deps)
	require.NoError(t, err, "fully wired deps should pass validation")

	svc := youtubeapp.NewService(deps)
	require.NotNil(t, svc, "NewService should return non-nil with valid deps")

	// Smoke: optional port methods should not panic.
	err = svc.IndexClip(context.Background(), "test")
	assert.NoError(t, err, "IndexClip should no-op when no indexer wired")

	transcript, err := svc.TranscribeAudio(context.Background(), "/tmp/test.wav")
	assert.NoError(t, err, "TranscribeAudio should no-op when no whisper wired")
	assert.Empty(t, transcript)
}

// TestYouTubeComposition_AllRequiredDepsRejectsNil validates that every
// required port is checked individually.
func TestYouTubeComposition_AllRequiredDepsRejectsNil(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*youtubeapp.ServiceDeps)
		wantMsg string
	}{
		{
			name:    "nil SearchRunner",
			mutate:  func(d *youtubeapp.ServiceDeps) { d.SearchRunner = nil },
			wantMsg: "SearchRunner",
		},
		{
			name:    "nil AssetRepo",
			mutate:  func(d *youtubeapp.ServiceDeps) { d.AssetRepo = nil },
			wantMsg: "AssetRepo",
		},
		{
			name:    "nil VideoPipeline",
			mutate:  func(d *youtubeapp.ServiceDeps) { d.VideoPipeline = nil },
			wantMsg: "VideoPipeline",
		},
		{
			name:    "nil MediaProcessor",
			mutate:  func(d *youtubeapp.ServiceDeps) { d.MediaProcessor = nil },
			wantMsg: "MediaProcessor",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			deps := youtubeapp.ServiceDeps{
				SearchRunner:   &stubSearchRunner{},
				AssetRepo:      &stubAssetRepo{},
				VideoPipeline:  &stubVideoPipeline{},
				MediaProcessor: &stubMediaProcessor{},
			}
			tc.mutate(&deps)
			err := youtubeapp.ValidateServiceDeps(deps)
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

func (s *stubMediaProcessor) Process(ctx context.Context, input *asset.ProcessInput) (*asset.ProcessResult, error) {
	return &asset.ProcessResult{Status: "ok"}, nil
}

// ── fromExistingClip mapper tests ───────────────────────────────────────

// TestFromExistingClip_SetsMediaTypeAndLifecycleState regression-guards the
// YouTube registration path: without MediaType and LifecycleState, UpsertClipTx
// writes empty strings to media_assets.media_type and lifecycle_state columns.
// This is the canonical 2-line bug that this test prevents from recurring.
func TestFromExistingClip_SetsMediaTypeAndLifecycleState(t *testing.T) {
	clip := &sourcing.ExistingClip{
		ID:       "yt_abc123_0_10_v1",
		Name:     "Test Clip",
		Filename: "test-clip.mp4",
		Source:   "youtube",
		Duration: 10 * time.Second,
	}
	a := fromExistingClip(clip)
	require.NotNil(t, a, "fromExistingClip must return non-nil for non-nil input")

	assert.Equal(t, asset.MediaTypeClip, a.MediaType,
		"fromExistingClip must set MediaType=clip so UpsertClipTx writes media_type correctly")
	assert.Equal(t, asset.StateActive, a.LifecycleState,
		"fromExistingClip must set LifecycleState=ACTIVE so UpsertClipTx writes lifecycle_state correctly")
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
// statically satisfies asset.Repository. AGENTS.md Pattern 0 doctrine
// applied at the test-stub home: if asset.Repository evolves in a future
// wave (e.g. a new method), this assertion will fail at test compile
// time, forcing the stub to grow BEFORE the next vet run unblocks —
// preventing the rot that the FindByExternalRef addition above fixes
// from recurring. Mirrors the production-side precedents at
// internal/infrastructure/qdrant/search_adapter.go and
// internal/infrastructure/database/sqlite/assets/clips_repository.go.
var _ asset.Repository = (*stubAssetRepo)(nil)
