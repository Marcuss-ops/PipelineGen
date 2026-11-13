package adapters

import (
	"testing"

	mediadomain "github.com/Marcuss-ops/PipelineGen/internal/kernel/media"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

func imageModePlan(mode string) *scriptpkg.ResolvedGenerationPlan {
	return &scriptpkg.ResolvedGenerationPlan{
		Title: "image search modes",
		MediaPlan: mediadomain.MediaPlanSpec{
			Extraction: mediadomain.MediaExtractionPolicy{
				EntityImages:              mediadomain.EntityImagePolicy{Enabled: true, Scope: "per_scene"},
				MaxImageQueriesPerSegment: 1,
				ImageSearchMode:           mode,
			},
			ProviderPolicy: mediadomain.MediaProviderPolicy{InternetImages: mediadomain.MediaToggleEnabled},
			Searches: []mediadomain.SegmentMediaSearch{
				{SegmentID: "scene-1", Slot: mediadomain.SlotSecondaryImage, Query: "Alberto Stasi portrait"},
				{SegmentID: "scene-1", Slot: mediadomain.SlotSecondaryImage, Query: "Garlasco piazza"},
			},
		},
	}
}

func imageModeSegment() scriptpkg.VidRushSegmentResult {
	return scriptpkg.VidRushSegmentResult{
		SegmentID: "scene-1", TextHash: "hash-1",
		Text: "Alberto Stasi arriva in tribunale a Garlasco con i giornalisti.",
		Insights: scriptpkg.SegmentInsights{
			Entities:     []scriptpkg.ExtractedEntity{{Value: "Alberto Stasi", Type: "PERSON"}},
			ImageQueries: []string{"Alberto Stasi", "Garlasco", "tribunale"},
		},
	}
}

func TestImageSearchModeExplicitOnlyIsExclusive(t *testing.T) {
	fanout := buildVidRushFanoutPlan(imageModePlan(mediadomain.ImageSearchModeExplicitOnly), imageModeSegment(), &gatedImageSearcher{}, nil)
	want := []string{"Alberto Stasi portrait", "Garlasco piazza"}
	if len(fanout.imageQueries) != len(want) {
		t.Fatalf("explicit_only queries = %#v, want only %#v", fanout.imageQueries, want)
	}
	for i := range want {
		if fanout.imageQueries[i] != want[i] {
			t.Fatalf("explicit_only queries = %#v, want %#v", fanout.imageQueries, want)
		}
	}
	if !fanout.imagesEnabled {
		t.Fatal("explicit_only with explicit pins must enable image retrieval")
	}
}

func TestImageSearchModeAutomaticKeepsIdentityAndContextLanes(t *testing.T) {
	plan := imageModePlan(mediadomain.ImageSearchModeAutomatic)
	plan.MediaPlan.Extraction.MaxImageQueriesPerSegment = 10
	fanout := buildVidRushFanoutPlan(plan, imageModeSegment(), &gatedImageSearcher{}, nil)
	hasPerson, hasContext := false, false
	for _, query := range fanout.imageQueries {
		if query == "Alberto Stasi" {
			hasPerson = true
		}
		if query == "Alberto Stasi portrait" || query == "Garlasco piazza" {
			hasContext = true
		}
	}
	if !hasPerson || !hasContext {
		t.Fatalf("automatic queries = %#v, want both identity and caller context searches", fanout.imageQueries)
	}
	if !fanout.imagesEnabled {
		t.Fatal("automatic mode with derived queries must enable image retrieval")
	}
}

func TestImageSearchModeExplicitThenFallbackUsesAutomaticOnlyWhenEmpty(t *testing.T) {
	plan := imageModePlan(mediadomain.ImageSearchModeExplicitThenFallback)
	plan.MediaPlan.Searches = nil
	fanout := buildVidRushFanoutPlan(plan, imageModeSegment(), &gatedImageSearcher{}, nil)
	if len(fanout.imageQueries) == 0 {
		t.Fatal("explicit_then_fallback with no pins must fall back to derived queries")
	}
}

func TestImageSearchModeExplicitThenFallbackDoesNotSuppressIdentitySearch(t *testing.T) {
	plan := imageModePlan(mediadomain.ImageSearchModeExplicitThenFallback)
	plan.MediaPlan.Extraction.MaxImageQueriesPerSegment = 10
	fanout := buildVidRushFanoutPlan(plan, imageModeSegment(), &gatedImageSearcher{}, nil)
	if len(fanout.imageQueries) < 3 || fanout.imageQueries[0] != "Alberto Stasi" {
		t.Fatalf("explicit_then_fallback queries = %#v, want identity first plus context", fanout.imageQueries)
	}
}

func TestImageSearchModeDisabledRetrievesNothing(t *testing.T) {
	fanout := buildVidRushFanoutPlan(imageModePlan(mediadomain.ImageSearchModeDisabled), imageModeSegment(), &gatedImageSearcher{}, nil)
	if len(fanout.imageQueries) != 0 {
		t.Fatalf("disabled image search queries = %#v, want none", fanout.imageQueries)
	}
	if fanout.imagesEnabled {
		t.Fatal("disabled image search must not enable the image provider")
	}
}
