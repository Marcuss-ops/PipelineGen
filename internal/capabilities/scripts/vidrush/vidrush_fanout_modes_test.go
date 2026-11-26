package vidrush

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
			Entities:     []scriptpkg.ExtractedEntity{{Value: "Alberto Stasi", Type: "PERSON", SearchIntents: []string{"Alberto Stasi"}}},
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

func TestExplicitOnlyKeepsIdentityPortraitsAlongsideContextPins(t *testing.T) {
	plan := imageModePlan(mediadomain.ImageSearchModeExplicitOnly)
	plan.MediaPlan.Searches = []mediadomain.SegmentMediaSearch{
		{SegmentID: "scene-1", Slot: mediadomain.SlotSecondaryImage, Query: "Garlasco piazza"},
	}
	fanout := buildVidRushFanoutPlan(plan, imageModeSegment(), &gatedImageSearcher{}, nil)
	want := []string{"Alberto Stasi portrait", "Garlasco piazza"}
	if len(fanout.imageQueries) != len(want) {
		t.Fatalf("explicit_only queries = %#v, want %#v", fanout.imageQueries, want)
	}
	for i := range want {
		if fanout.imageQueries[i] != want[i] {
			t.Fatalf("explicit_only queries = %#v, want %#v", fanout.imageQueries, want)
		}
	}
	if !fanout.imagesEnabled {
		t.Fatal("explicit_only with entity_images per_scene must enable image retrieval")
	}
}

func TestImageSearchModeAutomaticKeepsIdentityAndContextLanes(t *testing.T) {
	plan := imageModePlan(mediadomain.ImageSearchModeAutomatic)
	plan.MediaPlan.Extraction.MaxImageQueriesPerSegment = 10
	fanout := buildVidRushFanoutPlan(plan, imageModeSegment(), &gatedImageSearcher{}, nil)
	// The identity lane is the dedicated portrait query, not the bare name:
	// bare names let collages/news cards bind as portraits. The caller's
	// contextual pin stays on its own lane.
	hasPerson, hasContext := false, false
	for _, query := range fanout.imageQueries {
		if query == "Alberto Stasi portrait" {
			hasPerson = true
		}
		if query == "Garlasco piazza" {
			hasContext = true
		}
	}
	if !hasPerson || !hasContext {
		t.Fatalf("automatic queries = %#v, want both the identity portrait query and the caller context search", fanout.imageQueries)
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
	// Explicit pins cannot suppress the identity lane; when the pin already is
	// the portrait query the two dedupe into one entry, but it must lead.
	if len(fanout.imageQueries) < 2 || fanout.imageQueries[0] != "Alberto Stasi portrait" {
		t.Fatalf("explicit_then_fallback queries = %#v, want the identity portrait query first plus caller context", fanout.imageQueries)
	}
	foundContext := false
	for _, query := range fanout.imageQueries {
		if query == "Garlasco piazza" {
			foundContext = true
		}
	}
	if !foundContext {
		t.Fatalf("explicit_then_fallback queries = %#v, want the explicit context pin retained", fanout.imageQueries)
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
