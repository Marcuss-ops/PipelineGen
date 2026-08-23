package adapters

import (
	"context"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/media"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

type timingProbe struct {
	providers []string
}

func (*timingProbe) IncSegments()                             {}
func (*timingProbe) IncExtractionCache(bool)                  {}
func (*timingProbe) IncAssetCache(string, bool)               {}
func (*timingProbe) IncProviderRequest(string)                {}
func (*timingProbe) IncProviderFailure(string)                {}
func (*timingProbe) IncBinding()                              {}
func (*timingProbe) IncUnresolvedSegment()                    {}
func (*timingProbe) ObserveProcessorDuration(string, float64) {}
func (p *timingProbe) ObserveProviderDuration(provider string, _ float64) {
	p.providers = append(p.providers, provider)
}

type timingArtlistSearcher struct{}

func (timingArtlistSearcher) SearchClips(context.Context, string, []string) ([]ArtlistClipMatch, error) {
	return []ArtlistClipMatch{{
		Phrase:         "timing query",
		Remote:         true,
		ClipNames:      []string{"timing-clip"},
		ClipDriveLinks: []string{"https://cdn.artlist.example/timing.mp4"},
	}}, nil
}

func TestClipSearchRecordsProviderTiming(t *testing.T) {
	probe := &timingProbe{}
	processor := NewClipSearchProcessor(timingArtlistSearcher{}, probe)
	plan := &scriptpkg.ResolvedGenerationPlan{MediaPlan: media.MediaPlanSpec{
		ProviderPolicy: media.MediaProviderPolicy{
			Artlist:        media.MediaToggleEnabled,
			InternetImages: media.MediaToggleEnabled,
		},
	}}
	_, err := processor.Process(context.Background(), plan, ProcessInput{VidRushSegments: []scriptpkg.VidRushSegmentResult{{
		SegmentID: "timing-segment",
		Insights:  scriptpkg.SegmentInsights{ArtlistQueries: []string{"timing query"}},
	}}})
	if err != nil {
		t.Fatalf("process: %v", err)
	}
	if len(probe.providers) != 1 || probe.providers[0] != "artlist_search" {
		t.Fatalf("provider timing observations = %#v, want [artlist_search]", probe.providers)
	}
}

type timingImageSearcher struct{}

func (timingImageSearcher) SearchImages(_ context.Context, req InternetImageSearchRequest) ([]scriptpkg.SegmentAssetCandidate, error) {
	return []scriptpkg.SegmentAssetCandidate{{
		AssetID:   "timing-image",
		Provider:  scriptpkg.VidRushProviderInternetImages,
		Query:     req.Query,
		SourceURL: "https://images.example/timing.jpg",
	}}, nil
}

func TestInternetImageSearchRecordsProviderTiming(t *testing.T) {
	probe := &timingProbe{}
	processor := NewInternetImagesProcessor(timingImageSearcher{}, probe)
	plan := &scriptpkg.ResolvedGenerationPlan{MediaPlan: media.MediaPlanSpec{
		ProviderPolicy: media.MediaProviderPolicy{InternetImages: media.MediaToggleEnabled},
	}}
	_, err := processor.Process(context.Background(), plan, ProcessInput{VidRushSegments: []scriptpkg.VidRushSegmentResult{{
		SegmentID: "timing-image-segment",
		TextHash:  "timing-image-hash",
		Insights:  scriptpkg.SegmentInsights{ImageQueries: []string{"timing image query"}},
	}}})
	if err != nil {
		t.Fatalf("process: %v", err)
	}
	if len(probe.providers) != 1 || probe.providers[0] != "internet_images_search" {
		t.Fatalf("provider timing observations = %#v, want [internet_images_search]", probe.providers)
	}
}
