package adapters

import (
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/media"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// TestVidRushImageTargetHonorsPlanImagesPerScene pins the images_per_scene
// contract: an explicit plan cadence drives the per-scene target even above
// the historical default of two, the default applies only when an image
// provider is enabled but no cadence was requested, and a plan that asks for
// no images stays at zero.
func TestVidRushImageTargetHonorsPlanImagesPerScene(t *testing.T) {
	if got := vidRushImageTarget(&scriptpkg.ResolvedGenerationPlan{ImagesPerScene: 5}); got != 5 {
		t.Fatalf("explicit ImagesPerScene target = %d, want 5", got)
	}
	internetOnly := &scriptpkg.ResolvedGenerationPlan{MediaPlan: media.MediaPlanSpec{
		ProviderPolicy: media.MediaProviderPolicy{InternetImages: media.MediaToggleEnabled},
	}}
	if got := vidRushImageTarget(internetOnly); got != vidRushDefaultImagesPerScene {
		t.Fatalf("default image target = %d, want %d", got, vidRushDefaultImagesPerScene)
	}
	if got := vidRushImageTarget(nil); got != 0 {
		t.Fatalf("nil plan image target = %d, want 0", got)
	}
	if got := vidRushImageTarget(&scriptpkg.ResolvedGenerationPlan{}); got != 0 {
		t.Fatalf("provider-less plan image target = %d, want 0", got)
	}
}

// TestVidRushImageTargetForSegmentTreatsPlannerCeilingAsCeiling pins the
// r44 regression: planner.max_images_per_scene is an admission ceiling, not
// a request to download that many generic scene images. Only images_per_scene
// asks for the generic lane; an entity-only plan must not inherit 10
// downloads per scene from the ceiling.
func TestVidRushImageTargetForSegmentTreatsPlannerCeilingAsCeiling(t *testing.T) {
	plan := &scriptpkg.ResolvedGenerationPlan{MediaPlan: media.MediaPlanSpec{
		Planner:        media.MediaPlannerPolicy{MaxImagesPerScene: 10},
		ProviderPolicy: media.MediaProviderPolicy{InternetImages: media.MediaToggleEnabled},
	}}
	if got := vidRushImageTargetForSegment(plan, scriptpkg.VidRushSegmentResult{}); got != vidRushDefaultImagesPerScene {
		t.Fatalf("planner ceiling leaked into materialization target = %d, want default %d", got, vidRushDefaultImagesPerScene)
	}
}

func TestVidRushImageTargetForSegmentKeepsExplicitCadence(t *testing.T) {
	plan := &scriptpkg.ResolvedGenerationPlan{ImagesPerScene: 10, MediaPlan: media.MediaPlanSpec{
		Planner:        media.MediaPlannerPolicy{MaxImagesPerScene: 12},
		ProviderPolicy: media.MediaProviderPolicy{InternetImages: media.MediaToggleEnabled},
	}}
	if got := vidRushImageTargetForSegment(plan, scriptpkg.VidRushSegmentResult{}); got != 10 {
		t.Fatalf("explicit ImagesPerScene target = %d, want 10", got)
	}
}

func TestVidRushImageTargetForSegmentKeepsEntityCoverageAboveCeiling(t *testing.T) {
	plan := &scriptpkg.ResolvedGenerationPlan{MediaPlan: media.MediaPlanSpec{
		Planner: media.MediaPlannerPolicy{MaxImagesPerScene: 2},
		Extraction: media.MediaExtractionPolicy{EntityImages: media.EntityImagePolicy{
			Enabled: true, Scope: "per_scene", MaxPerEntity: 3,
		}},
		ProviderPolicy: media.MediaProviderPolicy{InternetImages: media.MediaToggleEnabled},
	}}
	segment := scriptpkg.VidRushSegmentResult{Insights: scriptpkg.SegmentInsights{Entities: []scriptpkg.ExtractedEntity{
		{Value: "A", Type: "PERSON"}, {Value: "B", Type: "PERSON"}, {Value: "C", Type: "GPE"},
	}}}
	if got := vidRushImageTargetForSegment(plan, segment); got != 3 {
		t.Fatalf("entity coverage target = %d, want 3", got)
	}
}

// TestVidRushImageGroupCanonicalizesRetrievalQualifier pins the Floyd/Gotti
// failure: providers expand "John Cena" into "John Cena photo" and the old
// group key created a second slot for the same person. The canonical entity
// must win, and retrieval-only suffixes must not split the group.
func TestVidRushImageGroupCanonicalizesRetrievalQualifier(t *testing.T) {
	cases := []struct {
		name   string
		entity string
		query  string
		want   string
	}{
		{"entity wins over expanded query", "John Cena", "John Cena photo", "john cena"},
		{"query fallback strips photo", "", "John Cena photo", "john cena"},
		{"strips portrait", "Floyd Mayweather Jr", "Floyd Mayweather Jr portrait", "floyd mayweather jr"},
		{"keeps a real surname", "Michael Jordan", "Michael Jordan", "michael jordan"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := vidRushImageGroup(scriptpkg.SegmentAssetCandidate{Entity: tc.entity, Query: tc.query})
			if got != tc.want {
				t.Fatalf("group = %q, want %q", got, tc.want)
			}
		})
	}
	if got := vidRushImageGroup(scriptpkg.SegmentAssetCandidate{AssetID: "a-1"}); got != "asset:a-1" {
		t.Fatalf("asset fallback = %q, want asset:a-1", got)
	}
}
