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
	builders := NewVidRushProviderQueryBuilders()
	youtube := builders.YouTube(profile, "Operation Barbarossa 1941 footage")
	artlist := builders.Artlist(profile)
	images := builders.InternetImages(profile)
	generation := builders.ImageGeneration(profile)
	if len(youtube) == 0 || len(artlist) == 0 || len(images) == 0 || len(generation) == 0 {
		t.Fatal("all provider builders must produce focused queries")
	}
	if youtube[0] == artlist[0] || artlist[0] == images[0] || images[0] == generation[0] {
		t.Fatalf("provider queries were not adapted: yt=%v art=%v img=%v gen=%v", youtube, artlist, images, generation)
	}
}
