package adapters

import (
	"strings"
	"testing"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
)

func TestSegmentSpecSceneContextIsolatesCurrentScene(t *testing.T) {
	input := scriptpkg.SpecSceneOutput{
		Version: 1,
		Scenes: []scriptpkg.SpecScene{
			{ID: "scene-1", SegmentID: "segment-001", Index: 0, Text: "first", Kind: scriptpkg.SceneClip},
			{ID: "scene-2", SegmentID: "segment-002", Index: 1, Text: "second", Kind: scriptpkg.SceneImage},
		},
	}

	got := segmentSpecSceneContext(input, scriptpkg.CanonicalSegment{
		ID:       "segment-002",
		SceneID:  "scene-2",
		Position: 1,
	})
	if len(got.Scenes) != 1 {
		t.Fatalf("expected one isolated scene, got %d", len(got.Scenes))
	}
	if got.Scenes[0].ID != "scene-2" {
		t.Fatalf("isolated scene id = %q, want scene-2", got.Scenes[0].ID)
	}
	if got.Scenes[0].Index != 0 {
		t.Fatalf("isolated scene index = %d, want 0", got.Scenes[0].Index)
	}
}

func TestSegmentQueryContextPrefersSourceSegmentOverGeneratedProse(t *testing.T) {
	plan := &scriptpkg.ResolvedGenerationPlan{
		SourceText: "Aerial drone footage reveals a winding coastal road at golden hour.\n\nA barista crafts latte art in a coffee shop.",
	}
	segment := scriptpkg.CanonicalSegment{ID: "segment-001", Position: 0, Text: "The world unfolds beneath us in a breathtaking tapestry."}
	if got := segmentQueryContext(plan, segment); got != "Aerial drone footage reveals a winding coastal road at golden hour." {
		t.Fatalf("query context = %q, want source paragraph", got)
	}
	result := buildVidRushSegmentResult(plan, segment, &scriptpkg.EntityResult{}, 5, 5, 5, 5, 5, segmentQueryContext(plan, segment))
	if !strings.Contains(strings.Join(result.Insights.ArtlistQueries, " | "), "coastal road") {
		t.Fatalf("Artlist queries = %v, want source-grounded coastal road query", result.Insights.ArtlistQueries)
	}
}

func TestBuildVidRushSegmentResultPreservesEntityType(t *testing.T) {
	result := buildVidRushSegmentResult(
		&scriptpkg.ResolvedGenerationPlan{},
		scriptpkg.CanonicalSegment{ID: "segment-001", Text: "OpenAI research", TextHash: "hash"},
		&scriptpkg.EntityResult{Concepts: []scriptpkg.Entity{{Value: "OpenAI", Type: "ORGANIZATION", Score: 0.98}}},
		5,
		5,
		5,
		5,
		5,
	)
	if len(result.Insights.Entities) != 1 {
		t.Fatalf("expected one entity, got %d", len(result.Insights.Entities))
	}
	if result.Insights.Entities[0].Type != "ORGANIZATION" {
		t.Fatalf("entity type = %q, want ORGANIZATION", result.Insights.Entities[0].Type)
	}
}
