package adapters

import (
	"strings"
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
	artlist := scriptpkg.BuildArtlistQueries(profile, 5)
	images := scriptpkg.BuildImageQueries(profile, 7)
	if len(youtube) == 0 || len(artlist) == 0 || len(images) == 0 {
		t.Fatal("all canonical provider builders must produce focused queries")
	}
	if youtube[0] == artlist[0] || artlist[0] == images[0] {
		t.Fatalf("provider queries were not adapted: yt=%v art=%v img=%v", youtube, artlist, images)
	}
}

func TestArtlistQueryBuilderDoesNotConsumeEditorialPhrases(t *testing.T) {
	profile := scriptpkg.SegmentSemanticProfile{
		VisualTerms: []scriptpkg.WeightedKeyword{
			{Value: "latte art", Confidence: 1},
			{Value: "specialty coffee shop", Confidence: .9},
		},
		Actions:          []string{"pouring steamed milk"},
		ImportantPhrases: []string{"Aerial drone footage reveals"},
		Keywords:         []scriptpkg.WeightedKeyword{{Value: "reveals", Confidence: 1}},
	}
	queries := scriptpkg.BuildArtlistQueries(profile, 5)
	joined := strings.ToLower(strings.Join(queries, " | "))
	if !strings.Contains(joined, "latte art") || !strings.Contains(joined, "specialty coffee shop") {
		t.Fatalf("canonical visual terms missing from Artlist queries: %v", queries)
	}
	if strings.Contains(joined, "aerial drone footage reveals") || strings.Contains(joined, "reveals") {
		t.Fatalf("editorial phrase/keyword leaked into Artlist queries: %v", queries)
	}
}
