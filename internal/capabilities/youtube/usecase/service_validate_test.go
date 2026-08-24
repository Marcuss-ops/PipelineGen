package usecase

import (
	"context"
	"testing"

	youtubeports "github.com/Marcuss-ops/PipelineGen/internal/capabilities/youtube/ports"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// ── Fake implementations for typed-nil testing ──────────────────────────

// fakeSearchRunner satisfies youtubeports.SearchRunnerPort.
type fakeSearchRunner struct{}

func (f *fakeSearchRunner) SearchLive(ctx context.Context, query string, limit int, sort string) ([]youtubeports.SearchLiveResult, error) {
	return nil, nil
}
func (f *fakeSearchRunner) GetVideoInfo(ctx context.Context, videoURL string) (*youtubeports.DownloaderMetadata, error) {
	return nil, nil
}

// fakeAssetRepo satisfies asset.Repository.
type fakeAssetRepo struct{}

func (f *fakeAssetRepo) Upsert(ctx context.Context, a *asset.Asset) error { return nil }
func (f *fakeAssetRepo) Get(ctx context.Context, id string) (*asset.Asset, error) {
	return nil, nil
}
func (f *fakeAssetRepo) List(ctx context.Context, filter asset.Filter) ([]*asset.Asset, error) {
	return nil, nil
}
func (f *fakeAssetRepo) Count(ctx context.Context, filter asset.Filter) (int64, error) {
	return 0, nil
}
func (f *fakeAssetRepo) SoftDelete(ctx context.Context, id string) error { return nil }
func (f *fakeAssetRepo) Restore(ctx context.Context, id string) error    { return nil }
func (f *fakeAssetRepo) HardDelete(ctx context.Context, id string) error { return nil }
func (f *fakeAssetRepo) FindByExternalRef(ctx context.Context, provider, externalID string) (*asset.Asset, error) {
	return nil, nil
}

// validSubBundles returns the 5 capability-area sub-bundles with all
// required ports wired. PR-GRUPOC-1 (July 2026): replaces the pre-PR
// `validDeps() ServiceDeps` helper. Tests that exercise a typed-nil
// guard mutate the relevant sub-bundle (e.g. `adapter.SearchRunner = nil`)
// after calling this helper.
func validSubBundles() (ServiceCoreDeps, ServiceAssetDeps, ServiceVideoDeps, ServiceStorageDeps, ServiceAdapterDeps) {
	return ServiceCoreDeps{},
		ServiceAssetDeps{
			AssetRepo:      &fakeAssetRepo{},
			MediaProcessor: &fakeMediaProcessor{},
		},
		ServiceVideoDeps{VideoPipeline: &fakeVideoPipeline{}},
		ServiceStorageDeps{},
		ServiceAdapterDeps{SearchRunner: &fakeSearchRunner{}}
}

// ── Required-port rejection tests ───────────────────────────────────────

func TestValidateServiceDeps_RejectsTypedNilSearchRunner(t *testing.T) {
	var nilRunner *fakeSearchRunner
	var runner youtubeports.SearchRunnerPort = nilRunner // typed-nil

	core, asset, video, storage, adapter := validSubBundles()
	adapter.SearchRunner = runner
	err := ValidateServiceDepsFromSubBundles(core, asset, video, storage, adapter)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "SearchRunner")
}

func TestValidateServiceDeps_RejectsTypedNilAssetRepo(t *testing.T) {
	var nilRepo *fakeAssetRepo
	var repo asset.Repository = nilRepo // typed-nil

	core, asset, video, storage, adapter := validSubBundles()
	asset.AssetRepo = repo
	err := ValidateServiceDepsFromSubBundles(core, asset, video, storage, adapter)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "AssetRepo")
}

func TestValidateServiceDeps_RejectsTypedNilVideoPipeline(t *testing.T) {
	var nilPipeline *fakeVideoPipeline
	var pipeline youtubeports.VideoPipelinePort = nilPipeline // typed-nil

	core, asset, video, storage, adapter := validSubBundles()
	video.VideoPipeline = pipeline
	err := ValidateServiceDepsFromSubBundles(core, asset, video, storage, adapter)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "VideoPipeline")
}

func TestValidateServiceDeps_RejectsTypedNilMediaProcessor(t *testing.T) {
	var nilProc *fakeMediaProcessor
	var proc asset.Processor = nilProc // typed-nil

	core, asset, video, storage, adapter := validSubBundles()
	asset.MediaProcessor = proc
	err := ValidateServiceDepsFromSubBundles(core, asset, video, storage, adapter)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "MediaProcessor")
}

func TestValidateServiceDeps_RejectsBareNilSearchRunner(t *testing.T) {
	core, asset, video, storage, adapter := validSubBundles()
	adapter.SearchRunner = nil
	err := ValidateServiceDepsFromSubBundles(core, asset, video, storage, adapter)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "SearchRunner")
}

func TestValidateServiceDeps_RejectsBareNilAssetRepo(t *testing.T) {
	core, asset, video, storage, adapter := validSubBundles()
	asset.AssetRepo = nil
	err := ValidateServiceDepsFromSubBundles(core, asset, video, storage, adapter)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "AssetRepo")
}

// ── Optional-port allowance tests ───────────────────────────────────────

func TestValidateServiceDeps_AllowsOptionalTypedNilPorts(t *testing.T) {
	// All required ports are valid, optional ports can be nil or typed-nil.
	core, asset, video, storage, adapter := validSubBundles()

	// Optional ports intentionally left zero/nil — should pass.
	storage.Indexer = nil
	adapter.Whisper = nil
	storage.Ollama = nil
	adapter.HashSvc = nil
	adapter.DriveFolderMgr = nil
	adapter.SubtitleFetcher = nil
	adapter.ClipFiles = nil
	adapter.MetaFetcher = nil
	storage.Clips = nil
	storage.Monitors = nil
	storage.FolderMemory = nil

	err := ValidateServiceDepsFromSubBundles(core, asset, video, storage, adapter)
	assert.NoError(t, err)
}

func TestValidateServiceDeps_AllowsOptionalBareNilPorts(t *testing.T) {
	core, asset, video, storage, adapter := validSubBundles()
	// All optional ports left as zero values (nil) — should pass.
	err := ValidateServiceDepsFromSubBundles(core, asset, video, storage, adapter)
	assert.NoError(t, err)
}

func TestValidateServiceDeps_AllValidDepsPass(t *testing.T) {
	core, asset, video, storage, adapter := validSubBundles()
	err := ValidateServiceDepsFromSubBundles(core, asset, video, storage, adapter)
	assert.NoError(t, err)
}

// ── Best-effort IndexClip / TranscribeAudio tests ───────────────────────

func TestIndexClip_NoOpWhenIndexerNotWired(t *testing.T) {
	// PR-GRUPOC-1 (July 2026): sub-builder ctor. Indexer is in
	// ServiceStorageDeps; leaving it nil exercises the no-op path.
	svc := NewServiceFromSubBundles(
		ServiceCoreDeps{Log: zap.NewNop()},
		ServiceAssetDeps{},
		ServiceVideoDeps{
			ProcessSeg: newTestProcessSegmentUseCase(zap.NewNop(), &fakeVideoPipeline{}),
		},
		ServiceStorageDeps{},
		ServiceAdapterDeps{},
	)
	err := svc.IndexClip(context.Background(), "test-clip-id")
	assert.NoError(t, err, "IndexClip should return nil (no-op) when indexer is not wired")
}

func TestTranscribeAudio_EmptyWhenWhisperNotWired(t *testing.T) {
	// PR-GRUPOC-1 (July 2026): sub-builder ctor. Whisper is in
	// ServiceAdapterDeps; leaving it nil exercises the no-op path.
	svc := NewServiceFromSubBundles(
		ServiceCoreDeps{Log: zap.NewNop()},
		ServiceAssetDeps{},
		ServiceVideoDeps{
			ProcessSeg: newTestProcessSegmentUseCase(zap.NewNop(), &fakeVideoPipeline{}),
		},
		ServiceStorageDeps{},
		ServiceAdapterDeps{},
	)
	result, err := svc.TranscribeAudio(context.Background(), "/tmp/test.wav")
	assert.NoError(t, err)
	assert.Empty(t, result, "TranscribeAudio should return empty string when whisper is not wired")
}
