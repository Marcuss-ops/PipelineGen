package scriptgeneration

import (
	"encoding/json"
	"testing"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

func TestProjectEntityCompatibilityRestoresLegacySurfaces(t *testing.T) {
	entities := []scriptpkg.ExtractedEntity{
		{Value: "Maya", Type: "CONCEPT", Confidence: 0.95},
		{Value: "Tikal", Type: "LOCATION", Confidence: 0.94},
		{Value: "Palenque", Type: "LOCATION", Confidence: 0.93},
		{Value: "Chichen Itza", Type: "LOCATION", Confidence: 0.92},
		{Value: "Yucatan", Type: "LOCATION", Confidence: 0.91},
	}
	segments := []scriptpkg.VidRushSegmentResult{{
		SegmentID: "segment-0",
		SceneID:   "scene-0",
		Position:  0,
		Insights:  scriptpkg.SegmentInsights{Entities: entities},
	}}
	result := &GenerateResult{Entities: &scriptpkg.EntityResult{
		Concepts: []scriptpkg.Entity{{Value: "Maya", Type: "CONCEPT", Score: 0.95}},
	}}

	projectEntityCompatibility(result, segments)
	wire, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	var decoded struct {
		Segments  []scriptpkg.VidRushSegmentResult `json:"segments"`
		Artifacts struct {
			Entities *scriptpkg.EntityResult `json:"entities"`
		} `json:"artifacts"`
	}
	if err := json.Unmarshal(wire, &decoded); err != nil {
		t.Fatal(err)
	}
	if len(decoded.Segments) != 1 {
		t.Fatalf("segments=%d, want 1", len(decoded.Segments))
	}
	if len(decoded.Segments[0].Insights.Entities) < 5 {
		t.Fatalf("segment entities=%d, want at least 5", len(decoded.Segments[0].Insights.Entities))
	}
	if decoded.Artifacts.Entities == nil {
		t.Fatal("artifacts.entities is missing")
	}
	for i, entity := range decoded.Segments[0].Insights.Entities[:5] {
		if entity.Value == "" || entity.Type == "" {
			t.Fatalf("entity[%d] is invalid: %+v", i, entity)
		}
	}
}
