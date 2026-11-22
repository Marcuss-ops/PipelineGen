package rustexec

import (
	"encoding/json"
	"testing"
)

// The worker forwards the recipe payload verbatim. Dropping the field here was
// the exact gap that made every downstream recipe lane unreachable, so the
// decode is pinned on its own.
func TestProductionCandidateDecodesTheVisualPayload(t *testing.T) {
	const wire = `{
		"schema": "visual_consolidation.v1",
		"production_candidates": [{
			"id": "vc_0001",
			"recipe": "counter_odometer",
			"scene_id": "s01",
			"visual_payload": {"recipe": "counter_odometer", "value": 12, "subject_label": "Debito"}
		}]
	}`
	var decoded semanticEngineConsolidation
	if err := json.Unmarshal([]byte(wire), &decoded); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(decoded.ProductionCandidates) != 1 {
		t.Fatalf("candidates = %d, want 1", len(decoded.ProductionCandidates))
	}
	var payload map[string]any
	if err := json.Unmarshal(decoded.ProductionCandidates[0].VisualPayload, &payload); err != nil {
		t.Fatalf("payload is not an object: %v", err)
	}
	if payload["subject_label"] != "Debito" {
		t.Fatalf("payload = %v", payload)
	}
}
