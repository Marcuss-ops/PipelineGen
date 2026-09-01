package adapters

import (
	"context"
	"testing"

	scriptports "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/ports"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

type youtubeFastPathProvider struct {
	acquireCalls int
}

func (p *youtubeFastPathProvider) Name() string { return scriptpkg.VidRushProviderYouTube }
func (p *youtubeFastPathProvider) Search(context.Context, scriptports.VidRushSearchRequest) ([]scriptpkg.SegmentAssetCandidate, error) {
	return nil, nil
}
func (p *youtubeFastPathProvider) Acquire(context.Context, scriptpkg.SegmentAssetCandidate) (scriptports.LocalArtifact, error) {
	p.acquireCalls++
	return scriptports.LocalArtifact{}, nil
}
func (p *youtubeFastPathProvider) Verify(context.Context, scriptports.LocalArtifact) (scriptports.VerifiedArtifact, error) {
	return scriptports.VerifiedArtifact{}, nil
}

type youtubeFastPathFinalizer struct{ calls int }

func (f *youtubeFastPathFinalizer) Finalize(context.Context, scriptports.VerifiedArtifact) (scriptpkg.SegmentAssetCandidate, error) {
	f.calls++
	return scriptpkg.SegmentAssetCandidate{}, nil
}

func TestYouTubeReadyCandidateUsesFastPath(t *testing.T) {
	provider := &youtubeFastPathProvider{}
	finalizer := &youtubeFastPathFinalizer{}
	registry := NewVidRushAssetProviderRegistry()
	if err := registry.Register(provider); err != nil {
		t.Fatal(err)
	}
	registry.Freeze()
	processor := newTestMaterializationProcessor(registry, finalizer)
	plan := &scriptpkg.ResolvedGenerationPlan{}
	segment := scriptpkg.VidRushSegmentResult{
		SegmentID: "segment-youtube",
		Assets: scriptpkg.SegmentAssetSelection{Candidates: []scriptpkg.SegmentAssetCandidate{{
			AssetID: "asset-youtube-1", Provider: scriptpkg.VidRushProviderYouTube,
			SourceURL: "https://www.youtube.com/watch?v=video-1", SourceStartMs: 151000, SourceEndMs: 161000,
			DurationMs: 10000, DriveLink: "https://drive.google.com/file/d/asset-youtube-1",
			AcquisitionStatus: "acquired", VerificationStatus: "verified", PersistenceStatus: "persisted", IndexStatus: "indexed",
			RelevanceScore: .92, Score: .92,
		}}},
	}
	result, err := processor.Materialize(context.Background(), plan, segment)
	if err != nil {
		t.Fatal(err)
	}
	if provider.acquireCalls != 0 || finalizer.calls != 0 {
		t.Fatalf("YouTube fast path invoked generic lifecycle: acquire=%d finalize=%d", provider.acquireCalls, finalizer.calls)
	}
	if result.Assets.PrimaryVideo == nil || result.Assets.PrimaryVideo.AssetID != "asset-youtube-1" {
		t.Fatalf("YouTube candidate was not retained as primary: %+v", result.Assets.PrimaryVideo)
	}
}
