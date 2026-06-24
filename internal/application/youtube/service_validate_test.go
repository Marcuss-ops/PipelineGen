package youtube

import (
	"context"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	youtubeports "github.com/Marcuss-ops/PipelineGen/internal/application/youtube/ports"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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

// validDeps returns a ServiceDeps with all required ports wired (non-nil).
func validDeps() ServiceDeps {
	return ServiceDeps{
		SearchRunner:    &fakeSearchRunner{},
		AssetRepo:       &fakeAssetRepo{},
		VideoPipeline:   &fakeVideoPipeline{},
		MediaProcessor:  &fakeMediaProcessor{},
	}
}

// ── Required-port rejection tests ───────────────────────────────────────

func TestValidateServiceDeps_RejectsTypedNilSearchRunner(t *testing.T) {
	var nilRunner *fakeSearchRunner
	var runner youtubeports.SearchRunnerPort = nilRunner // typed-nil

	deps := validDeps()
	deps.SearchRunner = runner
	err := ValidateServiceDeps(deps)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "SearchRunner")
}

func TestValidateServiceDeps_RejectsTypedNilAssetRepo(t *testing.T) {
	var nilRepo *fakeAssetRepo
	var repo asset.Repository = nilRepo // typed-nil

	deps := validDeps()
	deps.AssetRepo = repo
	err := ValidateServiceDeps(deps)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "AssetRepo")
}

func TestValidateServiceDeps_RejectsTypedNilVideoPipeline(t *testing.T) {
	var nilPipeline *fakeVideoPipeline
	var pipeline youtubeports.VideoPipelinePort = nilPipeline // typed-nil

	deps := validDeps()
	deps.VideoPipeline = pipeline
	err := ValidateServiceDeps(deps)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "VideoPipeline")
}

func TestValidateServiceDeps_RejectsTypedNilMediaProcessor(t *testing.T) {
	var nilProc *fakeMediaProcessor
	var proc asset.Processor = nilProc // typed-nil

	deps := validDeps()
	deps.MediaProcessor = proc
	err := ValidateServiceDeps(deps)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "MediaProcessor")
}

func TestValidateServiceDeps_RejectsBareNilSearchRunner(t *testing.T) {
	deps := validDeps()
	deps.SearchRunner = nil
	err := ValidateServiceDeps(deps)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "SearchRunner")
}

func TestValidateServiceDeps_RejectsBareNilAssetRepo(t *testing.T) {
	deps := validDeps()
	deps.AssetRepo = nil
	err := ValidateServiceDeps(deps)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "AssetRepo")
}

// ── Optional-port allowance tests ───────────────────────────────────────

func TestValidateServiceDeps_AllowsOptionalTypedNilPorts(t *testing.T) {
	// All required ports are valid, optional ports can be nil or typed-nil.
	deps := validDeps()

	// Optional ports intentionally left zero/nil — should pass.
	deps.Indexer = nil
	deps.Whisper = nil
	deps.Ollama = nil
	deps.HashSvc = nil
	deps.DriveFolderMgr = nil
	deps.SubtitleFetcher = nil
	deps.ClipFiles = nil
	deps.MetaFetcher = nil
	deps.TempFiles = nil
	deps.Clips = nil
	deps.Monitors = nil
	deps.CacheStore = nil
	deps.FolderMemory = nil

	err := ValidateServiceDeps(deps)
	assert.NoError(t, err)
}

func TestValidateServiceDeps_AllowsOptionalBareNilPorts(t *testing.T) {
	deps := validDeps()
	// All optional ports left as zero values (nil) — should pass.
	err := ValidateServiceDeps(deps)
	assert.NoError(t, err)
}

func TestValidateServiceDeps_AllValidDepsPass(t *testing.T) {
	deps := validDeps()
	err := ValidateServiceDeps(deps)
	assert.NoError(t, err)
}

// ── Best-effort IndexClip / TranscribeAudio tests ───────────────────────

func TestIndexClip_NoOpWhenIndexerNotWired(t *testing.T) {
	svc := NewService(ServiceDeps{
		Indexer: nil, // no indexer
	})
	err := svc.IndexClip(context.Background(), "test-clip-id")
	assert.NoError(t, err, "IndexClip should return nil (no-op) when indexer is not wired")
}

func TestTranscribeAudio_EmptyWhenWhisperNotWired(t *testing.T) {
	svc := NewService(ServiceDeps{
		Whisper: nil, // no whisper
	})
	result, err := svc.TranscribeAudio(context.Background(), "/tmp/test.wav")
	assert.NoError(t, err)
	assert.Empty(t, result, "TranscribeAudio should return empty string when whisper is not wired")
}
