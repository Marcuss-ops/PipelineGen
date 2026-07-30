package adapters

import (
	"context"
	"fmt"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/domain/media"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
)

type emptyInternetImageSearcher struct {
	calls int
}

func (s *emptyInternetImageSearcher) SearchImages(context.Context, InternetImageSearchRequest) ([]scriptpkg.SegmentAssetCandidate, error) {
	s.calls++
	return nil, nil
}

func TestInternetImagesProcessorDoesNotCacheProviderMisses(t *testing.T) {
	searcher := &emptyInternetImageSearcher{}
	processor := NewInternetImagesProcessor(searcher)
	plan := &scriptpkg.ResolvedGenerationPlan{
		MediaPlan: media.MediaPlanSpec{
			ProviderPolicy: media.MediaProviderPolicy{InternetImages: media.MediaToggleEnabled},
		},
	}
	input := ProcessInput{VidRushSegments: []scriptpkg.VidRushSegmentResult{{
		SegmentID: "negative-cache-images-test",
		TextHash:  "negative-cache-images-hash",
		Insights: scriptpkg.SegmentInsights{
			ImageQueries: []string{"no result query"},
		},
	}}}

	for i := 0; i < 2; i++ {
		if _, err := processor.Process(context.Background(), plan, input); err != nil {
			t.Fatalf("process call %d failed: %v", i+1, err)
		}
	}
	if searcher.calls != 2 {
		t.Fatalf("expected provider to be retried after an empty result, calls = %d", searcher.calls)
	}
}

type multipleInternetImageSearcher struct{}

func (multipleInternetImageSearcher) SearchImages(_ context.Context, req InternetImageSearchRequest) ([]scriptpkg.SegmentAssetCandidate, error) {
	out := make([]scriptpkg.SegmentAssetCandidate, 0, 3)
	for i := 0; i < 3; i++ {
		url := fmt.Sprintf("https://images.example/%s/%d", req.Query, i)
		out = append(out, scriptpkg.SegmentAssetCandidate{
			AssetID:   fmt.Sprintf("%s-%d", req.Query, i),
			Provider:  "internet_images",
			SourceURL: url,
		})
	}
	return out, nil
}

func TestInternetImagesProcessorRetainsResultsAcrossAllQueries(t *testing.T) {
	processor := NewInternetImagesProcessor(multipleInternetImageSearcher{})
	plan := &scriptpkg.ResolvedGenerationPlan{
		MediaPlan: media.MediaPlanSpec{
			ProviderPolicy: media.MediaProviderPolicy{InternetImages: media.MediaToggleEnabled},
		},
	}
	input := ProcessInput{VidRushSegments: []scriptpkg.VidRushSegmentResult{{
		SegmentID: "full-images-test",
		TextHash:  "full-images-hash",
		Insights: scriptpkg.SegmentInsights{
			ImageQueries: []string{"query-a", "query-b"},
		},
	}}}

	result, err := processor.Process(context.Background(), plan, input)
	if err != nil {
		t.Fatalf("process failed: %v", err)
	}
	if len(result.VidRushSegments) != 1 {
		t.Fatalf("expected one segment, got %d", len(result.VidRushSegments))
	}
	if got := len(result.VidRushSegments[0].Assets.SecondaryImages); got != 6 {
		t.Fatalf("expected all six image results, got %d", got)
	}
}

// rogueInternetImageSearcher returns a mix of valid and forbidden providers,
// simulating a misconfigured or compromised searcher that leaks YouTube results.
type rogueInternetImageSearcher struct{}

func (rogueInternetImageSearcher) SearchImages(_ context.Context, req InternetImageSearchRequest) ([]scriptpkg.SegmentAssetCandidate, error) {
	return []scriptpkg.SegmentAssetCandidate{
		{AssetID: "good-1", Provider: "internet_images", SourceURL: "https://images.example/maya/1.jpg"},
		{AssetID: "bad-yt", Provider: "youtube", SourceURL: "https://youtube.com/watch?v=leaked"},
		{AssetID: "bad-gen", Provider: "generated_images", SourceURL: "https://ai.example/gen.png"},
		{AssetID: "good-2", Provider: "", SourceURL: "https://images.example/maya/2.jpg"},
		{AssetID: "bad-yt2", Provider: "YOUTUBE", SourceURL: "https://youtu.be/leaked2"},
		{AssetID: "good-3", Provider: "internet_images", SourceURL: "https://images.example/maya/3.jpg"},
	}, nil
}

// TestInternetImagesProcessor_RejectsNonInternetImagesProviders verifies
// the processor-level YouTube-block contract: when a searcher returns
// candidates with provider="youtube" (or any non-internet_images provider),
// the processor MUST filter them out at ingest time. Only candidates with
// provider="internet_images" (or empty, which gets defaulted) survive.
func TestInternetImagesProcessor_RejectsNonInternetImagesProviders(t *testing.T) {
	processor := NewInternetImagesProcessor(rogueInternetImageSearcher{})
	plan := &scriptpkg.ResolvedGenerationPlan{
		MediaPlan: media.MediaPlanSpec{
			ProviderPolicy: media.MediaProviderPolicy{InternetImages: media.MediaToggleEnabled},
		},
	}
	input := ProcessInput{VidRushSegments: []scriptpkg.VidRushSegmentResult{{
		SegmentID: "provider-gate-test",
		TextHash:  "provider-gate-hash",
		Insights: scriptpkg.SegmentInsights{
			ImageQueries: []string{"maya ruins"},
		},
	}}}

	result, err := processor.Process(context.Background(), plan, input)
	if err != nil {
		t.Fatalf("process failed: %v", err)
	}
	if len(result.VidRushSegments) != 1 {
		t.Fatalf("expected 1 segment, got %d", len(result.VidRushSegments))
	}
	candidates := result.VidRushSegments[0].Assets.Candidates
	if len(candidates) != 3 {
		t.Fatalf("expected exactly 3 valid candidates (good-1, good-2, good-3), got %d: %+v",
			len(candidates), candidates)
	}
	for _, c := range candidates {
		if c.Provider != "internet_images" {
			t.Errorf("candidate %q has provider=%q, want \"internet_images\" (forbidden providers must be filtered)",
				c.AssetID, c.Provider)
		}
		if c.Provider == "youtube" {
			t.Errorf("candidate %q has provider=youtube — FORBIDDEN, must be rejected at processor level", c.AssetID)
		}
	}
}
