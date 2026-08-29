package adapters

import (
	"context"
	"testing"

	scriptports "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/ports"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

type stubSegmentUnderstanding struct{}

func (stubSegmentUnderstanding) Understand(context.Context, scriptports.SegmentUnderstandingRequest) (scriptports.SegmentUnderstandingResult, error) {
	return scriptports.SegmentUnderstandingResult{
		Topic:            "Tesla Optimus demonstration",
		Subtopics:        []string{"humanoid robotics"},
		Keywords:         []string{"Optimus"},
		VisualTerms:      []string{"robot walking on stage"},
		ImportantPhrases: []string{"Optimus walking autonomously"},
		Actions:          []string{"walking"},
		VisualConcepts:   []string{"technology stage"},
	}, nil
}

func TestUnderstandProfileEnrichesSemanticFieldsWithoutChangingEntities(t *testing.T) {
	adapter := NewProfileSegmentUnderstandingModel(stubSegmentUnderstanding{})
	base := scriptpkg.SegmentSemanticProfile{
		SegmentID: "segment-1", TextHash: "hash-1", Topic: "original topic",
		Entities: []scriptpkg.ExtractedEntity{{Type: "ORG", Value: "Tesla", Confidence: 1}},
	}
	profile, err := adapter.UnderstandProfile(context.Background(), scriptpkg.CanonicalSegment{ID: "segment-1", Text: "Tesla demonstrates Optimus", TextHash: "hash-1"}, base, "en", "small-model", "prompt-v1")
	if err != nil {
		t.Fatal(err)
	}
	if profile.Topic != "original topic" || len(profile.Entities) != 1 || profile.Entities[0].Value != "Tesla" {
		t.Fatalf("base semantic/entity ownership changed: %+v", profile)
	}
	if len(profile.VisualTerms) != 1 || profile.VisualTerms[0].Value != "robot walking on stage" {
		t.Fatalf("visual terms not enriched: %+v", profile.VisualTerms)
	}
	if len(profile.Actions) != 1 || profile.Actions[0] != "walking" {
		t.Fatalf("actions not enriched: %+v", profile.Actions)
	}
}
