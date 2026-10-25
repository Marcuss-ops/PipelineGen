package adapters

import (
	"context"
	"errors"
	"testing"

	scriptports "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/ports"
	mediapkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/media"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

type youtubeFailureProvider struct{}

func (youtubeFailureProvider) Name() string { return scriptpkg.VidRushProviderYouTube }
func (youtubeFailureProvider) Search(context.Context, scriptports.VidRushSearchRequest) ([]scriptpkg.SegmentAssetCandidate, error) {
	return nil, errors.New("youtube source unavailable")
}
func (youtubeFailureProvider) Acquire(context.Context, scriptpkg.SegmentAssetCandidate) (scriptports.LocalArtifact, error) {
	return scriptports.LocalArtifact{}, errors.New("not expected")
}
func (youtubeFailureProvider) Verify(context.Context, scriptports.LocalArtifact) (scriptports.VerifiedArtifact, error) {
	return scriptports.VerifiedArtifact{}, errors.New("not expected")
}

type fallbackImagesSearcher struct{}

func (fallbackImagesSearcher) SearchImages(context.Context, InternetImageSearchRequest) ([]scriptpkg.SegmentAssetCandidate, error) {
	return nil, nil
}

func fallbackPlan(mode string) *scriptpkg.ResolvedGenerationPlan {
	return &scriptpkg.ResolvedGenerationPlan{
		Title: "WWII fallback", Language: "en", Model: "test", PromptVersion: "test", MediaPlan: mediapkg.MediaPlanSpec{
			ProviderPolicy: mediapkg.MediaProviderPolicy{YouTube: mediapkg.MediaToggleEnabled},
			Sources:        []mediapkg.SegmentMediaSource{{SegmentID: "segment-001", Provider: scriptpkg.VidRushProviderYouTube, SourceURL: "https://youtu.be/failing-video", Mode: mediapkg.SegmentMediaSourceMode(mode)}},
		},
	}
}

func fallbackSegment() scriptpkg.VidRushSegmentResult {
	return scriptpkg.VidRushSegmentResult{SegmentID: "segment-001", Text: "German invasion of Poland", Insights: scriptpkg.SegmentInsights{YouTubeQueries: []string{"historical footage"}}}
}

// A suggested YouTube failure is best-effort: the fan-out records no candidates
// but never turns the failed provider into a hard segment failure.
func TestYouTubeSuggestedFailureDoesNotFailClosed(t *testing.T) {
	fanout := NewVidRushProviderFanoutWithYouTube(fallbackImagesSearcher{}, youtubeFailureProvider{})
	got, err := fanout.ResolveProviders(context.Background(), fallbackPlan("suggested"), fallbackSegment())
	if err != nil {
		t.Fatal(err)
	}
	if got.Assets.PrimaryVideo != nil {
		t.Fatalf("provider fallback selected a primary: %+v", got.Assets.PrimaryVideo)
	}
	if len(got.Assets.Candidates) != 0 {
		t.Fatalf("discovered candidates = %+v, want none from a failed YouTube source", got.Assets.Candidates)
	}
}

func TestYouTubeRequiredFailureFailsClosed(t *testing.T) {
	fanout := NewVidRushProviderFanoutWithYouTube(fallbackImagesSearcher{}, youtubeFailureProvider{})
	_, err := fanout.ResolveProviders(context.Background(), fallbackPlan("required"), fallbackSegment())
	if err == nil {
		t.Fatal("required YouTube failure must fail closed")
	}
}
