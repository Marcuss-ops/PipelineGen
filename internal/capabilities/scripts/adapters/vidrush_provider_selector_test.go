package adapters

import (
	"testing"

	mediadomain "github.com/Marcuss-ops/PipelineGen/internal/kernel/media"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

func TestVidRushProviderSelectorExistingLockedAssetWins(t *testing.T) {
	selector := NewVidRushProviderSelector()
	plan := &scriptpkg.ResolvedGenerationPlan{MediaPlan: mediadomain.MediaPlanSpec{
		ProviderPolicy: mediadomain.MediaProviderPolicy{YouTube: mediadomain.MediaToggleEnabled},
		Assignments: []mediadomain.SegmentMediaAssignment{{
			SegmentID: "segment-1", Slot: mediadomain.SlotPrimaryVideo, Locked: true,
			Asset: mediadomain.MediaRef{AssetID: "asset-existing", Provider: scriptpkg.VidRushProviderArtlist},
		}},
	}}
	got, err := selector.Select(plan, scriptpkg.VidRushSegmentResult{SegmentID: "segment-1"}, "video")
	if err != nil {
		t.Fatal(err)
	}
	if got.Selected != scriptpkg.VidRushProviderArtlist || got.UsedAssetID != "asset-existing" {
		t.Fatalf("selection = %+v", got)
	}
}

func TestVidRushProviderSelectorHistoricalEntityPrefersYouTube(t *testing.T) {
	selector := NewVidRushProviderSelector()
	plan := &scriptpkg.ResolvedGenerationPlan{MediaPlan: mediadomain.MediaPlanSpec{
		ProviderPolicy: mediadomain.MediaProviderPolicy{
			YouTube: mediadomain.MediaToggleEnabled, Artlist: mediadomain.MediaToggleEnabled,
		},
	}}
	segment := scriptpkg.VidRushSegmentResult{SegmentID: "segment-2", Text: "John Froelich built a tractor in 1892.", Insights: scriptpkg.SegmentInsights{
		Entities:       []scriptpkg.ExtractedEntity{{Value: "John Froelich", Type: "PERSON"}, {Value: "1892", Type: "DATE"}},
		ArtlistQueries: []string{"historic tractor"},
	}}
	got, err := selector.Select(plan, segment, "video")
	if err != nil {
		t.Fatal(err)
	}
	if got.Selected != scriptpkg.VidRushProviderYouTube {
		t.Fatalf("selected = %q, want youtube; preferences=%+v", got.Selected, got.Preferences)
	}
}

func TestVidRushProviderSelectorImageContentPrefersInternetImages(t *testing.T) {
	selector := NewVidRushProviderSelector()
	plan := &scriptpkg.ResolvedGenerationPlan{MediaPlan: mediadomain.MediaPlanSpec{
		ProviderPolicy: mediadomain.MediaProviderPolicy{
			InternetImages: mediadomain.MediaToggleEnabled, Artlist: mediadomain.MediaToggleEnabled,
		},
	}}
	got, err := selector.Select(plan, scriptpkg.VidRushSegmentResult{SegmentID: "segment-3", Insights: scriptpkg.SegmentInsights{
		Entities:     []scriptpkg.ExtractedEntity{{Value: "Ada Lovelace", Type: "PERSON"}},
		ImageQueries: []string{"Ada Lovelace portrait"},
	}}, "image")
	if err != nil {
		t.Fatal(err)
	}
	if got.Selected != scriptpkg.VidRushProviderInternetImages {
		t.Fatalf("selected = %q, want internet_images; preferences=%+v", got.Selected, got.Preferences)
	}
}

func TestVidRushProviderSelectorReturnsStableTieOrder(t *testing.T) {
	selector := NewVidRushProviderSelector()
	plan := &scriptpkg.ResolvedGenerationPlan{MediaPlan: mediadomain.MediaPlanSpec{
		ProviderPolicy: mediadomain.MediaProviderPolicy{Artlist: mediadomain.MediaToggleEnabled, ImageGeneration: mediadomain.MediaToggleEnabled},
	}}
	segment := scriptpkg.VidRushSegmentResult{SegmentID: "segment-4", Text: "An abstract idea."}
	first, err := selector.Select(plan, segment, "")
	if err != nil {
		t.Fatal(err)
	}
	second, err := selector.Select(plan, segment, "")
	if err != nil {
		t.Fatal(err)
	}
	if first.Selected != second.Selected || first.Selected == "" {
		t.Fatalf("unstable selection: first=%+v second=%+v", first, second)
	}
}

func TestVidRushProviderSelectorFailsWhenAllProvidersDisabled(t *testing.T) {
	selector := NewVidRushProviderSelector()
	_, err := selector.Select(&scriptpkg.ResolvedGenerationPlan{}, scriptpkg.VidRushSegmentResult{SegmentID: "segment-5"}, "video")
	if err == nil {
		t.Fatal("expected no-provider error")
	}
}
