package adapters

import (
	"testing"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

func TestVidRushProviderQueryBuildersAdaptByProvider(t *testing.T) {
	profile := scriptpkg.SegmentSemanticProfile{
		Topic:          "Operation Barbarossa",
		Entities:       []scriptpkg.ExtractedEntity{{Value: "Hitler", Type: "PERSON"}, {Value: "Soviet Union", Type: "PLACE"}},
		Actions:        []string{"tanks advancing"},
		VisualConcepts: []string{"German tanks advancing"},
		VisualTerms:    []scriptpkg.WeightedKeyword{{Value: "historical battlefield footage", Confidence: 1}},
	}
	youtube := scriptpkg.BuildYouTubeQueriesWithExplicit(profile, "Operation Barbarossa 1941 footage", 5)
	images := scriptpkg.BuildImageQueries(profile, 7)
	if len(youtube) == 0 || len(images) == 0 {
		t.Fatal("all canonical provider builders must produce focused queries")
	}
	if youtube[0] == images[0] {
		t.Fatalf("provider queries were not adapted: yt=%v img=%v", youtube, images)
	}
}
