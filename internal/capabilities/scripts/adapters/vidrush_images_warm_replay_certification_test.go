package adapters

import (
	"context"
	"sync"
	"testing"

	mediadomain "github.com/Marcuss-ops/PipelineGen/internal/kernel/media"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

type warmReplayImageMetrics struct {
	mu            sync.Mutex
	assetHits     int
	assetMisses   int
	providerCalls int
}

func (*warmReplayImageMetrics) IncSegments()                             {}
func (*warmReplayImageMetrics) IncExtractionCache(bool)                  {}
func (*warmReplayImageMetrics) IncProviderFailure(string)                {}
func (*warmReplayImageMetrics) IncBinding()                              {}
func (*warmReplayImageMetrics) IncUnresolvedSegment()                    {}
func (*warmReplayImageMetrics) ObserveProcessorDuration(string, float64) {}
func (*warmReplayImageMetrics) ObserveProviderDuration(string, float64)  {}
func (m *warmReplayImageMetrics) IncAssetCache(_ string, hit bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if hit {
		m.assetHits++
	} else {
		m.assetMisses++
	}
}
func (m *warmReplayImageMetrics) IncProviderRequest(_ string) {
	m.mu.Lock()
	m.providerCalls++
	m.mu.Unlock()
}

func TestVidRushGoldenT6ColdWarmHasZeroNewSearchesDownloadsAndUploads(t *testing.T) {
	vidrushImageCache = sync.Map{}
	cache := newMemoryVidRushCache()
	searcher := &countingImageSearcher{}
	metrics := &warmReplayImageMetrics{}
	processor := NewInternetImagesProcessorWithCache(searcher, cache, metrics)
	plan := func(force bool) *scriptpkg.ResolvedGenerationPlan {
		return &scriptpkg.ResolvedGenerationPlan{
			Language: "it", Topic: "maya",
			MediaPlan: mediadomain.MediaPlanSpec{
				ProviderPolicy:     mediadomain.MediaProviderPolicy{InternetImages: mediadomain.MediaToggleEnabled},
				ForceRefreshAssets: force,
			},
		}
	}
	input := ProcessInput{VidRushSegments: []scriptpkg.VidRushSegmentResult{{
		SegmentID: "t6-segment", TextHash: "t6-hash",
		Insights: scriptpkg.SegmentInsights{ImageQueries: []string{"maya ruins"}},
	}}}

	cold, err := processor.Process(context.Background(), plan(true), input)
	if err != nil {
		t.Fatal(err)
	}
	if searcher.calls != 1 || cold.VidRushSegments[0].Cache.InternetImagesProviderSearches != 1 {
		t.Fatalf("cold provider/search counters = %d/%d, want 1/1", searcher.calls, cold.VidRushSegments[0].Cache.InternetImagesProviderSearches)
	}
	if cold.VidRushSegments[0].Cache.InternetImages != "REFRESHED" {
		t.Fatalf("cold cache state = %q, want REFRESHED", cold.VidRushSegments[0].Cache.InternetImages)
	}

	// The processor exposes provider searches and new Drive uploads directly;
	// the materialization layer is represented by the asset-cache miss/hit
	// metrics. This makes the zero-side-effect warm contract explicit.
	warm, err := processor.Process(context.Background(), plan(false), input)
	if err != nil {
		t.Fatal(err)
	}
	segment := warm.VidRushSegments[0]
	if segment.Cache.InternetImages != "HIT_EXACT" {
		t.Fatalf("warm cache state = %q, want HIT_EXACT", segment.Cache.InternetImages)
	}
	if segment.Cache.InternetImagesProviderSearches != 0 {
		t.Fatalf("warm provider searches = %d, want 0", segment.Cache.InternetImagesProviderSearches)
	}
	if segment.Cache.InternetImagesNewUploads != 0 {
		t.Fatalf("warm new uploads = %d, want 0", segment.Cache.InternetImagesNewUploads)
	}
	if searcher.calls != 1 {
		t.Fatalf("warm provider calls = %d, want unchanged at 1", searcher.calls)
	}
	if metrics.providerCalls != 1 || metrics.assetMisses != 1 || metrics.assetHits != 1 {
		t.Fatalf("side-effect metrics provider=%d misses=%d hits=%d, want 1/1/1", metrics.providerCalls, metrics.assetMisses, metrics.assetHits)
	}
	if len(segment.Assets.SecondaryImages) != len(cold.VidRushSegments[0].Assets.SecondaryImages) {
		t.Fatalf("warm asset count = %d, cold asset count = %d", len(segment.Assets.SecondaryImages), len(cold.VidRushSegments[0].Assets.SecondaryImages))
	}
}

func TestVidRushImagesWarmReplayPersistsAndReusesL2(t *testing.T) {
	vidrushImageCache = sync.Map{}
	cache := newMemoryVidRushCache()
	searcher := &countingImageSearcher{}
	metrics := &warmReplayImageMetrics{}
	processor := NewInternetImagesProcessorWithCache(searcher, cache, metrics)
	plan := func(force bool) *scriptpkg.ResolvedGenerationPlan {
		return &scriptpkg.ResolvedGenerationPlan{Language: "it", Topic: "maya", MediaPlan: mediadomain.MediaPlanSpec{
			ProviderPolicy:     mediadomain.MediaProviderPolicy{InternetImages: mediadomain.MediaToggleEnabled},
			ForceRefreshAssets: force,
		}}
	}
	input := ProcessInput{VidRushSegments: []scriptpkg.VidRushSegmentResult{{
		SegmentID: "warm-segment", TextHash: "warm-hash", Insights: scriptpkg.SegmentInsights{ImageQueries: []string{"maya ruins"}},
	}}}

	cold, err := processor.Process(context.Background(), plan(true), input)
	if err != nil {
		t.Fatal(err)
	}
	if searcher.calls != 1 || cold.VidRushSegments[0].Cache.InternetImagesProviderSearches != 1 {
		t.Fatalf("cold calls/searches=%d/%d, want 1/1", searcher.calls, cold.VidRushSegments[0].Cache.InternetImagesProviderSearches)
	}
	if cold.VidRushSegments[0].Cache.InternetImages != "REFRESHED" || len(cold.VidRushSegments[0].Assets.SecondaryImages) != 1 {
		t.Fatalf("cold result=%+v", cold.VidRushSegments[0])
	}

	warm, err := processor.Process(context.Background(), plan(false), input)
	if err != nil {
		t.Fatal(err)
	}
	if searcher.calls != 1 || warm.VidRushSegments[0].Cache.InternetImagesProviderSearches != 0 {
		t.Fatalf("warm calls/searches=%d/%d, want 1/0", searcher.calls, warm.VidRushSegments[0].Cache.InternetImagesProviderSearches)
	}
	if warm.VidRushSegments[0].Cache.InternetImages != "HIT_EXACT" || len(warm.VidRushSegments[0].Assets.SecondaryImages) != 1 {
		t.Fatalf("warm result=%+v", warm.VidRushSegments[0])
	}
	if metrics.providerCalls != 1 || metrics.assetMisses != 1 || metrics.assetHits != 1 {
		t.Fatalf("metrics provider=%d misses=%d hits=%d, want 1/1/1", metrics.providerCalls, metrics.assetMisses, metrics.assetHits)
	}
}
