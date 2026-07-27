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
